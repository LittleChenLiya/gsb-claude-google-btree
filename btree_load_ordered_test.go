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
	"fmt"
	"reflect"
	"testing"
)

// kvItem is an item whose key determines the order but whose value travels
// alongside it, so that "last item wins" is observable for equal keys.
type kvItem struct {
	key   int
	value string
}

func (a kvItem) Less(b Item) bool {
	return a.key < b.(kvItem).key
}

func kvLess(a, b kvItem) bool { return a.key < b.key }

// flLenG reports how many nodes are currently cached in a generic freelist.
func flLenG[T any](f *FreeListG[T]) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.freelist)
}

// checkInvariantsG verifies B-Tree node invariants over a built tree:
// children/items relation, per-node min/max fill, strict item ordering,
// key bounds, equal leaf depth, and total item count matching Len.
func checkInvariantsG[T any](t *testing.T, tr *BTreeG[T]) {
	t.Helper()
	if tr.Len() == 0 {
		if tr.root != nil {
			t.Fatalf("empty tree has a root")
		}
		return
	}
	maxItems, minItems := tr.maxItems(), tr.minItems()
	less := tr.cow.less

	var countItems func(n *node[T]) int
	countItems = func(n *node[T]) int {
		c := len(n.items)
		for _, ch := range n.children {
			c += countItems(ch)
		}
		return c
	}
	if got := countItems(tr.root); got != tr.Len() {
		t.Fatalf("subtree holds %d items but Len=%d", got, tr.Len())
	}

	var check func(n *node[T], depth int, isRoot bool, lo, hi *optionalItem[T]) int
	check = func(n *node[T], depth int, isRoot bool, lo, hi *optionalItem[T]) int {
		if len(n.children) != 0 && len(n.children) != len(n.items)+1 {
			t.Fatalf("children=%d items=%d", len(n.children), len(n.items))
		}
		if len(n.items) > maxItems {
			t.Fatalf("node has %d items > max %d", len(n.items), maxItems)
		}
		if !isRoot && len(n.items) < minItems {
			t.Fatalf("non-root node has %d items < min %d", len(n.items), minItems)
		}
		for i := 1; i < len(n.items); i++ {
			if !less(n.items[i-1], n.items[i]) {
				t.Fatalf("items not strictly ordered: %v !< %v", n.items[i-1], n.items[i])
			}
		}
		for _, it := range n.items {
			if lo != nil && !less(lo.item, it) {
				t.Fatalf("item %v <= lower bound %v", it, lo.item)
			}
			if hi != nil && !less(it, hi.item) {
				t.Fatalf("item %v >= upper bound %v", it, hi.item)
			}
		}
		if len(n.children) == 0 {
			return depth
		}
		var leafDepth int
		for i, child := range n.children {
			var childLo, childHi *optionalItem[T]
			if i > 0 {
				v := optional(n.items[i-1])
				childLo = &v
			} else {
				childLo = lo
			}
			if i < len(n.items) {
				v := optional(n.items[i])
				childHi = &v
			} else {
				childHi = hi
			}
			d := check(child, depth+1, false, childLo, childHi)
			if i == 0 {
				leafDepth = d
			} else if d != leafDepth {
				t.Fatalf("unequal leaf depths %d and %d", d, leafDepth)
			}
		}
		return leafDepth
	}
	check(tr.root, 0, true, nil, nil)
}

// Five ordered-batch inputs exercising the acceptance scenarios.
type loadCase struct {
	name    string
	items   []kvItem
	policy  DuplicatePolicy
	wantErr *LoadError
}

