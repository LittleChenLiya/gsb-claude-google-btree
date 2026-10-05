// Copyright 2026 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build go1.18
// +build go1.18

package btree

import (
	"errors"
	"reflect"
)

// RangeG describes a bounded key range for a CursorG.
//
// The zero value is an unbounded range covering every key in the tree. Set
// HasLower/HasUpper to enable the corresponding bound; LowerOpen/UpperOpen
// control whether the bound is strict (exclusive) or not (inclusive). Bounds
// are interpreted using the tree's own comparator: a key K is in the range
// when (lower disabled, or K >= Lower, or K > Lower when LowerOpen) and
// (upper disabled, or K <= Upper, or K < Upper when UpperOpen), where
// equality and ordering are exactly those defined by the comparator (two keys
// are equal when neither compares less than the other).
type RangeG[T any] struct {
	Lower     T
	LowerOpen bool // true: exclude Lower (K > Lower); false: include it (K >= Lower)
	Upper     T
	UpperOpen bool // true: exclude Upper (K < Upper); false: include it (K <= Upper)
	HasLower  bool
	HasUpper  bool
}

// Sentinel errors returned by CursorG.Next.
var (
	// ErrCursorExhausted is returned when the cursor's range has been fully
	// iterated. The cursor is not reusable: every subsequent Next call returns
	// the same error.
	ErrCursorExhausted = errors.New("btree: cursor range exhausted")

	// ErrCursorInvalidated is returned when the tree the cursor was bound to
	// has been modified after the cursor was created. The cursor does not
	// silently observe the change; callers that need a stable view across
	// writes should Clone the tree first and create the cursor on the clone.
	ErrCursorInvalidated = errors.New("btree: cursor invalidated by tree modification")

	// ErrCursorTreeMismatch is returned when Next is called with a tree other
	// than the one the cursor was created from.
	ErrCursorTreeMismatch = errors.New("btree: cursor bound to a different tree")

	// ErrCursorComparatorMismatch is returned when Next is called with a tree
	// whose comparator differs from the one the cursor was created with.
	ErrCursorComparatorMismatch = errors.New("btree: cursor bound to a different comparator")

	// ErrCursorBadLimit is returned when Next is called with a limit <= 0.
	ErrCursorBadLimit = errors.New("btree: cursor page limit must be positive")
)

// CursorG is an explicit, bounded, paged range cursor over a BTreeG snapshot.
//
// A cursor is bound to the identity of the tree it was created from and to the
// comparator in effect at creation time. It iterates a fixed RangeG in pages
// of at most a caller-supplied limit; each page starts at the strict
// successor of the last key of the previous page, so pages never overlap and
// no key is returned twice. Advancing to the next page seeks directly to its
// first key and does not scan the keys returned by previous pages.
//
// If the bound tree is modified after the cursor is created, Next returns
// ErrCursorInvalidated rather than silently observing the change. To iterate
// a stable snapshot across writes, Clone the tree first and create the cursor
// on the clone; the cursor then keeps reading the shared, immutable nodes.
type CursorG[T any] struct {
	tree *BTreeG[T]
	root *node[T]
	less LessFunc[T]
	gen  uint64
	rng  RangeG[T]
	pos  optionalItem[T] // last key returned; the next page starts strictly after it
	done bool
}

// Cursor returns a new CursorG over the given range, bound to t's current
// snapshot and comparator.
func (t *BTreeG[T]) Cursor(rng RangeG[T]) *CursorG[T] {
	if t == nil {
		panic("btree: Cursor on nil tree")
	}
	return &CursorG[T]{
		tree: t,
		root: t.root,
		less: t.cow.less,
		gen:  t.gen,
		rng:  rng,
	}
}

// Done reports whether the cursor's range has been fully iterated.
func (c *CursorG[T]) Done() bool {
	return c.done
}

