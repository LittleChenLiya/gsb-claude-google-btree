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
	"reflect"
	"testing"
)

func cursorPages(t *testing.T, cur *CursorG[int], tr *BTreeG[int], limit int) [][]int {
	t.Helper()
	var pages [][]int
	for {
		page, err := cur.Next(tr, limit)
		if err == ErrCursorExhausted {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		pages = append(pages, page)
	}
	return pages
}

func TestCursorPaging(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	cur := tr.Cursor(RangeG[int]{})
	pages := cursorPages(t, cur, tr, 2)
	want := [][]int{{0, 1}, {2, 3}, {4}}
	if !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages: got %v want %v", pages, want)
	}
	if !cur.Done() {
		t.Fatal("cursor should report Done after the final page")
	}
	// One more call after the final partial page is explicitly exhausted.
	if _, err := cur.Next(tr, 2); err != ErrCursorExhausted {
		t.Fatalf("want ErrCursorExhausted after pages, got %v", err)
	}
	// No key is returned twice: flattening the pages yields each key once.
	var flat []int
	for _, p := range pages {
		flat = append(flat, p...)
	}
	if !reflect.DeepEqual(flat, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("duplicates or missing keys across pages: %v", flat)
	}
}

func TestCursorReverseComparator(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a > b })
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	cur := tr.Cursor(RangeG[int]{})
	pages := cursorPages(t, cur, tr, 2)
	want := [][]int{{4, 3}, {2, 1}, {0}}
	if !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages with reverse comparator: got %v want %v", pages, want)
	}
}

func TestCursorWrongTree(t *testing.T) {
	less := func(a, b int) bool { return a < b }
	tr1 := NewG[int](2, less)
	tr2 := NewG[int](2, less) // same comparator value, distinct tree
	for i := 0; i < 5; i++ {
		tr1.ReplaceOrInsert(i)
		tr2.ReplaceOrInsert(i)
	}
	cur := tr1.Cursor(RangeG[int]{})
	if _, err := cur.Next(tr2, 2); err != ErrCursorTreeMismatch {
		t.Fatalf("want ErrCursorTreeMismatch, got %v", err)
	}
}

func TestCursorWrongComparator(t *testing.T) {
	less := func(a, b int) bool { return a < b }
	rev := func(a, b int) bool { return a > b }
	tr1 := NewG[int](2, less)
	tr2 := NewG[int](2, rev)
	for i := 0; i < 5; i++ {
		tr1.ReplaceOrInsert(i)
		tr2.ReplaceOrInsert(i)
	}
	cur := tr1.Cursor(RangeG[int]{})
	if _, err := cur.Next(tr2, 2); err != ErrCursorComparatorMismatch {
		t.Fatalf("want ErrCursorComparatorMismatch, got %v", err)
	}
}

func TestCursorEmptyPage(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	// A range whose lower bound is above its upper bound holds no keys.
	cur := tr.Cursor(RangeG[int]{Lower: 10, Upper: 2, HasLower: true, HasUpper: true})
	page, err := cur.Next(tr, 2)
	if err != ErrCursorExhausted {
		t.Fatalf("want ErrCursorExhausted for empty range, got %v (page %v)", err, page)
	}
	if page != nil {
		t.Fatalf("empty page must be nil, got %v", page)
	}
	if !cur.Done() {
		t.Fatal("cursor must report Done after an empty page")
	}
	// The empty page does not pretend the cursor is reusable.
	page, err = cur.Next(tr, 2)
	if err != ErrCursorExhausted {
		t.Fatalf("want ErrCursorExhausted on reuse, got %v", err)
	}
	if page != nil {
		t.Fatalf("reused page must be nil, got %v", page)
	}
}

func TestCursorEmptyTree(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	cur := tr.Cursor(RangeG[int]{})
	page, err := cur.Next(tr, 2)
	if err != ErrCursorExhausted {
		t.Fatalf("want ErrCursorExhausted on empty tree, got %v", err)
	}
	if page != nil {
		t.Fatalf("page must be nil, got %v", page)
	}
}

func TestCursorBadLimit(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	tr.ReplaceOrInsert(1)
	cur := tr.Cursor(RangeG[int]{})
	for _, limit := range []int{0, -1} {
		if _, err := cur.Next(tr, limit); err != ErrCursorBadLimit {
			t.Fatalf("limit %d: want ErrCursorBadLimit, got %v", limit, err)
		}
	}
}