func loadCases() []loadCase {
	return []loadCase{
		{
			name:   "01_strictly_ordered",
			items:  []kvItem{{1, "a"}, {2, "b"}, {3, "c"}, {5, "e"}, {8, "h"}},
			policy: RejectDuplicates,
		},
		{
			name:    "02_out_of_order",
			items:   []kvItem{{1, "a"}, {4, "d"}, {3, "c"}, {5, "e"}},
			policy:  RejectDuplicates,
			wantErr: &LoadError{Kind: KindOutOfOrder, Index: 2, Item: Item(kvItem{3, "c"})},
		},
		{
			name:    "03_duplicate_reject",
			items:   []kvItem{{1, "a"}, {2, "first"}, {2, "second"}, {4, "d"}, {7, "g"}},
			policy:  RejectDuplicates,
			wantErr: &LoadError{Kind: KindDuplicate, Index: 2, Item: Item(kvItem{2, "second"})},
		},
		{
			name:   "04_duplicate_last_wins",
			items:  []kvItem{{1, "a"}, {2, "first"}, {2, "second"}, {4, "d"}, {7, "g"}},
			policy: LastWins,
		},
		{
			name:   "05_last_wins_run_then_ordered",
			items:  []kvItem{{1, "a"}, {2, "v1"}, {2, "v2"}, {2, "v3"}, {3, "c"}, {6, "f"}, {9, "i"}},
			policy: LastWins,
		},
	}
}

func expectedItems(c loadCase) []kvItem {
	if c.wantErr != nil {
		return nil
	}
	out := make([]kvItem, 0, len(c.items))
	for _, it := range c.items {
		if len(out) > 0 && it.key == out[len(out)-1].key {
			out[len(out)-1] = it // last wins across the whole equal run
		} else {
			out = append(out, it)
		}
	}
	return out
}

func indexOfKey(items []kvItem, key int) int {
	for i, it := range items {
		if it.key == key {
			return i
		}
	}
	return -1
}

// preloadedFreeListG builds a tree, clears its nodes back into a big freelist,
// and returns the empty tree (using the freelist) and the freelist.
func preloadedFreeListG(t *testing.T, degree int) (*BTreeG[kvItem], *FreeListG[kvItem]) {
	fl := NewFreeListG[kvItem](256)
	tr := NewWithFreeListG[kvItem](degree, kvLess, fl)
	for i := 0; i < 60; i++ {
		tr.ReplaceOrInsert(kvItem{1000 + i, "seed"})
	}
	tr.Clear(true)
	if flLenG(fl) == 0 {
		t.Fatal("test setup: freelist unexpectedly empty after Clear")
	}
	return tr, fl
}

