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

//go:build !go1.18
// +build !go1.18

package btree

import (
	"errors"
	"reflect"
)

// CursorDirection selects the traversal direction of a RangeCursor.
// Bounds are interpreted in traversal order: Lower is the side iteration
// starts at, Upper is the side it ends at.
type CursorDirection int

const (
	// CursorAscend walks items from the Lower bound towards the Upper bound
	// under the tree's Item ordering.
	CursorAscend CursorDirection = iota

	// CursorDescend walks items from the Upper bound towards the Lower bound
	// under the tree's Item ordering.
	CursorDescend
)

// Bound is one endpoint of a Range.
//
// A zero Bound (Set == false) means the endpoint is absent, i.e. iteration
// extends to the end of the tree on that side. When Set is true, Open decides
// whether the endpoint is exclusive (true) or inclusive (false); open and
// closed endpoints are therefore handled independently.
type Bound struct {
	// Value is the boundary key. It is only consulted when Set is true.
	Value Item
	// Open reports whether Value is exclusive. When false, Value is inclusive.
	Open bool
	// Set reports whether the bound is present.
	Set bool
}

// ClosedBound returns an inclusive bound at value, denoted [value ...] or
// [... value].
func ClosedBound(value Item) Bound { return Bound{Value: value, Set: true} }

// OpenBound returns an exclusive bound at value, denoted (value ...) or
// (... value).
func OpenBound(value Item) Bound { return Bound{Value: value, Open: true, Set: true} }

// Range is an open, closed, half-open or fully-unbounded interval expressed in
// the tree's Item ordering. Each endpoint independently selects whether it is
// present and whether it is inclusive or exclusive, so all four combinations
// of open and closed ends are supported: [lo, hi], [lo, hi), (lo, hi] and
// (lo, hi).
type Range struct {
	// Lower is the endpoint iteration starts at.
	Lower Bound
	// Upper is the endpoint iteration stops at.
	Upper Bound
}

