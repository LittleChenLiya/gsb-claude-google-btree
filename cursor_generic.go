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

// CursorDirection selects the traversal direction of a RangeCursorG.
// Bounds are interpreted in traversal order: Lower is the side iteration
// starts at, Upper is the side it ends at.
type CursorDirection int

const (
	// CursorAscend walks items from the Lower bound towards the Upper bound
	// under the tree's LessFunc ordering.
	CursorAscend CursorDirection = iota

	// CursorDescend walks items from the Upper bound towards the Lower bound
	// under the tree's LessFunc ordering.
	CursorDescend
)

func (d CursorDirection) internal() (direction, bool) {
	switch d {
	case CursorAscend:
		return ascend, true
	case CursorDescend:
		return descend, true
	default:
		return 0, false
	}
}

// Bound is one endpoint of a Range.
//
// A zero Bound (Set == false) means the endpoint is absent, i.e. iteration
// extends to the end of the tree on that side. When Set is true, Open decides
// whether the endpoint is exclusive (true) or inclusive (false); open and
// closed endpoints are therefore handled independently.
type Bound[T any] struct {
	// Value is the boundary key. It is only consulted when Set is true.
	Value T
	// Open reports whether Value is exclusive. When false, Value is inclusive.
	Open bool
	// Set reports whether the bound is present.
	Set bool
}

// ClosedBound returns an inclusive bound at value, denoted [value ...] or
// [... value].
func ClosedBound[T any](value T) Bound[T] {
	return Bound[T]{Value: value, Set: true}
}

// OpenBound returns an exclusive bound at value, denoted (value ...) or
// (... value).
func OpenBound[T any](value T) Bound[T] {
	return Bound[T]{Value: value, Open: true, Set: true}
}

// Range is an open, closed, half-open or fully-unbounded interval expressed in
// the tree's LessFunc ordering. Each endpoint independently selects whether it
// is present and whether it is inclusive or exclusive, so all four
// combinations of open and closed ends are supported: [lo, hi], [lo, hi),
// (lo, hi] and (lo, hi).
type Range[T any] struct {
	// Lower is the endpoint iteration starts at.
	Lower Bound[T]
	// Upper is the endpoint iteration stops at.
	Upper Bound[T]
}

// Errors returned by range cursor construction and paging.
var (
	// ErrWrongTree is returned when a cursor is asked to page against a
	// different BTree than the one it was created from.
	ErrWrongTree = errors.New("btree: range cursor is not bound to this tree")

	// ErrWrongComparator is returned when the LessFunc handed to a cursor is
	// not the exact comparator instance of the bound tree.
	ErrWrongComparator = errors.New("btree: range cursor comparator does not match tree comparator")

	// ErrInvalidRange is returned when the Range is malformed, such as an
	// Upper bound that sorts strictly before the Lower bound or an unknown
	// traversal direction.
	ErrInvalidRange = errors.New("btree: invalid range bounds")

	// ErrInvalidPageSize is returned when Next is called with a non-positive
	// page size.
	ErrInvalidPageSize = errors.New("btree: page size must be positive")

	// ErrCursorClosed is returned when Next is called after Close.
	ErrCursorClosed = errors.New("btree: range cursor is closed")
)

// RangeCursorG is an explicit, bounded, resumable page cursor over a fixed
// snapshot of a BTreeG.
//
// The cursor is created with NewRangeCursorG (or BTreeG.RangeCursor) and binds
// two identities for its whole lifetime:
//
//   - the concrete BTreeG it was created from (checked by NextPage), and
//   - that tree's exact LessFunc instance (checked at construction).
//
// It also captures an immutable Clone of the tree at construction time.
// Mutating the original tree afterwards never changes the pages a cursor
// returns: the cursor keeps reading its snapshot. Equivalently, the cursor is
// explicitly invalidated for live-tree reading and can only continue over the
// original snapshot; create a new cursor against the mutated tree to read new
// data.
//
// Paging never rescans earlier pages: each Next call seeks the tree for the
// strict successor (ascending) or strict predecessor (descending) of the last
// item handed out, so the cost of a page is proportional to the page size plus
// one tree lookup, never to the number of previously read pages.
//
// When Next returns an empty page it always returns more == false: an empty
// page means the range is exhausted. An exhausted or closed cursor cannot be
// rewound or reused for another pass; create a fresh cursor instead.
//
// RangeCursorG is not safe for concurrent use. Call Close when done to release
// the snapshot.
type RangeCursorG[T any] struct {
	origin   *BTreeG[T]      // the live tree the cursor was created from
	snapshot *BTreeG[T]      // immutable clone captured at construction
	less     LessFunc[T]     // exact comparator identity of the origin
	r        Range[T]        // bounded interval
	dir      direction       // internal traversal direction
	resume   optionalItem[T] // strict successor/predecessor for Next
	started  bool            // whether the first page was produced
	include  bool            // include resume on the next iterate call
	done     bool            // range exhausted
	closed   bool            // Close was called
}