func TestBTreeGLoadOrderedFiveInputs(t *testing.T) {
	for _, degree := range []int{2, 3, 4, 8} {
		for _, tc := range loadCases() {
			t.Run(fmt.Sprintf("degree%d/%s", degree, tc.name), func(t *testing.T) {
				tr, fl := preloadedFreeListG(t, degree)
				before := flLenG(fl)
				// Pass a copy: LastWins is documented to compact in place into
				// the input slice's backing array.
				in := append([]kvItem(nil), tc.items...)

				err := tr.LoadOrdered(in, tc.policy)
				if tc.wantErr != nil {
					if err == nil {
						t.Fatalf("want error %v, got nil", tc.wantErr)
					}
					// A generic tree returns *LoadErrorG[kvItem]; inspect it
					// via the concrete error type rather than the Item alias.
					le, ok := err.(*LoadErrorG[kvItem])
					if !ok {
						t.Fatalf("want *LoadErrorG[kvItem], got %T: %v", err, err)
					}
					if le.Kind != tc.wantErr.Kind || le.Index != tc.wantErr.Index {
						t.Fatalf("error mismatch: got kind=%d index=%d, want kind=%d index=%d",
							le.Kind, le.Index, tc.wantErr.Kind, tc.wantErr.Index)
					}
					if got := le.Item; got != tc.wantErr.Item.(kvItem) {
						t.Fatalf("error item mismatch: got %v want %v", got, tc.wantErr.Item)
					}
					if tr.Len() != 0 || tr.root != nil {
						t.Fatalf("tree not empty after failed preflight: len=%d root=%v", tr.Len(), tr.root)
					}
					// Failed preflight must not consume any FreeList node.
					if after := flLenG(fl); after != before {
						t.Fatalf("freelist changed on failure: before=%d after=%d", before, after)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				got := expectedItems(tc)[:0:0]
				tr.Ascend(func(it kvItem) bool { got = append(got, it); return true })
				if want := expectedItems(tc); !reflect.DeepEqual(got, want) {
					t.Fatalf("iterated items:\n got: %v\nwant: %v", got, want)
				}
				if tr.Len() != len(got) {
					t.Fatalf("Len=%d, iterated %d", tr.Len(), len(got))
				}
				// LastWins observability: key 2 must carry the last value.
				if tc.policy == LastWins {
					want := expectedItems(tc)
					if i := indexOfKey(got, 2); i >= 0 && got[i].value != want[i].value {
						t.Fatalf("last-wins value not retained for key 2: got %q want %q",
							got[i].value, want[i].value)
					}
				}
				checkInvariantsG(t, tr)
			})
		}
	}
}

func TestBTreeGLoadOrderedFreelistReuse(t *testing.T) {
	tr, fl := preloadedFreeListG(t, 4)
	before := flLenG(fl)
	items := []kvItem{{1, "a"}, {2, "b"}, {3, "c"}, {4, "d"}, {5, "e"},
		{6, "f"}, {7, "g"}, {8, "h"}, {9, "i"}, {10, "j"}}
	if err := tr.LoadOrdered(items, RejectDuplicates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A successful build along the existing node structure draws nodes from
	// the pre-populated freelist.
	if after := flLenG(fl); after >= before {
		t.Fatalf("freelist not consumed on success: before=%d after=%d", before, after)
	}
	var got []int
	tr.Ascend(func(it kvItem) bool { got = append(got, it.key); return true })
	if want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Mutating the bulk-built tree via existing operations keeps working.
	tr.Delete(kvItem{key: 4})
	tr.ReplaceOrInsert(kvItem{11, "k"})
	got = nil
	tr.Ascend(func(it kvItem) bool { got = append(got, it.key); return true })
	if want := []int{1, 2, 3, 5, 6, 7, 8, 9, 10, 11}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-build mutation mismatch: got %v want %v", got, want)
	}
	checkInvariantsG(t, tr)
}

// Both flavors (generic BTreeG and non-generic BTree) must iterate identically.
func TestLoadOrderedGenericAndNonGenericAgree(t *testing.T) {
	for _, tc := range loadCases() {
		t.Run(tc.name, func(t *testing.T) {
			gtr := NewG[kvItem](3, kvLess)
			ntr := New(3)
			nitems := make([]Item, len(tc.items))
			for i, it := range tc.items {
				nitems[i] = it
			}

			gerr := gtr.LoadOrdered(tc.items, tc.policy)
			nerr := ntr.LoadOrdered(nitems, tc.policy)
			if (gerr == nil) != (nerr == nil) {
				t.Fatalf("error presence mismatch: generic=%v nongeneric=%v", gerr, nerr)
			}
			if gerr != nil {
				gl, ok := gerr.(*LoadErrorG[kvItem])
				if !ok {
					t.Fatalf("generic: want *LoadErrorG[kvItem], got %T", gerr)
				}
				nl, ok := nerr.(*LoadError)
				if !ok {
					t.Fatalf("nongeneric: want *LoadError, got %T", nerr)
				}
				if gl.Kind != nl.Kind || gl.Index != nl.Index {
					t.Fatalf("error mismatch: generic kind=%d idx=%d; nongeneric kind=%d idx=%d",
						gl.Kind, gl.Index, nl.Kind, nl.Index)
				}
				return
			}
			var ggot []kvItem
			gtr.Ascend(func(it kvItem) bool { ggot = append(ggot, it); return true })
			var ngot []kvItem
			ntr.Ascend(func(it Item) bool { ngot = append(ngot, it.(kvItem)); return true })
			if !reflect.DeepEqual(ggot, ngot) {
				t.Fatalf("iteration mismatch:\n generic:    %v\n nongeneric: %v", ggot, ngot)
			}
			if gtr.Len() != ntr.Len() {
				t.Fatalf("Len mismatch: generic=%d nongeneric=%d", gtr.Len(), ntr.Len())
			}
		})
	}
}

func TestLoadOrderedConstructorsAndGuards(t *testing.T) {
	// Package-level entry points.
	tr, err := NewOrderedGFromOrdered(4, []int{1, 2, 3, 5, 8}, RejectDuplicates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkInvariantsG(t, tr)

	ntr, err := NewFromOrdered(4, []Item{Int(1), Int(2), Int(3), Int(5), Int(8)}, RejectDuplicates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ngot []int
	ntr.Ascend(func(it Item) bool { ngot = append(ngot, int(it.(Int))); return true })
	if want := []int{1, 2, 3, 5, 8}; !reflect.DeepEqual(ngot, want) {
		t.Fatalf("got %v want %v", ngot, want)
	}

	// An empty batch yields an empty tree and no error.
	empty := NewOrderedG[int](4)
	if err := empty.LoadOrdered(nil, RejectDuplicates); err != nil {
		t.Fatalf("empty batch: unexpected error %v", err)
	}
	if empty.Len() != 0 || empty.root != nil {
		t.Fatalf("empty batch produced a tree")
	}

	// Loading into a non-empty tree is rejected.
	if err := tr.LoadOrdered([]int{1, 9}, RejectDuplicates); !errors.Is(err, ErrTreeNotEmpty) {
		t.Fatalf("want ErrTreeNotEmpty, got %v", err)
	}

	// Out-of-order failure leaves the tree empty with no freelist consumption.
	fl := NewFreeListG[int](16)
	ftr := NewWithFreeListG[int](4, Less[int](), fl)
	before := flLenG(fl)
	err = ftr.LoadOrdered([]int{1, 3, 2}, RejectDuplicates)
	var le *LoadErrorG[int]
	if !errors.As(err, &le) || le.Kind != KindOutOfOrder || le.Index != 2 {
		t.Fatalf("want out-of-order error at index 2, got %v", err)
	}
	if ftr.Len() != 0 || ftr.root != nil {
		t.Fatalf("tree mutated after failed preflight")
	}
	if after := flLenG(fl); after != before {
		t.Fatalf("freelist changed on failure: before=%d after=%d", before, after)
	}
}

// TestBulkBuildMatchesIncrementalInsert builds trees from ordered batches of
// many sizes and degrees via LoadOrdered and verifies contents and node
// invariants against a tree filled with ReplaceOrInsert.
func TestBulkBuildMatchesIncrementalInsert(t *testing.T) {
	for _, degree := range []int{2, 3, 5, 16} {
		for n := 0; n <= 400; n++ {
			items := make([]int, n)
			for i := range items {
				items[i] = i
			}
			bulk := NewOrderedG[int](degree)
			if err := bulk.LoadOrdered(items, RejectDuplicates); err != nil {
				t.Fatalf("degree=%d n=%d: %v", degree, n, err)
			}
			inc := NewOrderedG[int](degree)
			for _, it := range items {
				inc.ReplaceOrInsert(it)
			}
			if bulk.Len() != inc.Len() {
				t.Fatalf("degree=%d n=%d: bulk len %d != insert len %d",
					degree, n, bulk.Len(), inc.Len())
			}
			var bgot, igot []int
			bulk.Ascend(func(i int) bool { bgot = append(bgot, i); return true })
			inc.Ascend(func(i int) bool { igot = append(igot, i); return true })
			if !reflect.DeepEqual(bgot, igot) {
				t.Fatalf("degree=%d n=%d: iteration mismatch", degree, n)
			}
			// The bulk-built tree must remain usable under ordinary mutations.
			if n > 0 {
				bulk.Delete(n / 2)
				if bulk.Has(n / 2) {
					t.Fatalf("degree=%d n=%d: delete did not take", degree, n)
				}
				bulk.ReplaceOrInsert(n + 1)
				checkInvariantsG(t, bulk)
			}
			checkInvariantsG(t, bulk)
		}
	}
}

// ExampleNewFromOrdered is the small in-memory fixture demonstrating the
// result of both duplicate policies on a batch containing a duplicate.
func ExampleNewFromOrdered() {
	batch := []Item{Int(1), Int(2), Int(2), Int(4)}

	_, err := NewFromOrdered(4, batch, RejectDuplicates)
	fmt.Println("reject:", err)

	t2, err := NewFromOrdered(4, batch, LastWins)
	if err != nil {
		fmt.Println("lastwins error:", err)
		return
	}
	t2.Ascend(func(i Item) bool {
		fmt.Print(i, " ")
		return true
	})
	fmt.Println("len:", t2.Len())
	// Output:
	// reject: btree: duplicate key at index 2 (2)
	// 1 2 4 len: 3
}