// Errors returned by range cursor construction and paging.
var (
	// ErrWrongTree is returned when a cursor is asked to page against a
	// different BTree than the one it was created from.
	ErrWrongTree = errors.New("btree: range cursor is not bound to this tree")

	// ErrWrongComparator is returned when the comparator handed to a cursor is
	// not the exact Item.Less comparator of the bound tree.
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

// itemLessFunc is the canonical Item comparator value. Every legacy BTree
// orders through Item.Less; obtain the same value with ItemLess and pass it to
// NewRangeCursor to prove comparator identity.
func itemLessFunc(a, b Item) bool { return a.Less(b) }

// ItemLess returns the shared Item.Less comparator value used by every legacy
// BTree. Pass it to NewRangeCursor.
func ItemLess() func(a, b Item) bool { return itemLessFunc }

// RangeCursor is an explicit, bounded, resumable page cursor over a fixed
// snapshot of a BTree.
//
// The cursor binds the concrete BTree it was created from (checked by
// NextPage), the tree's Item.Less comparator, and an immutable Clone captured
// at construction time. Mutating the original tree afterwards never changes
// the pages a cursor returns: the cursor keeps reading its snapshot.
//
// Paging never rescans earlier pages: each Next call seeks the tree for the
// strict successor (ascending) or strict predecessor (descending) of the last
// item handed out, so the cost of a page is proportional to the page size
// plus one tree lookup, never to the number of previously read pages.
//
// When Next returns an empty page it always returns more == false: an empty
// page means the range is exhausted. An exhausted or closed cursor cannot be
// rewound or reused for another pass; create a fresh cursor instead.
//
// RangeCursor is not safe for concurrent use. Call Close when done to release
// the snapshot.
type RangeCursor struct {
	origin   *BTree // the live tree the cursor was created from
	snapshot *BTree // immutable clone captured at construction
	r        Range
	dir      direction
	start    Item // start key for the next iterate call
	include  bool // include start in the next iterate call
	hasStart bool // whether start is set
	started  bool // whether the first page was produced
	done     bool // range exhausted
	closed   bool // Close was called
}

// comparatorToken is the single function identity the legacy tree uses for
// ordering; see itemLessFunc and ItemLess.
var comparatorToken = itemLessFunc

// NewRangeCursor creates a RangeCursor over an immutable snapshot of t,
// restricted to r and traversing in dir.
//
// comparator must be the ordering of t; pass the value returned by ItemLess.
// Passing any other comparator instance returns ErrWrongComparator. Keys are
// always compared through the tree's real Item.Less semantics, never through
// reflection or as strings.
func NewRangeCursor(t *BTree, comparator func(a, b Item) bool, r Range, dir CursorDirection) (*RangeCursor, error) {
	if t == nil {
		return nil, ErrWrongTree
	}
	if comparator == nil ||
		reflect.ValueOf(comparator).Pointer() != reflect.ValueOf(comparatorToken).Pointer() {
		return nil, ErrWrongComparator
	}
	switch dir {
	case CursorAscend, CursorDescend:
	default:
		return nil, ErrInvalidRange
	}
	if r.Lower.Set && r.Upper.Set && r.Upper.Value.Less(r.Lower.Value) {
		return nil, ErrInvalidRange
	}
	return &RangeCursor{
		origin:   t,
		snapshot: t.Clone(),
		r:        r,
		dir:      dir.internal(),
	}, nil
}

// RangeCursor creates a range cursor over an immutable snapshot of t.
func (t *BTree) RangeCursor(r Range, dir CursorDirection) (*RangeCursor, error) {
	return NewRangeCursor(t, itemLessFunc, r, dir)
}

// Close releases the cursor's snapshot. It is idempotent. Next called after
// Close returns ErrCursorClosed.
func (c *RangeCursor) Close() {
	if c == nil || c.closed {
		return
	}
	c.closed = true
	c.done = true
	c.snapshot = nil
}

// Origin returns the live tree the cursor was created from. It is the same
// pointer NextPage accepts.
func (c *RangeCursor) Origin() *BTree { return c.origin }

// NextPage returns the next page through the cursor's snapshot, but only if t
// is the exact BTree the cursor was created from. Passing any other tree
// (including a Clone of the origin) returns ErrWrongTree without touching the
// cursor. Otherwise it behaves exactly like Next.
//
// The returned page is always read from the captured snapshot, never from t,
// so concurrent or later mutations of t do not affect paging.
func (c *RangeCursor) NextPage(t *BTree, pageSize int) (page []Item, more bool, err error) {
	if c == nil {
		return nil, false, ErrWrongTree
	}
	if t != c.origin {
		return nil, false, ErrWrongTree
	}
	return c.Next(pageSize)
}

// Next returns up to pageSize items in traversal order and whether more pages
// may follow. Paging starts at the range's starting endpoint and each later
// page starts at the strict successor (CursorAscend) or strict predecessor
// (CursorDescend) of the previous page's last item, so adjacent pages never
// repeat or skip an item.
//
// A returned empty page always comes with more == false and signals
// exhaustion; exhausted cursors cannot be rewound, create a new cursor for a
// new pass.
func (c *RangeCursor) Next(pageSize int) (page []Item, more bool, err error) {
	switch {
	case c == nil || c.closed:
		return nil, false, ErrCursorClosed
	case c.done:
		return nil, false, nil
	case pageSize <= 0:
		return nil, false, ErrInvalidPageSize
	case c.snapshot.root == nil:
		c.done = true
		return nil, false, nil
	}

	if !c.started {
		c.started = true
		switch c.dir {
		case ascend:
			if c.r.Lower.Set {
				c.start = c.r.Lower.Value
				c.hasStart = true
				c.include = !c.r.Lower.Open
			}
		case descend:
			if c.r.Upper.Set {
				c.start = c.r.Upper.Value
				c.hasStart = true
				c.include = !c.r.Upper.Open
			}
		}
	} else {
		// start holds the strict successor/predecessor fetched as the extra
		// item of the previous page: it is the very first item of this page.
		c.include = true
	}

	var start Item
	if c.hasStart {
		start = c.start
	}
	page = make([]Item, 0, pageSize)
	limit := pageSize + 1
	reachedBound := false

	c.snapshot.root.iterate(c.dir, start, nil, c.include, false, func(item Item) bool {
		if c.pastTerminal(item) {
			reachedBound = true
			return false
		}
		page = append(page, item)
		return len(page) < limit
	})

	if reachedBound || len(page) <= pageSize {
		c.done = true
		if len(page) == 0 {
			return nil, false, nil
		}
		return page, false, nil
	}

	c.start = page[pageSize]
	c.hasStart = true
	page = page[:pageSize]
	return page, true, nil
}

// pastTerminal reports whether item lies beyond the range's closing endpoint
// in traversal order.
func (c *RangeCursor) pastTerminal(item Item) bool {
	switch c.dir {
	case ascend:
		if !c.r.Upper.Set {
			return false
		}
		if c.r.Upper.Open {
			return !item.Less(c.r.Upper.Value) // item >= upper
		}
		return c.r.Upper.Value.Less(item) // item > upper
	default: // descend
		if !c.r.Lower.Set {
			return false
		}
		if c.r.Lower.Open {
			return !c.r.Lower.Value.Less(item) // item <= lower
		}
		return item.Less(c.r.Lower.Value) // item < lower
	}
}

func (d CursorDirection) internal() direction {
	switch d {
	case CursorAscend:
		return ascend
	case CursorDescend:
		return descend
	default:
		panic("invalid direction")
	}
}