// sameLessFunc reports whether a and b are the exact same comparator value.
// It compares function identity (entry-point pointer) only; it never inspects
// or compares keys.
func sameLessFunc(a, b any) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() != reflect.Func || vb.Kind() != reflect.Func {
		return false
	}
	return va.Pointer() == vb.Pointer()
}

// NewRangeCursorG creates a RangeCursorG over an immutable snapshot of t,
// restricted to r and traversing in dir.
//
// less must be the exact LessFunc value t was constructed with; passing a
// different comparator instance (even one with equivalent behaviour) returns
// ErrWrongComparator. Keys are never compared through reflection or converted
// to strings; ordering is taken solely from the tree's real LessFunc.
//
// Constructing a cursor clones t lazily (see BTreeG.Clone); do not mutate t
// concurrently with construction. The returned cursor and t may be used
// concurrently once NewRangeCursorG returns.
func NewRangeCursorG[T any](t *BTreeG[T], less LessFunc[T], r Range[T], dir CursorDirection) (*RangeCursorG[T], error) {
	if t == nil {
		return nil, ErrWrongTree
	}
	if less == nil || t.cow == nil || !sameLessFunc(less, t.cow.less) {
		return nil, ErrWrongComparator
	}
	id, ok := dir.internal()
	if !ok {
		return nil, ErrInvalidRange
	}
	if r.Lower.Set && r.Upper.Set && less(r.Upper.Value, r.Lower.Value) {
		return nil, ErrInvalidRange
	}
	return &RangeCursorG[T]{
		origin:   t,
		snapshot: t.Clone(),
		less:     less,
		r:        r,
		dir:      id,
	}, nil
}

// RangeCursor creates a RangeCursorG over an immutable snapshot of t. The
// comparator and tree identities are taken from t itself, so they cannot
// mismatch.
func (t *BTreeG[T]) RangeCursor(r Range[T], dir CursorDirection) (*RangeCursorG[T], error) {
	if t == nil {
		return nil, ErrWrongTree
	}
	return NewRangeCursorG(t, t.cow.less, r, dir)
}

// Origin returns the live tree the cursor was created from. It is the same
// pointer NextPage accepts.
func (c *RangeCursorG[T]) Origin() *BTreeG[T] { return c.origin }

// Close releases the cursor's snapshot. It is idempotent. Next called after
// Close returns ErrCursorClosed.
func (c *RangeCursorG[T]) Close() {
	if c == nil || c.closed {
		return
	}
	c.closed = true
	c.done = true
	c.snapshot = nil
}

// NextPage returns the next page through the cursor's snapshot, but only if t
// is the exact BTree the cursor was created from. Passing any other tree
// (including a Clone of the origin) returns ErrWrongTree without touching the
// cursor. Otherwise it behaves exactly like Next.
//
// The returned page is always read from the captured snapshot, never from t,
// so concurrent or later mutations of t do not affect paging.
func (c *RangeCursorG[T]) NextPage(t *BTreeG[T], pageSize int) (page []T, more bool, err error) {
	if c == nil {
		return nil, false, ErrWrongTree
	}
	if t != c.origin {
		return nil, false, ErrWrongTree
	}
	return c.Next(pageSize)
}

