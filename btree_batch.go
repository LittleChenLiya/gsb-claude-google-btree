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

import "fmt"

// BatchOpKind identifies the kind of a single mutation inside a batch plan.
type BatchOpKind int

const (
	// BatchInsert inserts op.Item. The key must NOT be present (by Less
	// equivalence) in the virtual state produced by the tree plus all
	// preceding operations of the same batch.
	BatchInsert BatchOpKind = iota
	// BatchReplace replaces the stored item that is Less-equivalent to
	// op.Item. The key MUST be present in the virtual state.
	BatchReplace
	// BatchDelete removes the item that is Less-equivalent to op.Item.
	// The key MUST be present in the virtual state.
	BatchDelete
)

// BatchOp is a single declared mutation. Operations inside one batch are
// validated and applied strictly in their declared (slice) order.
type BatchOp[T any] struct {
	Kind BatchOpKind
	Item T
}

// BatchConflictReason explains why a single operation conflicts.
type BatchConflictReason int

const (
	// BatchConflictDuplicateKey: a BatchInsert target is already present
	// (either in the tree or made present by an earlier op of the batch).
	BatchConflictDuplicateKey BatchConflictReason = iota
	// BatchConflictMissingKey: a BatchReplace/BatchDelete target is absent
	// from the virtual state.
	BatchConflictMissingKey
	// BatchConflictInvalidKind: the operation kind is not one of the
	// declared BatchOpKind constants.
	BatchConflictInvalidKind
)

// BatchConflict describes one rejected operation. Index is always the
// operation's original position in the declared batch, so callers can map
// every conflict back to the exact input operation even when several
// operations conflict.
type BatchConflict[T any] struct {
	Index  int
	Op     BatchOp[T]
	Reason BatchConflictReason
}

// BatchError is returned by PlanBatch/Batch when one or more operations
// conflict. It carries every conflict found during validation, each with
// its original operation index.
type BatchError[T any] struct {
	Conflicts []BatchConflict[T]
}

func (e *BatchError[T]) Error() string {
	return fmt.Sprintf("btree: batch plan rejected: %d conflict(s), first at op index %d (reason %d)",
		len(e.Conflicts), e.Conflicts[0].Index, e.Conflicts[0].Reason)
}

// BatchPlan is an explicit, validated mutation plan. It is produced by
// PlanBatch against a specific tree state and may only be applied to that
// same tree (ApplyBatch panics otherwise). A plan is immutable once created.
type BatchPlan[T any] struct {
	src *BTreeG[T]
	ops []BatchOp[T]
}

// Ops returns a copy of the validated operations, in declared order.
func (p *BatchPlan[T]) Ops() []BatchOp[T] {
	out := make([]BatchOp[T], len(p.ops))
	copy(out, p.ops)
	return out
}

