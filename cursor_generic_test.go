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
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// cmpCount wraps less while counting invocations so that tests can prove later
// pages never rescan the keys already handed out on earlier pages.
type cmpCount[T any] struct {
	less  LessFunc[T]
	calls int
}

func (c *cmpCount[T]) fn() LessFunc[T] {
	return func(a, b T) bool {
		c.calls++
		return c.less(a, b)
	}
}

// drain pages a cursor to exhaustion and returns the concatenation of all
// pages plus the sizes of the individual non-empty pages. The terminal call
// signals the end with more == false.
func drain[T any](t *testing.T, c *RangeCursorG[T], pageSize int) ([]T, []int) {
	t.Helper()
	var all []T
	var sizes []int
	for {
		page, more, err := c.Next(pageSize)
		if err != nil {
			t.Fatalf("unexpected cursor error: %v", err)
		}
		if len(page) == 0 {
			if more {
				t.Fatal("empty page returned with more=true")
			}
			// Empty terminal page: no further data and no resumable cursor.
			return all, sizes
		}
		sizes = append(sizes, len(page))
		all = append(all, page...)
		if !more {
			return all, sizes
		}
	}
}

// TestRangeCursorPaging covers the headline acceptance: five keys paged two at
// a time yield page sizes 2,2,1 with no duplicate or missing key.
func TestRangeCursorPaging(t *testing.T) {
	tr := NewOrderedG[int](2)
	for _, k := range []int{1, 2, 3, 4, 5} {
		tr.ReplaceOrInsert(k)
	}
	c, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	all, sizes := drain(t, c, 2)
	if want := []int{2, 2, 1}; !reflect.DeepEqual(sizes, want) {
		t.Fatalf("page sizes = %v, want %v", sizes, want)
	}
	if want := []int{1, 2, 3, 4, 5}; !reflect.DeepEqual(all, want) {
		t.Fatalf("paged keys = %v, want %v", all, want)
	}
}

// TestRangeCursorDescending verifies that a reverse comparator genuinely
// reverses every page, not just the keys inside a page.
func TestRangeCursorDescending(t *testing.T) {
	reverse := func(a, b int) bool { return a > b }
	tr := NewG[int](2, reverse)
	for _, k := range []int{3, 1, 4, 1, 5, 9, 2, 6} {
		tr.ReplaceOrInsert(k)
	}
	c, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	all, _ := drain(t, c, 3)
	if want := []int{9, 6, 5, 4, 3, 2, 1}; !reflect.DeepEqual(all, want) {
		t.Fatalf("reverse-order paging = %v, want %v", all, want)
	}

	// CursorDescend under the same comparator walks towards smaller reverse
	// order, i.e. numerically ascending.
	c2, err := tr.RangeCursor(Range[int]{}, CursorDescend)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	all2, _ := drain(t, c2, 3)
	if want := []int{1, 2, 3, 4, 5, 6, 9}; !reflect.DeepEqual(all2, want) {
		t.Fatalf("descend paging = %v, want %v", all2, want)
	}
}

// TestRangeCursorWrongTreeAndComparator covers rejection of the wrong tree and
// the wrong comparator.
func TestRangeCursorWrongTreeAndComparator(t *testing.T) {
	tr := NewOrderedG[int](2)
	for _, k := range []int{1, 2, 3} {
		tr.ReplaceOrInsert(k)
	}
	other := NewOrderedG[int](2)

	// Wrong comparator instance: a freshly written Less[int] closure is not
	// the exact value tr holds, even though it orders identically.
	if _, err := NewRangeCursorG(tr, Less[int](), Range[int]{}, CursorAscend); err != ErrWrongComparator {
		t.Fatalf("wrong comparator: got %v, want ErrWrongComparator", err)
	}
	// Nil comparator is rejected too.
	if _, err := NewRangeCursorG(tr, nil, Range[int]{}, CursorAscend); err != ErrWrongComparator {
		t.Fatalf("nil comparator: got %v, want ErrWrongComparator", err)
	}
	// A comparator from a differently ordered tree is rejected as well.
	reverse := func(a, b int) bool { return a > b }
	trRev := NewG[int](2, reverse)
	if _, err := NewRangeCursorG(trRev, Less[int](), Range[int]{}, CursorAscend); err != ErrWrongComparator {
		t.Fatalf("foreign comparator: got %v, want ErrWrongComparator", err)
	}

	c, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Paging through a different tree (including a clone) is rejected and
	// does not advance the cursor.
	if _, _, err := c.NextPage(other, 2); err != ErrWrongTree {
		t.Fatalf("wrong tree: got %v, want ErrWrongTree", err)
	}
	if _, _, err := c.NextPage(tr.Clone(), 2); err != ErrWrongTree {
		t.Fatalf("clone tree: got %v, want ErrWrongTree", err)
	}
	if c.Origin() != tr {
		t.Fatal("Origin must be the tree passed at construction")
	}

	// The rejected calls must not have consumed a page.
	page, more, err := c.NextPage(tr, 2)
	if err != nil || !more || !reflect.DeepEqual(page, []int{1, 2}) {
		t.Fatalf("cursor advanced despite rejection: page=%v more=%v err=%v", page, more, err)
	}
}

