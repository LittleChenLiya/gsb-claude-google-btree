# BTree implementation for Go

This package provides an in-memory B-Tree implementation for Go, useful as
an ordered, mutable data structure.

The API is based off of the wonderful
http://godoc.org/github.com/petar/GoLLRB/llrb, and is meant to allow btree to
act as a drop-in replacement for gollrb trees.

See http://godoc.org/github.com/google/btree for documentation.

## Bounded range cursors

`BTreeG` (and the non-generic `BTree`) support explicit, bounded, paged range
cursors via `Cursor` and `Range`. A cursor is bound to the tree's current
snapshot and comparator identity, iterates a range with independently
open/closed bounds, and returns pages of at most a caller-supplied limit. Each
page starts at the strict successor of the last key of the previous page, so
pages never overlap and no key is returned twice; advancing to the next page
seeks directly to its first key and does not scan previous pages. Keys are
positioned using the tree's own comparator, so equality follows the tree's true
semantics. Mutating the tree after the cursor was created invalidates the
cursor (`ErrCursorInvalidated`); to iterate a stable snapshot across writes,
`Clone` the tree first and create the cursor on the clone.

## Testing

Run the deterministic test suite from the repository root with:

    go test ./...

To run just the bounded range cursor tests and their two range examples:

    go test -run 'Cursor|ExampleBTreeG_Cursor' -v ./...