// PlanBatch validates ops against the current state of t and returns an
// executable plan. Key equivalence is decided ONLY by the tree's LessFunc
// (a and b are equivalent when !less(a,b) && !less(b,a)); Go's == is never
// used to guess business equivalence.
//
// Validation simulates the operations in declared order over a virtual
// state (tree contents plus the effects of preceding valid ops):
//   - BatchInsert conflicts if its key is already present.
//   - BatchReplace/BatchDelete conflict if their key is absent.
//   - An unknown BatchOpKind conflicts as BatchConflictInvalidKind.
//
// Conflicting operations do not modify the virtual state used to validate
// subsequent operations. PlanBatch never mutates t.
//
// Value ownership: items are stored exactly as supplied. If T is a pointer
// or other reference type, the tree stores the reference itself and does
// NOT deep-copy the referenced value; the caller keeps ownership of the
// referenced memory and must not mutate stored items in ways that change
// their Less ordering.
func (t *BTreeG[T]) PlanBatch(ops []BatchOp[T]) (*BatchPlan[T], error) {
	// Overlay trees tracking the virtual presence state. They use the
	// tree's own LessFunc, so equivalence matches the tree exactly.
	added := NewWithFreeListG(t.degree, t.cow.less, NewFreeListG[T](0))
	removed := NewWithFreeListG(t.degree, t.cow.less, NewFreeListG[T](0))
	present := func(item T) bool {
		if removed.Has(item) {
			return false
		}
		if added.Has(item) {
			return true
		}
		return t.Has(item)
	}
	var conflicts []BatchConflict[T]
	for i, op := range ops {
		switch op.Kind {
		case BatchInsert:
			if present(op.Item) {
				conflicts = append(conflicts, BatchConflict[T]{Index: i, Op: op, Reason: BatchConflictDuplicateKey})
				continue
			}
			removed.Delete(op.Item)
			added.ReplaceOrInsert(op.Item)
		case BatchReplace:
			if !present(op.Item) {
				conflicts = append(conflicts, BatchConflict[T]{Index: i, Op: op, Reason: BatchConflictMissingKey})
				continue
			}
			// Presence unchanged; nothing to record in the overlay.
		case BatchDelete:
			if !present(op.Item) {
				conflicts = append(conflicts, BatchConflict[T]{Index: i, Op: op, Reason: BatchConflictMissingKey})
				continue
			}
			if added.Has(op.Item) {
				added.Delete(op.Item)
			} else {
				removed.ReplaceOrInsert(op.Item)
			}
		default:
			conflicts = append(conflicts, BatchConflict[T]{Index: i, Op: op, Reason: BatchConflictInvalidKind})
		}
	}
	if len(conflicts) > 0 {
		return nil, &BatchError[T]{Conflicts: conflicts}
	}
	copied := make([]BatchOp[T], len(ops))
	copy(copied, ops)
	return &BatchPlan[T]{src: t, ops: copied}, nil
}

// ApplyBatch publishes a new tree with every planned operation applied in
// declared order. The receiver (and any clone snapshot taken from it,
// before or after) is left completely untouched: the plan is executed on a
// lazy Clone, and copy-on-write guarantees the new tree never mutates nodes
// shared with older snapshots.
//
// ApplyBatch panics if the plan was created for a different tree. Since the
// plan was fully validated by PlanBatch, ApplyBatch itself cannot fail.
//
// Value ownership: the new tree stores the exact Item values carried by the
// plan (and the pre-existing values shared with the receiver). Reference
// values are stored as references; no deep copy is performed or implied.
func (t *BTreeG[T]) ApplyBatch(plan *BatchPlan[T]) *BTreeG[T] {
	if plan == nil || plan.src != t {
		panic("btree: ApplyBatch called with a plan created for a different tree")
	}
	out := t.Clone()
	for _, op := range plan.ops {
		switch op.Kind {
		case BatchInsert, BatchReplace:
			out.ReplaceOrInsert(op.Item)
		case BatchDelete:
			out.Delete(op.Item)
		}
	}
	return out
}

// Batch is the convenience composition of PlanBatch and ApplyBatch: it
// validates ops against t and, only if every operation is valid, publishes
// a new tree with all operations applied in declared order.
//
// On any conflict it returns (nil, *BatchError[T]) and t is guaranteed to
// be unmodified �� a failed batch never partially commits. On success the
// returned tree shares structure with t via copy-on-write, so t and any
// snapshot cloned from it remain valid and unchanged.
func (t *BTreeG[T]) Batch(ops []BatchOp[T]) (*BTreeG[T], error) {
	plan, err := t.PlanBatch(ops)
	if err != nil {
		return nil, err
	}
	return t.ApplyBatch(plan), nil
}

// BatchItem is the non-generic counterpart of BTreeG.Batch.
func (t *BTree) Batch(ops []BatchOp[Item]) (*BTree, error) {
	out, err := (*BTreeG[Item])(t).Batch(ops)
	if err != nil {
		return nil, err
	}
	return (*BTree)(out), nil
}

// PlanBatch is the non-generic counterpart of BTreeG.PlanBatch.
func (t *BTree) PlanBatch(ops []BatchOp[Item]) (*BatchPlan[Item], error) {
	return (*BTreeG[Item])(t).PlanBatch(ops)
}

// ApplyBatch is the non-generic counterpart of BTreeG.ApplyBatch.
func (t *BTree) ApplyBatch(plan *BatchPlan[Item]) *BTree {
	return (*BTree)((*BTreeG[Item])(t).ApplyBatch(plan))
}
