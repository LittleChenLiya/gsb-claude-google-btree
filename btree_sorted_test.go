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
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func collectG[T any](t *BTreeG[T]) []T {
	out := []T{}
	t.Ascend(func(i T) bool {
		out = append(out, i)
		return true
	})
	return out
}

func collectItems(t *BTree) []Item {
	out := []Item{}
	t.Ascend(func(i Item) bool {
		out = append(out, i)
		return true
	})
	return out
}

// TestSortedBatchBuildPrecheck runs the five acceptance inputs through the
// generic sorted-batch constructor: out-of-order and duplicate inputs must
// fail with the original input index and leave the FreeList untouched, while
// valid inputs (under both duplicate policies) must build a tree that
// iterates in order.
func TestSortedBatchBuildPrecheck(t *testing.T) {
	cases := []struct {
		name      string
		input     []int
		policy    DuplicatePolicy
		want      []int // expected iteration order; nil if an error is expected
		wantErrAt int    // original input index of the expected error; -1 if none
		wantDup   bool   // expected Duplicate flag of the error
	}{
		{name: "sorted unique", input: []int{1, 2, 3, 4, 5}, policy: RejectDuplicates, want: []int{1, 2, 3, 4, 5}, wantErrAt: -1},
		{name: "out of order", input: []int{1, 2, 4, 3, 5}, policy: RejectDuplicates, wantErrAt: 3},
		{name: "duplicate rejected", input: []int{1, 2, 2, 3}, policy: RejectDuplicates, wantErrAt: 2, wantDup: true},
		{name: "duplicate keep last", input: []int{1, 2, 2, 3}, policy: KeepLastDuplicate, want: []int{1, 2, 3}, wantErrAt: -1},
		{name: "empty", input: []int{}, policy: RejectDuplicates, want: []int{}, wantErrAt: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFreeListG[int](8)
			before := len(f.freelist)
			tr, err := NewWithFreeListFromSortedG(2, Less[int](), f, tc.input, tc.policy)
			if tc.wantErrAt >= 0 {
				if err == nil {
					t.Fatalf("expected error at input index %d, got none", tc.wantErrAt)
				}
				var sbe *SortedBatchError
				if !errors.As(err, &sbe) {
					t.Fatalf("error type = %T, want *SortedBatchError", err)
				}
				if sbe.Index != tc.wantErrAt || sbe.Duplicate != tc.wantDup {
					t.Fatalf("error = {Index:%d Duplicate:%v}, want {Index:%d Duplicate:%v}",
						sbe.Index, sbe.Duplicate, tc.wantErrAt, tc.wantDup)
				}
				if tr != nil {
					t.Fatalf("tree = %v, want nil on precheck failure", tr)
				}
				if got := len(f.freelist); got != before {
					t.Fatalf("freelist length changed on precheck failure: before=%d after=%d", before, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := collectG(tr); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("iteration = %v, want %v", got, tc.want)
			}
			if tr.Len() != len(tc.want) {
				t.Fatalf("Len() = %d, want %d", tr.Len(), len(tc.want))
			}
		})
	}
}

// TestSortedBatchBuildPrecheckNonGeneric runs the same five acceptance inputs
// through the non-generic (Item) constructor, which must share the generic
// semantics.
func TestSortedBatchBuildPrecheckNonGeneric(t *testing.T) {
	toItems := func(in []int) []Item {
		out := make([]Item, len(in))
		for i, v := range in {
			out[i] = Int(v)
		}
		return out
	}
	cases := []struct {
		name      string
		input     []int
		policy    DuplicatePolicy
		want      []int
		wantErrAt int
		wantDup   bool
	}{
		{name: "sorted unique", input: []int{1, 2, 3, 4, 5}, policy: RejectDuplicates, want: []int{1, 2, 3, 4, 5}, wantErrAt: -1},
		{name: "out of order", input: []int{1, 2, 4, 3, 5}, policy: RejectDuplicates, wantErrAt: 3},
		{name: "duplicate rejected", input: []int{1, 2, 2, 3}, policy: RejectDuplicates, wantErrAt: 2, wantDup: true},
		{name: "duplicate keep last", input: []int{1, 2, 2, 3}, policy: KeepLastDuplicate, want: []int{1, 2, 3}, wantErrAt: -1},
		{name: "empty", input: []int{}, policy: RejectDuplicates, want: []int{}, wantErrAt: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFreeList(8)
			before := len(f.freelist)
			tr, err := NewWithFreeListFromSorted(2, f, toItems(tc.input), tc.policy)
			if tc.wantErrAt >= 0 {
				if err == nil {
					t.Fatalf("expected error at input index %d, got none", tc.wantErrAt)
				}
				var sbe *SortedBatchError
				if !errors.As(err, &sbe) {
					t.Fatalf("error type = %T, want *SortedBatchError", err)
				}
				if sbe.Index != tc.wantErrAt || sbe.Duplicate != tc.wantDup {
					t.Fatalf("error = {Index:%d Duplicate:%v}, want {Index:%d Duplicate:%v}",
						sbe.Index, sbe.Duplicate, tc.wantErrAt, tc.wantDup)
				}
				if tr != nil {
					t.Fatalf("tree = %v, want nil on precheck failure", tr)
				}
				if got := len(f.freelist); got != before {
					t.Fatalf("freelist length changed on precheck failure: before=%d after=%d", before, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := []int{}
			tr.Ascend(func(i Item) bool {
				got = append(got, int(i.(Int)))
				return true
			})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("iteration = %v, want %v", got, tc.want)
			}
			if tr.Len() != len(tc.want) {
				t.Fatalf("Len() = %d, want %d", tr.Len(), len(tc.want))
			}
		})
	}
}

// TestSortedBatchKeepLastDuplicate verifies that with KeepLastDuplicate the
// last item of an equal run wins, and that the input batch is not mutated.
func TestSortedBatchKeepLastDuplicate(t *testing.T) {
	type kv struct {
		k, v int
	}
	less := func(a, b kv) bool { return a.k < b.k }
	input := []kv{{1, 1}, {2, 1}, {2, 2}, {2, 3}, {3, 1}}
	orig := append([]kv(nil), input...)
	tr, err := NewFromSortedG(2, less, input, KeepLastDuplicate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(input, orig) {
		t.Fatalf("input batch mutated: %v, want %v", input, orig)
	}
	if tr.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", tr.Len())
	}
	got, ok := tr.Get(kv{k: 2})
	if !ok || got.v != 3 {
		t.Fatalf("Get(2) = %v, %v; want {2 3}, true (last duplicate wins)", got, ok)
	}
	want := []kv{{1, 1}, {2, 3}, {3, 1}}
	if got := collectG(tr); !reflect.DeepEqual(got, want) {
		t.Fatalf("iteration = %v, want %v", got, want)
	}
}

// TestSortedBatchBuildLarge bulk-loads a multi-level tree and checks it
// against a tree built through the existing ReplaceOrInsert interface.
func TestSortedBatchBuildLarge(t *testing.T) {
	const n = 1000
	batch := make([]int, n)
	for i := range batch {
		batch[i] = i
	}
	tr, err := NewOrderedFromSortedG(3, batch, RejectDuplicates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Len() != n {
		t.Fatalf("Len() = %d, want %d", tr.Len(), n)
	}
	if got := collectG(tr); !reflect.DeepEqual(got, batch) {
		t.Fatalf("iteration mismatch: got %d items", len(got))
	}
	for i := 0; i < n; i++ {
		if !tr.Has(i) {
			t.Fatalf("Has(%d) = false", i)
		}
	}
	if mn, _ := tr.Min(); mn != 0 {
		t.Fatalf("Min() = %d, want 0", mn)
	}
	if mx, _ := tr.Max(); mx != n-1 {
		t.Fatalf("Max() = %d, want %d", mx, n-1)
	}
	// Cross-check against the existing one-by-one insertion interface.
	ref := NewOrderedG[int](3)
	for _, v := range batch {
		ref.ReplaceOrInsert(v)
	}
	if got, want := collectG(tr), collectG(ref); !reflect.DeepEqual(got, want) {
		t.Fatalf("bulk-loaded iteration != ReplaceOrInsert iteration")
	}
	// The bulk-loaded tree must remain fully writable.
	for i := 0; i < n; i += 2 {
		if _, ok := tr.Delete(i); !ok {
			t.Fatalf("Delete(%d) failed", i)
		}
	}
	if tr.Len() != n/2 {
		t.Fatalf("Len() after deletes = %d, want %d", tr.Len(), n/2)
	}
}

// TestSortedBatchBothVersionsConsistent checks that the non-generic (Item)
// and generic entry points build trees with identical iteration results.
func TestSortedBatchBothVersionsConsistent(t *testing.T) {
	const n = 200
	items := make([]Item, n)
	ints := make([]int, n)
	for i := 0; i < n; i++ {
		items[i] = Int(i * 2)
		ints[i] = i * 2
	}
	// Non-generic entry, including the FreeList variant.
	fl := NewFreeList(16)
	tr, err := NewWithFreeListFromSorted(2, fl, items, RejectDuplicates)
	if err != nil {
		t.Fatalf("non-generic build: unexpected error: %v", err)
	}
	// Generic entry.
	trg, err := NewOrderedFromSortedG(2, ints, RejectDuplicates)
	if err != nil {
		t.Fatalf("generic build: unexpected error: %v", err)
	}
	gotItems := collectItems(tr)
	gotInts := collectG(trg)
	if len(gotItems) != len(gotInts) {
		t.Fatalf("length mismatch: %d vs %d", len(gotItems), len(gotInts))
	}
	for i := range gotItems {
		if int(gotItems[i].(Int)) != gotInts[i] {
			t.Fatalf("item %d mismatch: %v vs %v", i, gotItems[i], gotInts[i])
		}
	}
	// Both versions must also agree on duplicate handling semantics.
	dups := []Item{Int(1), Int(2), Int(2), Int(3)}
	if _, err := NewFromSorted(2, dups, RejectDuplicates); err == nil {
		t.Fatal("non-generic: expected duplicate error, got none")
	} else {
		var sbe *SortedBatchError
		if !errors.As(err, &sbe) || sbe.Index != 2 || !sbe.Duplicate {
			t.Fatalf("non-generic duplicate error = %v, want index 2 duplicate", err)
		}
	}
	tr2, err := NewFromSorted(2, dups, KeepLastDuplicate)
	if err != nil {
		t.Fatalf("non-generic keep-last: unexpected error: %v", err)
	}
	if got, want := collectItems(tr2), []Item{Int(1), Int(2), Int(3)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("non-generic keep-last iteration = %v, want %v", got, want)
	}
}

// TestSortedBatchReportFixture demonstrates the precheck results with a small
// temporary-folder fixture and logs the report for inspection.
func TestSortedBatchReportFixture(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "sorted_batch_report.txt")
	f := NewFreeListG[int](8)
	var lines []string
	record := func(name string, input []int, policy DuplicatePolicy) {
		before := len(f.freelist)
		tr, err := NewWithFreeListFromSortedG(2, Less[int](), f, input, policy)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%-22s input=%v -> error: %v (freelist %d->%d)",
				name, input, err, before, len(f.freelist)))
			return
		}
		lines = append(lines, fmt.Sprintf("%-22s input=%v -> tree=%v len=%d (freelist %d->%d)",
			name, input, collectG(tr), tr.Len(), before, len(f.freelist)))
	}
	record("sorted unique", []int{1, 2, 3, 4, 5}, RejectDuplicates)
	record("out of order", []int{1, 2, 4, 3, 5}, RejectDuplicates)
	record("duplicate rejected", []int{1, 2, 2, 3}, RejectDuplicates)
	record("duplicate keep last", []int{1, 2, 2, 3}, KeepLastDuplicate)
	record("empty", []int{}, RejectDuplicates)
	content := ""
	for _, ln := range lines {
		content += ln + "\n"
	}
	if err := os.WriteFile(report, []byte(content), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("report fixture written to %s:\n%s", report, content)
}

func ExampleNewOrderedFromSortedG() {
	// Duplicates with KeepLastDuplicate: the last of each equal run is kept.
	tr, err := NewOrderedFromSortedG(2, []int{1, 2, 2, 3}, KeepLastDuplicate)
	if err != nil {
		fmt.Println(err)
		return
	}
	tr.Ascend(func(i int) bool {
		fmt.Println(i)
		return true
	})
	// Out-of-order input fails the precheck with the original input index.
	_, err = NewOrderedFromSortedG(2, []int{1, 3, 2}, RejectDuplicates)
	fmt.Println(err)
	// Duplicates with RejectDuplicates fail the precheck as well.
	_, err = NewOrderedFromSortedG(2, []int{1, 2, 2, 3}, RejectDuplicates)
	fmt.Println(err)
	// Output:
	// 1
	// 2
	// 3
	// btree: sorted batch: out-of-order item at input index 2
	// btree: sorted batch: duplicate item at input index 2
}
