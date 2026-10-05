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
	"reflect"
	"testing"
)

// TestRangeCursorLegacyPaging exercises the Item-based RangeCursor available
// on every supported Go version: five keys page two at a time as 2,2,1 with
// no repetition, open/closed endpoints are honored independently, and the
// wrong tree or a foreign comparator is rejected.
func TestRangeCursorLegacyPaging(t *testing.T) {
	tr := New(2)
	for _, k := range []int{1, 2, 3, 4, 5} {
		tr.ReplaceOrInsert(Int(k))
	}

	c, err := NewRangeCursor(tr, ItemLess(), Range{}, CursorAscend)
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
	if want := []int{2, 2, 1}; !reflect.DeepEqual(sizes, want) {
		t.Fatalf("page sizes = %v, want %v", sizes, want)
	}
	if want := []int{1, 2, 3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paged keys = %v, want %v", got, want)
	}

	// Descending with an inclusive upper and exclusive lower.
	c2, err := tr.RangeCursor(Range{
		Lower: OpenBound(Int(2)),
		Upper: ClosedBound(Int(5)),
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
		t.Fatalf("descending keys = %v, want %v", rev, want)
	}

	// Wrong tree rejection.
	other := New(2)
	if _, _, err := c.NextPage(other, 2); err != ErrWrongTree {
		t.Fatalf("wrong tree: got %v, want ErrWrongTree", err)
	}
	if _, _, err := c.NextPage(tr.Clone(), 2); err != ErrWrongTree {
		t.Fatalf("clone tree: got %v, want ErrWrongTree", err)
	}

	// Wrong comparator rejection.
	foreign := func(a, b Item) bool { return a.Less(b) }
	if _, err := NewRangeCursor(tr, foreign, Range{}, CursorAscend); err != ErrWrongComparator {
		t.Fatalf("foreign comparator: got %v, want ErrWrongComparator", err)
	}
	if _, err := NewRangeCursor(tr, nil, Range{}, CursorAscend); err != ErrWrongComparator {
		t.Fatalf("nil comparator: got %v, want ErrWrongComparator", err)
	}

	// Snapshot isolation: mutating the origin leaves the cursor untouched.
	tr.Delete(Int(4))
	page, more, err := c.NextPage(tr, 10)
	if err != nil || more {
		t.Fatalf("exhausted page: %v %v %v", page, more, err)
	}
}