func TestCursorOpenClosedBounds(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	cases := []struct {
		name string
		rng  RangeG[int]
		want []int
	}{
		{"closed-closed", RangeG[int]{Lower: 1, Upper: 3, HasLower: true, HasUpper: true}, []int{1, 2, 3}},
		{"open-lower", RangeG[int]{Lower: 1, Upper: 3, LowerOpen: true, HasLower: true, HasUpper: true}, []int{2, 3}},
		{"open-upper", RangeG[int]{Lower: 1, Upper: 3, UpperOpen: true, HasLower: true, HasUpper: true}, []int{1, 2}},
		{"open-open", RangeG[int]{Lower: 1, Upper: 3, LowerOpen: true, UpperOpen: true, HasLower: true, HasUpper: true}, []int{2}},
		{"no-bounds", RangeG[int]{}, []int{0, 1, 2, 3, 4}},
		{"lower-only-closed", RangeG[int]{Lower: 3, HasLower: true}, []int{3, 4}},
		{"lower-only-open", RangeG[int]{Lower: 3, LowerOpen: true, HasLower: true}, []int{4}},
		{"upper-only-closed", RangeG[int]{Upper: 1, HasUpper: true}, []int{0, 1}},
		{"upper-only-open", RangeG[int]{Upper: 1, UpperOpen: true, HasUpper: true}, []int{0}},
		{"exclusive-lower-past-end", RangeG[int]{Lower: 4, LowerOpen: true, HasLower: true}, nil},
		{"inclusive-upper-at-key", RangeG[int]{Upper: 4, HasUpper: true}, []int{0, 1, 2, 3, 4}},
	}
	for _, tc := range cases {
		cur := tr.Cursor(tc.rng)
		var got []int
		for {
			page, err := cur.Next(tr, 100)
			if err == ErrCursorExhausted {
				break
			}
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got = append(got, page...)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestCursorInvalidation(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	cur := tr.Cursor(RangeG[int]{})
	page, err := cur.Next(tr, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0] != 0 || page[1] != 1 {
		t.Fatalf("first page: got %v", page)
	}
	tr.ReplaceOrInsert(100)
	if _, err := cur.Next(tr, 2); err != ErrCursorInvalidated {
		t.Fatalf("want ErrCursorInvalidated after tree mutation, got %v", err)
	}
}

func TestCursorSnapshotClone(t *testing.T) {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	cur := tr.Cursor(RangeG[int]{})
	// Clone and mutate the clone; the cursor bound to tr keeps the snapshot.
	clone := tr.Clone()
	clone.ReplaceOrInsert(100)
	want := [][]int{{0, 1}, {2, 3}, {4}}
	for i, w := range want {
		page, err := cur.Next(tr, 2)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		if !reflect.DeepEqual(page, w) {
			t.Fatalf("page %d: got %v want %v", i, page, w)
		}
	}
	if _, err := cur.Next(tr, 2); err != ErrCursorExhausted {
		t.Fatalf("want ErrCursorExhausted, got %v", err)
	}
}

func TestCursorNonGeneric(t *testing.T) {
	tr := New(2)
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(Int(i))
	}
	cur := tr.Cursor(Range{})
	var pages [][]Item
	for {
		page, err := cur.Next(tr, 2)
		if err == ErrCursorExhausted {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, page)
	}
	want := [][]Item{{Int(0), Int(1)}, {Int(2), Int(3)}, {Int(4)}}
	if !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages: got %v want %v", pages, want)
	}
}

func ExampleBTreeG_Cursor() {
	tr := NewG[int](2, func(a, b int) bool { return a < b })
	for i := 0; i < 10; i++ {
		tr.ReplaceOrInsert(i)
	}
	// Range (2, 8]: keys strictly greater than 2 and less than or equal to 8.
	cur := tr.Cursor(RangeG[int]{
		Lower:     2,
		LowerOpen: true,
		Upper:     8,
		HasLower:  true,
		HasUpper:  true,
	})
	for {
		page, err := cur.Next(tr, 3)
		if err == ErrCursorExhausted {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(page)
	}
	// Output:
	// [3 4 5]
	// [6 7 8]
}

func ExampleBTreeG_Cursor_reverse() {
	tr := NewG[int](2, func(a, b int) bool { return a > b }) // descending order
	for i := 0; i < 6; i++ {
		tr.ReplaceOrInsert(i)
	}
	// The tree orders keys from largest to smallest. Express the range in
	// that same order: from Lower=5 (inclusive) down to Upper=3 (inclusive).
	cur := tr.Cursor(RangeG[int]{
		Lower:    5,
		Upper:    3,
		HasLower: true,
		HasUpper: true,
	})
	for {
		page, err := cur.Next(tr, 2)
		if err == ErrCursorExhausted {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(page)
	}
	// Output:
	// [5 4]
	// [3]
}