// TestRangeCursorBounds independently exercises every open/closed combination
// of both endpoints, including equality against keys actually present.
func TestRangeCursorBounds(t *testing.T) {
	mk := func() *BTreeG[int] {
		tr := NewOrderedG[int](2)
		for _, k := range []int{1, 2, 3, 4, 5} {
			tr.ReplaceOrInsert(k)
		}
		return tr
	}
	cases := []struct {
		name     string
		r        Range[int]
		dir      CursorDirection
		wantAsc  []int
		pageSize int
	}{
		{"[2,5)", Range[int]{Lower: ClosedBound(2), Upper: OpenBound(5)}, CursorAscend, []int{2, 3, 4}, 2},
		{"[2,5]", Range[int]{Lower: ClosedBound(2), Upper: ClosedBound(5)}, CursorAscend, []int{2, 3, 4, 5}, 2},
		{"(2,5)", Range[int]{Lower: OpenBound(2), Upper: OpenBound(5)}, CursorAscend, []int{3, 4}, 2},
		{"(2,5]", Range[int]{Lower: OpenBound(2), Upper: ClosedBound(5)}, CursorAscend, []int{3, 4, 5}, 2},
		{"[2,2]", Range[int]{Lower: ClosedBound(2), Upper: ClosedBound(2)}, CursorAscend, []int{2}, 1},
		{"(2,2)", Range[int]{Lower: OpenBound(2), Upper: OpenBound(2)}, CursorAscend, nil, 1},
		{"[0,10)", Range[int]{Lower: ClosedBound(0), Upper: OpenBound(10)}, CursorAscend, []int{1, 2, 3, 4, 5}, 3},
		{"(2,nil", Range[int]{Lower: OpenBound(2)}, CursorAscend, []int{3, 4, 5}, 4},
		{"nil,4]", Range[int]{Upper: ClosedBound(4)}, CursorAscend, []int{1, 2, 3, 4}, 2},
		{"[2,5) descend", Range[int]{Lower: ClosedBound(2), Upper: OpenBound(5)}, CursorDescend, []int{4, 3, 2}, 2},
		{"(2,5] descend", Range[int]{Lower: OpenBound(2), Upper: ClosedBound(5)}, CursorDescend, []int{5, 4, 3}, 2},
		{"(2,5) descend", Range[int]{Lower: OpenBound(2), Upper: OpenBound(5)}, CursorDescend, []int{4, 3}, 1},
		{"(1,5) descend", Range[int]{Lower: OpenBound(1), Upper: OpenBound(5)}, CursorDescend, []int{4, 3, 2}, 5},
		{"nil,3] descend", Range[int]{Upper: ClosedBound(3)}, CursorDescend, []int{3, 2, 1}, 2},
		{"(1,nil descend", Range[int]{Lower: OpenBound(1)}, CursorDescend, []int{5, 4, 3, 2}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := mk().RangeCursor(tc.r, tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			got, _ := drain(t, c, tc.pageSize)
			if !reflect.DeepEqual(got, tc.wantAsc) {
				t.Fatalf("paged = %v, want %v", got, tc.wantAsc)
			}
		})
	}
}

