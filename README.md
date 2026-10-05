# BTree implementation for Go

This package provides an in-memory B-Tree implementation for Go, useful as
an ordered, mutable data structure.

The API is based off of the wonderful
http://godoc.org/github.com/petar/GoLLRB/llrb, and is meant to allow btree to
act as a drop-in replacement for gollrb trees.

See http://godoc.org/github.com/google/btree for documentation.

## Bounded range cursors (paging)

`NewRangeCursorG` (generic) / `NewRangeCursor` (Item-based) create an explicit
bounded page cursor over an immutable snapshot of a tree:

- the cursor binds the exact tree and the exact comparator instance it was
  created with; paging through another tree or constructing it with another
  comparator returns `ErrWrongTree` / `ErrWrongComparator`;
- `Range` endpoints are handled independently as open or closed
  (`OpenBound` / `ClosedBound`), supporting `[lo,hi]`, `[lo,hi)`, `(lo,hi]`,
  `(lo,hi)` and unbounded sides;
- every page after the first starts at the strict successor (ascending) or
  strict predecessor (descending) of the previous page's last key, so pages
  never repeat or skip keys and never rescan earlier pages (one tree seek per
  page);
- keys are ordered solely by the tree's real `LessFunc` (or `Item.Less`),
  never by reflection or string comparison;
- pages always come from the captured snapshot: mutating the live tree leaves
  an existing cursor on its original snapshot, while a fresh cursor sees the
  new data;
- a terminal empty page is returned as `nil` with `more == false`; an exhausted
  cursor cannot be rewound or claimed reusable — create a new cursor instead.
  The original `Ascend*` / `Descend*` methods are unchanged.

Ascending page over `[2, 5)`, two keys per page:

```go
less := btree.Less[int]()
tr := btree.NewG[int](4, less)
for _, k := range []int{1, 2, 3, 4, 5} {
    tr.ReplaceOrInsert(k)
}
cur, _ := btree.NewRangeCursorG(tr, less, btree.Range[int]{
    Lower: btree.ClosedBound(2),
    Upper: btree.OpenBound(5),
}, btree.CursorAscend)
defer cur.Close()
for {
    page, more, err := cur.Next(2) // [2 3], then [4], then (nil, false, nil)
    if err != nil || !more {
        break
    }
    _ = page
}
```

Descending page over `(1, 5]`, two keys per page:

```go
cur, _ := tr.RangeCursor(btree.Range[int]{
    Lower: btree.OpenBound(1),
    Upper: btree.ClosedBound(5),
}, btree.CursorDescend)
defer cur.Close()
page, more, err := cur.Next(2) // [5 4], more=true; next page [3 2], more=false
```

## Running the tests

```sh
go test ./...
```

The range-cursor acceptance tests and runnable range examples live in
`cursor_generic_test.go` (generic) and `cursor_test.go` (pre-generic build);
run just those with:

```sh
go test -run 'RangeCursor|ExampleBTreeG_rangeCursor' ./...
go test -v -run ExampleBTreeG_rangeCursor ./...
```