// Next advances the cursor and returns the next page of at most limit items
// from the cursor's range.
//
// A returned page always contains at least one item. When the range is
// exhausted, Next returns a nil page and ErrCursorExhausted, and every
// subsequent call returns the same; the cursor is not reusable.
//
// Next returns ErrCursorBadLimit if limit <= 0, ErrCursorComparatorMismatch
// if t uses a different comparator than the cursor, ErrCursorTreeMismatch if
// t is not the tree the cursor was created from, and ErrCursorInvalidated if
// the tree has been modified since the cursor was created.
func (c *CursorG[T]) Next(t *BTreeG[T], limit int) (page []T, err error) {
	if c.done {
		return nil, ErrCursorExhausted
	}
	if limit <= 0 {
		return nil, ErrCursorBadLimit
	}
	if t == nil || t.cow == nil {
		return nil, ErrCursorInvalidated
	}
	// Comparator identity is the semantic identity of the tree; check it
	// first so a cursor used with a differently-ordered tree is rejected as
	// such even if the tree itself also differs. Function values are not
	// directly comparable, so compare their code pointers via reflect.
	if !sameLessFunc(t.cow.less, c.less) {
		return nil, ErrCursorComparatorMismatch
	}
	if t != c.tree {
		return nil, ErrCursorTreeMismatch
	}
	if t.gen != c.gen {
		return nil, ErrCursorInvalidated
	}
	if c.root == nil {
		c.done = true
		return nil, ErrCursorExhausted
	}
	start := c.pos
	startInclusive := false
	if !c.pos.valid && c.rng.HasLower {
		// First page: position at the lower bound itself (inclusive) or at
		// its strict successor (exclusive).
		start = optionalItem[T]{item: c.rng.Lower, valid: true}
		startInclusive = !c.rng.LowerOpen
	}
	var upper optionalItem[T]
	if c.rng.HasUpper {
		upper = optionalItem[T]{item: c.rng.Upper, valid: true}
	}
	page, complete := c.root.collectPage(make([]T, 0, limit), limit, start, startInclusive, upper, c.rng.UpperOpen, c.less)
	if len(page) > 0 {
		c.pos = optionalItem[T]{item: page[len(page)-1], valid: true}
	}
	if complete || len(page) < limit {
		c.done = true
	}
	if c.done && len(page) == 0 {
		return nil, ErrCursorExhausted
	}
	return page, nil
}

// collectPage appends in-order items from n to dst that are greater than
// *start (strictly greater when startInclusive is false, greater than or
// equal when true), bounded by upper (items less than upper when upperOpen,
// less than or equal when closed). It stops after limit items have been
// appended. The returned complete flag reports whether the subtree was fully
// consumed (no more in-range items exist in it).
//
// Positioning uses the comparator only: find locates the start key and the
// upper bound is tested with the same less function, so equal keys follow the
// tree's true equality semantics and no reflection or string comparison is
// involved.
func (n *node[T]) collectPage(dst []T, limit int, start optionalItem[T], startInclusive bool, upper optionalItem[T], upperOpen bool, less LessFunc[T]) (out []T, complete bool) {
	if len(dst) >= limit {
		return dst, false
	}
	index := 0
	if start.valid {
		idx, found := n.items.find(start.item, less)
		index = idx
		if found && !startInclusive {
			// Strict successor: skip the equal key itself.
			index++
		}
	}
	for i := index; i < len(n.items); i++ {
		if len(n.children) > 0 {
			var childComplete bool
			dst, childComplete = n.children[i].collectPage(dst, limit, start, startInclusive, upper, upperOpen, less)
			if len(dst) >= limit {
				return dst, false
			}
			if !childComplete {
				return dst, false
			}
		}
		if upper.valid {
			if upperOpen {
				if !less(n.items[i], upper.item) {
					return dst, true
				}
			} else {
				if less(upper.item, n.items[i]) {
					return dst, true
				}
			}
		}
		dst = append(dst, n.items[i])
		if len(dst) >= limit {
			return dst, false
		}
	}
	if len(n.children) > 0 {
		var childComplete bool
		dst, childComplete = n.children[len(n.children)-1].collectPage(dst, limit, start, startInclusive, upper, upperOpen, less)
		if len(dst) >= limit {
			return dst, false
		}
		if !childComplete {
			return dst, false
		}
	}
	return dst, true
}

// sameLessFunc reports whether two comparator functions are the same
// function. Function values are not comparable with ==, so their identity is
// compared via the underlying code pointer.
func sameLessFunc[T any](a, b LessFunc[T]) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// Range is the non-generic alias of RangeG.
type Range = RangeG[Item]

// Cursor is a paged, bounded range cursor for the non-generic BTree.
//
// See CursorG for the full semantics.
type Cursor struct {
	c *CursorG[Item]
}

// Cursor returns a new cursor over the given range, bound to t's current
// snapshot and comparator.
func (t *BTree) Cursor(rng Range) *Cursor {
	return &Cursor{c: (*BTreeG[Item])(t).Cursor(RangeG[Item](rng))}
}

// Done reports whether the cursor's range has been fully iterated.
func (c *Cursor) Done() bool {
	return c.c.Done()
}

// Next advances the cursor and returns the next page of at most limit items
// from the cursor's range. See CursorG.Next for the error semantics.
func (c *Cursor) Next(t *BTree, limit int) ([]Item, error) {
	page, err := c.c.Next((*BTreeG[Item])(t), limit)
	if page == nil {
		return nil, err
	}
	out := make([]Item, len(page))
	for i, v := range page {
		out[i] = v
	}
	return out, err
}