// TestRangeCursorEqualitySemantics checks that "equal" means what the tree
// means: equivalent keys collapse to one stored item, and bounds use the
// comparator rather than string or numeric identity.
func TestRangeCursorEqualitySemantics(t *testing.T) {
	// Two struct keys are equivalent when their ordering field matches,
	// even though they are not deeply equal.
	type row struct {
		id    int
		label string
	}
	less := func(a, b row) bool { return a.id < b.id }
	tr := NewG[row](2, less)
	tr.ReplaceOrInsert(row{1, "first"})
	tr.ReplaceOrInsert(row{2, "two"})
	if got, replaced := tr.ReplaceOrInsert(row{2, "second"}); got.label != "two" || !replaced {
		t.Fatalf("tree replacement semantics broken: %+v %v", got, replaced)
	}

	c, err := tr.RangeCursor(Range[row]{
		Lower: ClosedBound(row{id: 1, label: "IGNORED"}),
		Upper: ClosedBound(row{id: 2, label: "IGNORED"}),
	}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	all, _ := drain(t, c, 10)
	if len(all) != 2 || all[0].label != "first" || all[1].label != "second" {
		t.Fatalf("equality must follow the comparator, got %+v", all)
	}
}

// TestRangeCursorSnapshot guarantees that mutating the origin tree after a
// cursor is created neither changes its pages nor corrupts the live tree: the
// cursor keeps reading the captured snapshot.
func TestRangeCursorSnapshot(t *testing.T) {
	tr := NewOrderedG[int](2)
	for _, k := range []int{1, 2, 3, 4, 5} {
		tr.ReplaceOrInsert(k)
	}
	c, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// First page from the pre-mutation snapshot.
	page, more, err := c.Next(2)
	if err != nil || !more || !reflect.DeepEqual(page, []int{1, 2}) {
		t.Fatalf("first page = %v %v %v", page, more, err)
	}

	// Mutate the live tree heavily: deletions, inserts, replacements.
	tr.Delete(3)
	tr.ReplaceOrInsert(40)
	tr.ReplaceOrInsert(6)
	if tr.Len() != 6 {
		t.Fatalf("live tree length = %d, want 6", tr.Len())
	}

	// The cursor finishes over the original snapshot.
	rest, sizes := drain(t, c, 2)
	want := []int{3, 4, 5}
	if !reflect.DeepEqual(rest, want) {
		t.Fatalf("snapshot paging after mutation = %v (sizes %v), want %v", rest, sizes, want)
	}

	// A fresh cursor observes the new live state.
	c2, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	live, _ := drain(t, c2, 10)
	if want := []int{1, 2, 4, 5, 6, 40}; !reflect.DeepEqual(live, want) {
		t.Fatalf("fresh cursor = %v, want %v", live, want)
	}
}

// TestRangeCursorNoRescan proves each page is produced with work proportional
// to its own size plus one tree seek: comparator invocations while fetching a
// later page must stay bounded and must not grow with the number of keys on
// earlier pages.
func TestRangeCursorNoRescan(t *testing.T) {
	const n = 256
	cnt := &cmpCount[int]{less: Less[int]()}
	tr := NewG[int](2, cnt.fn()) // minimal degree => deep tree
	for k := 0; k < n; k++ {
		tr.ReplaceOrInsert(k)
	}
	c, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var maxCalls int
	pages := 0
	var total int
	for {
		cnt.calls = 0
		page, more, err := c.Next(8)
		if err != nil {
			t.Fatal(err)
		}
		if calls := cnt.calls; calls > maxCalls {
			maxCalls = calls
		}
		total += len(page)
		if !more {
			break
		}
		pages++
		_ = page
	}
	// Even the most expensive page only performs one root-to-leaf seek plus
	// the comparisons of the ~9 visited items; empirically this stays below
	// 30 calls. A rescan of earlier pages would grow this with the number of
	// already-consumed keys (hundreds of calls on the last pages).
	if maxCalls >= 64 {
		t.Fatalf("page cost %d comparisons suggests earlier pages were rescanned", maxCalls)
	}
	// Every key is delivered exactly once.
	if total != n {
		t.Fatalf("paged %d keys, want %d", total, n)
	}
	// 256 keys at 8 per page: 31 full pages followed by a final 8-key page.
	if pages != n/8-1 {
		t.Fatalf("paged %d full pages, want %d", pages, n/8-1)
	}
}

// TestRangeCursorEmptyAndLifecycle covers empty ranges/pages, empty trees,
// invalid arguments, exhaustion and close semantics.
func TestRangeCursorEmptyAndLifecycle(t *testing.T) {
	tr := NewOrderedG[int](2)
	for _, k := range []int{1, 2, 3} {
		tr.ReplaceOrInsert(k)
	}

	// Empty range between equal open bounds: first page is empty and more is
	// false; the cursor must not pretend it is still usable.
	c, err := tr.RangeCursor(Range[int]{Lower: OpenBound(2), Upper: OpenBound(2)}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	page, more, err := c.Next(2)
	if err != nil || page != nil || more {
		t.Fatalf("empty range: page=%v more=%v err=%v", page, more, err)
	}
	// Repeated Next on the exhausted cursor stays empty and terminal.
	page, more, err = c.Next(2)
	if err != nil || page != nil || more {
		t.Fatalf("exhausted cursor reuse: page=%v more=%v err=%v", page, more, err)
	}
	c.Close()
	// After close the cursor reports a clear error.
	if _, _, err := c.Next(2); err != ErrCursorClosed {
		t.Fatalf("closed cursor: got %v, want ErrCursorClosed", err)
	}
	// Close is idempotent.
	c.Close()

	// Empty tree cursor is immediately terminal.
	empty := NewOrderedG[int](2)
	c2, err := empty.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	if page, more, err := c2.Next(5); err != nil || page != nil || more {
		t.Fatalf("empty tree: page=%v more=%v err=%v", page, more, err)
	}
	c2.Close()

	// Invalid direction and backwards ranges.
	if _, err := tr.RangeCursor(Range[int]{}, CursorDirection(99)); err != ErrInvalidRange {
		t.Fatalf("bad direction: got %v", err)
	}
	backwards := Range[int]{Lower: ClosedBound(5), Upper: ClosedBound(1)}
	if _, err := tr.RangeCursor(backwards, CursorAscend); err != ErrInvalidRange {
		t.Fatalf("backwards range: got %v, want ErrInvalidRange", err)
	}
	// Equal bounds are not backwards: [5,5] is simply empty.
	if _, err := tr.RangeCursor(Range[int]{Lower: ClosedBound(5), Upper: ClosedBound(5)}, CursorAscend); err != nil {
		t.Fatalf("equal bounds: got %v", err)
	}

	c3, err := tr.RangeCursor(Range[int]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	if _, _, err := c3.Next(0); err != ErrInvalidPageSize {
		t.Fatalf("zero page size: got %v, want ErrInvalidPageSize", err)
	}
	if _, _, err := c3.Next(-1); err != ErrInvalidPageSize {
		t.Fatalf("negative page size: got %v", err)
	}
}

// TestRangeCursorLegacy exercises the non-generic Item-based wrapper.
func TestRangeCursorLegacy(t *testing.T) {
	tr := New(2)
	for _, k := range []int{1, 2, 3, 4, 5} {
		tr.ReplaceOrInsert(Int(k))
	}
	c, err := NewRangeCursor(tr, ItemLess(), Range[Item]{}, CursorAscend)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var got []int
	var sizes []int
	for {
		page, more, err := c.NextPage(tr, 2)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(page))
		for _, it := range page {
			got = append(got, int(it.(Int)))
		}
		if !more {
			break
		}
	}
	if wantSizes := []int{2, 2, 1}; !reflect.DeepEqual(sizes, wantSizes) {
		t.Fatalf("page sizes = %v, want %v", sizes, wantSizes)
	}
	if want := []int{1, 2, 3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paged keys = %v, want %v", got, want)
	}

	// Descending with an inclusive upper and exclusive lower.
	c2, err := tr.RangeCursor(Range[Item]{
		Lower: OpenBound[Item](Int(2)),
		Upper: ClosedBound[Item](Int(5)),
	}, CursorDescend)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var rev []int
	for {
		page, more, err := c2.Next(2)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page {
			rev = append(rev, int(it.(Int)))
		}
		if !more {
			break
		}
	}
	if want := []int{5, 4, 3}; !reflect.DeepEqual(rev, want) {
		t.Fatalf("legacy descending = %v, want %v", rev, want)
	}

	// Wrong tree rejection for the legacy API.
	other := New(2)
	if _, _, err := c.NextPage(other, 2); err != ErrWrongTree {
		t.Fatalf("legacy wrong tree: got %v, want ErrWrongTree", err)
	}
	foreign := func(a, b Item) bool { return a.Less(b) }
	if _, err := NewRangeCursor(tr, foreign, Range[Item]{}, CursorAscend); err != ErrWrongComparator {
		t.Fatalf("legacy foreign comparator: got %v, want ErrWrongComparator", err)
	}
}

// TestRangeCursorDifferential compares paged cursor output against a plain
// in-order collection with explicit bound filtering, across degrees, page
// sizes, ranges (including absent/equal/outside endpoints) and both
// directions, to guarantee no skips, repeats or bound mistakes.
func TestRangeCursorDifferential(t *testing.T) {
	less := Less[int]()
	rng := rand.New(rand.NewSource(1))
	for degree := 2; degree <= 6; degree++ {
		tr := NewG[int](degree, less)
		keys := rng.Perm(120)
		for _, k := range keys {
			tr.ReplaceOrInsert(k)
		}
		var sorted []int
		tr.Ascend(func(k int) bool {
			sorted = append(sorted, k)
			return true
		})

		// Candidate boundary values: absent, present and outside the key set.
		picks := []int{-10, -1, 0, 1, 7, 30, 59, 60, 100, 119, 120, 130}
		bounds := []Bound[int]{{}} // absent bound (unbounded side)
		for _, p := range picks {
			bounds = append(bounds, ClosedBound(p), OpenBound(p))
		}
		for _, lo := range bounds {
			for _, hi := range bounds {
				valid := !lo.Set || !hi.Set || !less(hi.Value, lo.Value)
				for _, dir := range []CursorDirection{CursorAscend, CursorDescend} {
					r := Range[int]{Lower: lo, Upper: hi}
					if !valid {
						if _, err := tr.RangeCursor(r, dir); err != ErrInvalidRange {
							t.Fatalf("backwards range accepted: %+v", r)
						}
						continue
					}
					c, err := tr.RangeCursor(r, dir)
					if err != nil {
						t.Fatalf("r=%+v dir=%v: %v", r, dir, err)
					}
					pageSize := 1 + rng.Intn(9)
					got, _ := drain(t, c, pageSize)
					c.Close()

					var want []int
					for _, k := range sorted {
						ok := true
						if lo.Set {
							if lo.Open {
								ok = less(lo.Value, k) // k > lo
							} else {
								ok = !less(k, lo.Value) // k >= lo
							}
						}
						if ok && hi.Set {
							if hi.Open {
								ok = less(k, hi.Value) // k < hi
							} else {
								ok = !less(hi.Value, k) // k <= hi
							}
						}
						if ok {
							want = append(want, k)
						}
					}
					if dir == CursorDescend {
						for i := len(want)/2 - 1; i >= 0; i-- {
							j := len(want) - 1 - i
							want[i], want[j] = want[j], want[i]
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("degree=%d r=%+v dir=%v ps=%d:\n got %v\nwant %v",
							degree, r, dir, pageSize, got, want)
					}
				}
			}
		}
	}
}

// ExampleBTreeG_rangeCursorPaging pages five keys two at a time over the
// bounded interval [2, 5).
func ExampleBTreeG_rangeCursorPaging() {
	less := Less[int]() // one comparator value, shared by tree and cursor
	tr := NewG[int](4, less)
	for _, k := range []int{1, 2, 3, 4, 5} {
		tr.ReplaceOrInsert(k)
	}

	c, err := NewRangeCursorG(tr, less, Range[int]{
		Lower: ClosedBound(2),
		Upper: OpenBound(5),
	}, CursorAscend)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer c.Close()

	for pageNo := 1; ; pageNo++ {
		page, more, err := c.Next(2)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("page %d: %v (more=%v)\n", pageNo, page, more)
		if !more {
			break
		}
	}
	// The exhausted cursor keeps returning an empty terminal page and never
	// claims to be reusable for another pass.
	page, more, _ := c.Next(2)
	fmt.Printf("reuse: %v (more=%v)\n", page, more)
	// Output:
	// page 1: [2 3] (more=true)
	// page 2: [4] (more=false)
	// reuse: [] (more=false)
}

// ExampleBTreeG_rangeCursorDescending pages the interval (1, 5] from the
// closed upper end downwards, i.e. using CursorDescend with independently
// specified open/closed endpoints.
func ExampleBTreeG_rangeCursorDescending() {
	tr := NewOrderedG[int](4)
	for _, k := range []int{0, 1, 2, 3, 4, 5, 6} {
		tr.ReplaceOrInsert(k)
	}

	c, err := tr.RangeCursor(Range[int]{
		Lower: OpenBound(1),
		Upper: ClosedBound(5),
	}, CursorDescend)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer c.Close()

	for pageNo := 1; ; pageNo++ {
		page, more, err := c.Next(2)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("page %d: %v (more=%v)\n", pageNo, page, more)
		if !more {
			break
		}
	}
	// Output:
	// page 1: [5 4] (more=true)
	// page 2: [3 2] (more=false)
}
