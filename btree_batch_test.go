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

package btree

import (
	"strings"
	"testing"
)

// lessFold is a case-insensitive LessFunc: "A" and "a" are equivalent keys.
func lessFold(a, b string) bool { return strings.ToLower(a) < strings.ToLower(b) }

func collectStrings(t *BTreeG[string]) []string {
	var out []string
	t.Ascend(func(s string) bool { out = append(out, s); return true })
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBatchCaseInsensitiveConflict: with a case-insensitive comparator,
// "A" and "a" are the same key. Inserting "a" when "A" is present must be
// rejected up front (by Less equivalence, not Go ==), and the tree must be
// left untouched.
func TestBatchCaseInsensitiveConflict(t *testing.T) {
	tr := NewG(2, lessFold)
	tr.ReplaceOrInsert("A")
	tr.ReplaceOrInsert("m")

	// Conflict against an existing tree key ("A" vs "a").
	out, err := tr.Batch([]BatchOp[string]{{Kind: BatchInsert, Item: "a"}})
	if err == nil {
		t.Fatal("expected conflict for case-insensitive duplicate insert")
	}
	if out != nil {
		t.Fatal("failed batch must not publish a tree")
	}
	be, ok := err.(*BatchError[string])
	if !ok {
		t.Fatalf("error type = %T, want *BatchError[string]", err)
	}
	if len(be.Conflicts) != 1 || be.Conflicts[0].Index != 0 ||
		be.Conflicts[0].Reason != BatchConflictDuplicateKey {
		t.Fatalf("unexpected conflicts: %+v", be.Conflicts)
	}
	if got := collectStrings(tr); !equalStrings(got, []string{"A", "m"}) {
		t.Fatalf("tree mutated by rejected batch: %v", got)
	}

	// Conflict between two ops of the same batch ("b" then "B").
	_, err = tr.Batch([]BatchOp[string]{
		{Kind: BatchInsert, Item: "b"},
		{Kind: BatchInsert, Item: "B"},
	})
	be, ok = err.(*BatchError[string])
	if !ok || len(be.Conflicts) != 1 || be.Conflicts[0].Index != 1 ||
		be.Conflicts[0].Reason != BatchConflictDuplicateKey {
		t.Fatalf("unexpected conflicts for in-batch duplicate: %+v", err)
	}
	if got := collectStrings(tr); !equalStrings(got, []string{"A", "m"}) {
		t.Fatalf("tree mutated by rejected batch: %v", got)
	}
}

// TestBatchFourOps applies a legal four-operation batch (insert, replace,
// delete, insert) in declared order and checks that the new tree reflects
// all of them while the old tree and a pre-batch clone snapshot stay
// exactly as they were.
func TestBatchFourOps(t *testing.T) {
	tr := NewG(2, lessFold)
	for _, s := range []string{"alpha", "beta", "gamma"} {
		tr.ReplaceOrInsert(s)
	}
	snapshot := tr.Clone() // snapshot taken before the batch

	ops := []BatchOp[string]{
		{Kind: BatchInsert, Item: "delta"},   // 0: add new key
		{Kind: BatchReplace, Item: "ALPHA"},  // 1: re-store existing key with new casing
		{Kind: BatchDelete, Item: "Beta"},    // 2: remove existing key (case-insensitive)
		{Kind: BatchInsert, Item: "epsilon"}, // 3: add another new key
	}
	out, err := tr.Batch(ops)
	if err != nil {
		t.Fatalf("legal batch rejected: %v", err)
	}
	want := []string{"ALPHA", "delta", "epsilon", "gamma"}
	if got := collectStrings(out); !equalStrings(got, want) {
		t.Fatalf("new tree = %v, want %v", got, want)
	}
	if out.Len() != 4 {
		t.Fatalf("new tree len = %d, want 4", out.Len())
	}
	// Replace must have stored the new value, not kept the old one.
	if v, ok := out.Get("alpha"); !ok || v != "ALPHA" {
		t.Fatalf("replaced value = %q, %v; want %q, true", v, ok, "ALPHA")
	}
	// Old tree unchanged.
	old := []string{"alpha", "beta", "gamma"}
	if got := collectStrings(tr); !equalStrings(got, old) {
		t.Fatalf("original tree mutated: %v, want %v", got, old)
	}
	// Pre-batch clone snapshot unchanged.
	if got := collectStrings(snapshot); !equalStrings(got, old) {
		t.Fatalf("snapshot mutated: %v, want %v", got, old)
	}
	// Further writes to the published tree must not leak into the snapshot:
	// the snapshot must not share nodes that later become mutable.
	out.ReplaceOrInsert("zeta")
	out.Delete("gamma")
	if got := collectStrings(snapshot); !equalStrings(got, old) {
		t.Fatalf("snapshot shares mutable nodes with new tree: %v, want %v", got, old)
	}
	if got := collectStrings(tr); !equalStrings(got, old) {
		t.Fatalf("original tree mutated by post-batch writes: %v, want %v", got, old)
	}
}

// TestBatchNoPartialCommit: when the second operation of a batch fails,
// nothing is committed; no new tree is published and the old tree and its
// snapshots are exactly the same as before.
func TestBatchNoPartialCommit(t *testing.T) {
	tr := NewG(2, lessFold)
	for _, s := range []string{"a", "b", "c"} {
		tr.ReplaceOrInsert(s)
	}
	snapshot := tr.Clone()

	out, err := tr.Batch([]BatchOp[string]{
		{Kind: BatchInsert, Item: "d"},       // 0: valid on its own
		{Kind: BatchDelete, Item: "missing"}, // 1: fails; key absent
		{Kind: BatchInsert, Item: "e"},       // 2: valid on its own
	})
	if err == nil {
		t.Fatal("expected batch to be rejected")
	}
	if out != nil {
		t.Fatal("no tree may be published on failure")
	}
	be := err.(*BatchError[string])
	if len(be.Conflicts) != 1 || be.Conflicts[0].Index != 1 ||
		be.Conflicts[0].Reason != BatchConflictMissingKey {
		t.Fatalf("conflicts = %+v, want single missing-key conflict at index 1", be.Conflicts)
	}
	want := []string{"a", "b", "c"}
	if got := collectStrings(tr); !equalStrings(got, want) {
		t.Fatalf("partial commit leaked into old tree: %v, want %v", got, want)
	}
	if got := collectStrings(snapshot); !equalStrings(got, want) {
		t.Fatalf("partial commit leaked into snapshot: %v, want %v", got, want)
	}
	if tr.Len() != 3 {
		t.Fatalf("old tree len = %d, want 3", tr.Len())
	}
}

// TestBatchConflictIndices: every conflict keeps its original operation
// index, even when several operations conflict in one batch.
func TestBatchConflictIndices(t *testing.T) {
	tr := NewG(2, lessFold)
	tr.ReplaceOrInsert("x")

	_, err := tr.Batch([]BatchOp[string]{
		{Kind: BatchInsert, Item: "y"},       // 0: ok
		{Kind: BatchDelete, Item: "nope"},    // 1: missing
		{Kind: BatchInsert, Item: "X"},       // 2: duplicate of existing "x"
		{Kind: BatchReplace, Item: "absent"}, // 3: missing
		{Kind: BatchDelete, Item: "y"},       // 4: ok (deletes op 0 insert)
	})
	be, ok := err.(*BatchError[string])
	if !ok {
		t.Fatalf("error type = %T, want *BatchError[string]", err)
	}
	if len(be.Conflicts) != 3 {
		t.Fatalf("got %d conflicts, want 3: %+v", len(be.Conflicts), be.Conflicts)
	}
	wantIdx := []int{1, 2, 3}
	wantReason := []BatchConflictReason{
		BatchConflictMissingKey, BatchConflictDuplicateKey, BatchConflictMissingKey,
	}
	for i, c := range be.Conflicts {
		if c.Index != wantIdx[i] || c.Reason != wantReason[i] {
			t.Fatalf("conflict %d = (idx %d, reason %d), want (idx %d, reason %d)",
				i, c.Index, c.Reason, wantIdx[i], wantReason[i])
		}
	}
	if got := collectStrings(tr); !equalStrings(got, []string{"x"}) {
		t.Fatalf("tree mutated by rejected batch: %v", got)
	}
}

// TestBatchDeclaredOrder: operations observe the effects of earlier
// operations in the same batch (insert then delete then re-insert of the
// same key is legal and nets out to a single insert).
func TestBatchDeclaredOrder(t *testing.T) {
	tr := NewOrderedG[string](2)
	tr.ReplaceOrInsert("keep")

	out, err := tr.Batch([]BatchOp[string]{
		{Kind: BatchInsert, Item: "tmp"},
		{Kind: BatchDelete, Item: "tmp"},
		{Kind: BatchInsert, Item: "tmp"},
		{Kind: BatchDelete, Item: "keep"},
		{Kind: BatchInsert, Item: "keep"},
	})
	if err != nil {
		t.Fatalf("declared-order batch rejected: %v", err)
	}
	want := []string{"keep", "tmp"}
	if got := collectStrings(out); !equalStrings(got, want) {
		t.Fatalf("new tree = %v, want %v", got, want)
	}
	if got := collectStrings(tr); !equalStrings(got, []string{"keep"}) {
		t.Fatalf("old tree mutated: %v", got)
	}
}

// kv is a reference type used to pin down value-ownership semantics.
type kv struct {
	key string
	val int
}

func lessKV(a, b *kv) bool { return a.key < b.key }

// TestBatchValueOwnership: reference values are stored as-is. The tree
// keeps the exact pointer supplied by the caller; it neither deep-copies
// on the way in nor on Clone/publish, so the caller's object remains the
// caller's responsibility (and later caller-side mutation is visible
// through the tree; the API never claims otherwise).
func TestBatchValueOwnership(t *testing.T) {
	tr := NewG(2, lessKV)
	orig := &kv{key: "k", val: 1}
	tr.ReplaceOrInsert(orig)

	repl := &kv{key: "k", val: 2}
	add := &kv{key: "j", val: 3}
	out, err := tr.Batch([]BatchOp[*kv]{
		{Kind: BatchReplace, Item: repl},
		{Kind: BatchInsert, Item: add},
	})
	if err != nil {
		t.Fatalf("batch rejected: %v", err)
	}
	got, ok := out.Get(&kv{key: "k"})
	if !ok || got != repl {
		t.Fatalf("stored value = %p (%v), want the exact pointer %p", got, ok, repl)
	}
	if got, ok := out.Get(&kv{key: "j"}); !ok || got != add {
		t.Fatalf("inserted value = %p (%v), want the exact pointer %p", got, ok, add)
	}
	// The old tree still holds the original pointer: no copy, no swap.
	if got, ok := tr.Get(&kv{key: "k"}); !ok || got != orig {
		t.Fatalf("old tree value = %p (%v), want the exact pointer %p", got, ok, orig)
	}
	// Shallow by contract: mutating the caller's object is visible via the
	// tree (the tree never claimed a deep copy).
	repl.val = 42
	if got, _ := out.Get(&kv{key: "k"}); got.val != 42 {
		t.Fatalf("reference semantics broken: got.val = %d, want 42", got.val)
	}
}

// TestBatchPlanApplySeparation: an explicit plan can be validated first and
// published later; a plan is bound to the tree that produced it.
func TestBatchPlanApplySeparation(t *testing.T) {
	tr := NewOrderedG[int](2)
	tr.ReplaceOrInsert(1)
	other := NewOrderedG[int](2)

	plan, err := tr.PlanBatch([]BatchOp[int]{
		{Kind: BatchInsert, Item: 2},
		{Kind: BatchDelete, Item: 1},
	})
	if err != nil {
		t.Fatalf("plan rejected: %v", err)
	}
	if got := collectInts(tr); !equalInts(got, []int{1}) {
		t.Fatalf("PlanBatch mutated the tree: %v", got)
	}
	out := tr.ApplyBatch(plan)
	if got := collectInts(out); !equalInts(got, []int{2}) {
		t.Fatalf("published tree = %v, want [2]", got)
	}
	if got := collectInts(tr); !equalInts(got, []int{1}) {
		t.Fatalf("ApplyBatch mutated the source tree: %v", got)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("ApplyBatch with a foreign plan must panic")
		}
	}()
	other.ApplyBatch(plan)
}

func collectInts(t *BTreeG[int]) []int {
	var out []int
	t.Ascend(func(i int) bool { out = append(out, i); return true })
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBatchNonGenericWrapper exercises the BTree (Item interface) wrapper.
func TestBatchNonGenericWrapper(t *testing.T) {
	tr := New(2)
	tr.ReplaceOrInsert(Int(1))
	tr.ReplaceOrInsert(Int(2))

	out, err := tr.Batch([]BatchOp[Item]{
		{Kind: BatchDelete, Item: Int(1)},
		{Kind: BatchInsert, Item: Int(3)},
	})
	if err != nil {
		t.Fatalf("batch rejected: %v", err)
	}
	if out.Len() != 2 || !out.Has(Int(2)) || !out.Has(Int(3)) || out.Has(Int(1)) {
		t.Fatalf("published tree wrong: len=%d", out.Len())
	}
	if tr.Len() != 2 || !tr.Has(Int(1)) {
		t.Fatal("source tree mutated")
	}
	if _, err := tr.Batch([]BatchOp[Item]{{Kind: BatchDelete, Item: Int(99)}}); err == nil {
		t.Fatal("expected missing-key conflict")
	}
}