// Next returns up to pageSize items in traversal order and whether more pages
// may follow. Paging starts at the range's Lower endpoint and each later page
// starts at the strict successor (CursorAscend) or strict predecessor
// (CursorDescend) of the previous page's last item, so adjacent pages never
// repeat or skip an item.
//
// A returned empty page always comes with more == false and signals
// exhaustion; exhausted cursors cannot be rewound, create a new cursor for a
// new pass.
func (c *RangeCursorG[T]) Next(pageSize int) (page []T, more bool, err error) {
	switch {
	case c == nil || c.closed:
		return nil, false, ErrCursorClosed
	case c.done:
		// An exhausted cursor is not reusable: keep returning an empty page
		// without claiming that more data exists.
		return nil, false, nil
	case pageSize <= 0:
		return nil, false, ErrInvalidPageSize
	case c.snapshot.root == nil:
		c.done = true
		return nil, false, nil
	}

	// Resolve the start position for this page: the lower bound on the first
	// page, then the strict successor/predecessor of the prior page's end.
	if !c.started {
		c.started = true
		switch c.dir {
		case ascend:
			if c.r.Lower.Set {
				c.resume = optional(c.r.Lower.Value)
				c.include = !c.r.Lower.Open
			}
		case descend:
			if c.r.Upper.Set {
				c.resume = optional(c.r.Upper.Value)
				c.include = !c.r.Upper.Open
			}
		}
	} else {
		// resume holds the strict successor/predecessor fetched as the extra
		// item of the previous page: it is the very first item of this page,
		// so it is included rather than skipped.
		c.include = true
	}

	page = make([]T, 0, pageSize)
	limit := pageSize + 1 // fetch one extra to learn whether more pages exist
	reachedBound := false

	c.snapshot.root.iterate(c.dir, c.resume, empty[T](), c.include, false, func(item T) bool {
		if c.pastTerminal(item) {
			reachedBound = true
			return false
		}
		page = append(page, item)
		return len(page) < limit
	})

	if reachedBound || len(page) <= pageSize {
		// No extra item was fetched: the range (and therefore the cursor) is
		// exhausted. An empty page is returned as nil with more == false, so
		// it can never be mistaken for a promise that the cursor remains
		// usable.
		c.done = true
		if len(page) == 0 {
			return nil, false, nil
		}
		return page, false, nil
	}

	// The (pageSize+1)th item belongs to the next page. Remember it as the
	// strict resume position; the following iterate call excludes it, so it
	// surfaces as the first item of the next page exactly once.
	c.resume = optional(page[pageSize])
	page = page[:pageSize]
	return page, true, nil
}

// pastTerminal reports whether item lies beyond the range's closing endpoint
// in traversal order.
func (c *RangeCursorG[T]) pastTerminal(item T) bool {
	switch c.dir {
	case ascend:
		if !c.r.Upper.Set {
			return false
		}
		if c.r.Upper.Open {
			return !c.less(item, c.r.Upper.Value) // item >= upper
		}
		return c.less(c.r.Upper.Value, item) // item > upper
	default: // descend
		if !c.r.Lower.Set {
			return false
		}
		if c.r.Lower.Open {
			return !c.less(c.r.Lower.Value, item) // item <= lower
		}
		return c.less(item, c.r.Lower.Value) // item < lower
	}
}

// RangeCursor is the non-generic, Item-based counterpart of RangeCursorG. It
// is returned by BTree.RangeCursor / NewRangeCursor and binds the tree's
// built-in Item.Less comparator.
type RangeCursor struct {
	c *RangeCursorG[Item]
}

// itemLessFunc is the canonical Item comparator value, used by every legacy
// BTree.
func itemLessFunc(a, b Item) bool { return a.Less(b) }

// ItemLess returns the shared Item.Less comparator value used by every legacy
// BTree. Pass it to NewRangeCursor.
func ItemLess() func(a, b Item) bool { return itemLessFunc }

// NewRangeCursor creates a RangeCursor over an immutable snapshot of t,
// restricted to r and traversing in dir.
//
// comparator must be the value returned by ItemLess; passing any other
// comparator instance returns ErrWrongComparator.
func NewRangeCursor(t *BTree, comparator func(a, b Item) bool, r Range[Item], dir CursorDirection) (*RangeCursor, error) {
	if t == nil {
		return nil, ErrWrongTree
	}
	if comparator == nil || !sameLessFunc(comparator, itemLessFunc) {
		return nil, ErrWrongComparator
	}
	c, err := NewRangeCursorG((*BTreeG[Item])(t), itemLess, r, dir)
	if err != nil {
		return nil, err
	}
	return &RangeCursor{c: c}, nil
}

// RangeCursor creates a range cursor over an immutable snapshot of t.
func (t *BTree) RangeCursor(r Range[Item], dir CursorDirection) (*RangeCursor, error) {
	return NewRangeCursor(t, itemLessFunc, r, dir)
}

// Close releases the cursor's snapshot. It is idempotent.
func (c *RangeCursor) Close() {
	if c == nil {
		return
	}
	c.c.Close()
}

// NextPage behaves like RangeCursorG.NextPage, accepting only the exact BTree
// the cursor was created from.
func (c *RangeCursor) NextPage(t *BTree, pageSize int) ([]Item, bool, error) {
	if c == nil {
		return nil, false, ErrWrongTree
	}
	if (*BTree)(c.c.origin) != t {
		return nil, false, ErrWrongTree
	}
	return c.c.Next(pageSize)
}

// Next returns the next page; see RangeCursorG.Next for semantics.
func (c *RangeCursor) Next(pageSize int) ([]Item, bool, error) {
	if c == nil {
		return nil, false, ErrCursorClosed
	}
	return c.c.Next(pageSize)
}
