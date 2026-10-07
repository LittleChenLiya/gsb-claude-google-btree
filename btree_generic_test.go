// Copyright 2014-2022 Google Inc.
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
	"sort"
	"sync"
	"testing"
)

func intRange(s int, reverse bool) []int {
	out := make([]int, s)
	for i := 0; i < s; i++ {
		v := i
		if reverse {
			v = s - i - 1
		}
		out[i] = v
	}
	return out
}

func intAll(t *BTreeG[int]) (out []int) {
	t.Ascend(func(a int) bool {
		out = append(out, a)
		return true
	})
	return
}

func intAllRev(t *BTreeG[int]) (out []int) {
	t.Descend(func(a int) bool {
		out = append(out, a)
		return true
	})
	return
}

func TestBTreeG(t *testing.T) {
	tr := NewOrderedG[int](*btreeDegree)
	const treeSize = 10000
	for i := 0; i < 10; i++ {
		if min, ok := tr.Min(); ok || min != 0 {
			t.Fatalf("empty min, got %+v", min)
		}
		if max, ok := tr.Max(); ok || max != 0 {
			t.Fatalf("empty max, got %+v", max)
		}
		for _, item := range rand.Perm(treeSize) {
			if x, ok := tr.ReplaceOrInsert(item); ok || x != 0 {
				t.Fatal("insert found item", item)
			}
		}
		for _, item := range rand.Perm(treeSize) {
			if x, ok := tr.ReplaceOrInsert(item); !ok || x != item {
				t.Fatal("insert didn't find item", item)
			}
		}
		want := 0
		if min, ok := tr.Min(); !ok || min != want {
			t.Fatalf("min: ok %v want %+v, got %+v", ok, want, min)
		}
		want = treeSize - 1
		if max, ok := tr.Max(); !ok || max != want {
			t.Fatalf("max: ok %v want %+v, got %+v", ok, want, max)
		}
		got := intAll(tr)
		wantRange := intRange(treeSize, false)
		if !reflect.DeepEqual(got, wantRange) {
			t.Fatalf("mismatch:\n got: %v\nwant: %v", got, wantRange)
		}

		gotrev := intAllRev(tr)
		wantrev := intRange(treeSize, true)
		if !reflect.DeepEqual(gotrev, wantrev) {
			t.Fatalf("mismatch:\n got: %v\nwant: %v", gotrev, wantrev)
		}

		for _, item := range rand.Perm(treeSize) {
			if x, ok := tr.Delete(item); !ok || x != item {
				t.Fatalf("didn't find %v", item)
			}
		}
		if got = intAll(tr); len(got) > 0 {
			t.Fatalf("some left!: %v", got)
		}
		if got = intAllRev(tr); len(got) > 0 {
			t.Fatalf("some left!: %v", got)
		}
	}
}

func ExampleBTreeG() {
	tr := NewOrderedG[int](*btreeDegree)
	for i := 0; i < 10; i++ {
		tr.ReplaceOrInsert(i)
	}
	fmt.Println("len:       ", tr.Len())
	v, ok := tr.Get(3)
	fmt.Println("get3:      ", v, ok)
	v, ok = tr.Get(100)
	fmt.Println("get100:    ", v, ok)
	v, ok = tr.Delete(4)
	fmt.Println("del4:      ", v, ok)
	v, ok = tr.Delete(100)
	fmt.Println("del100:    ", v, ok)
	v, ok = tr.ReplaceOrInsert(5)
	fmt.Println("replace5:  ", v, ok)
	v, ok = tr.ReplaceOrInsert(100)
	fmt.Println("replace100:", v, ok)
	v, ok = tr.Min()
	fmt.Println("min:       ", v, ok)
	v, ok = tr.DeleteMin()
	fmt.Println("delmin:    ", v, ok)
	v, ok = tr.Max()
	fmt.Println("max:       ", v, ok)
	v, ok = tr.DeleteMax()
	fmt.Println("delmax:    ", v, ok)
	fmt.Println("len:       ", tr.Len())
	// Output:
	// len:        10
	// get3:       3 true
	// get100:     0 false
	// del4:       4 true
	// del100:     0 false
	// replace5:   5 true
	// replace100: 0 false
	// min:        0 true
	// delmin:     0 true
	// max:        100 true
	// delmax:     100 true
	// len:        8
}

func TestDeleteMinG(t *testing.T) {
	tr := NewOrderedG[int](3)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	for v, ok := tr.DeleteMin(); ok; v, ok = tr.DeleteMin() {
		got = append(got, v)
	}
	if want := intRange(100, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
}

func TestDeleteMaxG(t *testing.T) {
	tr := NewOrderedG[int](3)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	for v, ok := tr.DeleteMax(); ok; v, ok = tr.DeleteMax() {
		got = append(got, v)
	}
	if want := intRange(100, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
}

func TestAscendRangeG(t *testing.T) {
	tr := NewOrderedG[int](2)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	tr.AscendRange(40, 60, func(a int) bool {
		got = append(got, a)
		return true
	})
	if want := intRange(100, false)[40:60]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
	got = got[:0]
	tr.AscendRange(40, 60, func(a int) bool {
		if a > 50 {
			return false
		}
		got = append(got, a)
		return true
	})
	if want := intRange(100, false)[40:51]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
}

func TestDescendRangeG(t *testing.T) {
	tr := NewOrderedG[int](2)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	tr.DescendRange(60, 40, func(a int) bool {
		got = append(got, a)
		return true
	})
	if want := intRange(100, true)[39:59]; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendrange:\n got: %v\nwant: %v", got, want)
	}
	got = got[:0]
	tr.DescendRange(60, 40, func(a int) bool {
		if a < 50 {
			return false
		}
		got = append(got, a)
		return true
	})
	if want := intRange(100, true)[39:50]; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendrange:\n got: %v\nwant: %v", got, want)
	}
}

func TestAscendLessThanG(t *testing.T) {
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	tr.AscendLessThan(60, func(a int) bool {
		got = append(got, a)
		return true
	})
	if want := intRange(100, false)[:60]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
	got = got[:0]
	tr.AscendLessThan(60, func(a int) bool {
		if a > 50 {
			return false
		}
		got = append(got, a)
		return true
	})
	if want := intRange(100, false)[:51]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
}

func TestDescendLessOrEqualG(t *testing.T) {
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	tr.DescendLessOrEqual(40, func(a int) bool {
		got = append(got, a)
		return true
	})
	if want := intRange(100, true)[59:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendlessorequal:\n got: %v\nwant: %v", got, want)
	}
	got = got[:0]
	tr.DescendLessOrEqual(60, func(a int) bool {
		if a < 50 {
			return false
		}
		got = append(got, a)
		return true
	})
	if want := intRange(100, true)[39:50]; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendlessorequal:\n got: %v\nwant: %v", got, want)
	}
}

func TestAscendGreaterOrEqualG(t *testing.T) {
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	tr.AscendGreaterOrEqual(40, func(a int) bool {
		got = append(got, a)
		return true
	})
	if want := intRange(100, false)[40:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
	got = got[:0]
	tr.AscendGreaterOrEqual(40, func(a int) bool {
		if a > 50 {
			return false
		}
		got = append(got, a)
		return true
	})
	if want := intRange(100, false)[40:51]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascendrange:\n got: %v\nwant: %v", got, want)
	}
}

func TestDescendGreaterThanG(t *testing.T) {
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	var got []int
	tr.DescendGreaterThan(40, func(a int) bool {
		got = append(got, a)
		return true
	})
	if want := intRange(100, true)[:59]; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendgreaterthan:\n got: %v\nwant: %v", got, want)
	}
	got = got[:0]
	tr.DescendGreaterThan(40, func(a int) bool {
		if a < 50 {
			return false
		}
		got = append(got, a)
		return true
	})
	if want := intRange(100, true)[:50]; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendgreaterthan:\n got: %v\nwant: %v", got, want)
	}
}

// TestClearWithCallbackCloneG clones a tree twice, mutates all three trees,
// then clears one clone with a callback that fails on the second item.  It
// verifies the notified count, that the cleared tree is emptied, and that
// the other trees keep their full, ordered contents.
func TestClearWithCallbackCloneG(t *testing.T) {
	const treeSize = 100
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(treeSize) {
		tr.ReplaceOrInsert(v)
	}
	c1 := tr.Clone()
	c2 := tr.Clone()
	// Each of the three trees diverges from the shared structure.
	tr.ReplaceOrInsert(-1)   // only visible in tr
	c1.Delete(42)            // 42 no longer visible in c1
	c2.ReplaceOrInsert(1000) // only visible in c2

	// Clear c1; the callback fails on the second notified item.
	var got []int
	notified := c1.ClearWithCallback(true, func(a int) bool {
		got = append(got, a)
		return len(got) < 2
	})
	if want := 2; notified != want {
		t.Fatalf("notified: got %d, want %d", notified, want)
	}
	// c1's visible items are 0..99 minus 42, so the first two are 0 and 1.
	if want := []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("notified items:\n got: %v\nwant: %v", got, want)
	}
	if c1.Len() != 0 {
		t.Fatalf("cleared tree length: got %d, want 0", c1.Len())
	}
	if items := intAll(c1); len(items) != 0 {
		t.Fatalf("cleared tree not empty: %v", items)
	}
	// The other two trees must be intact and in order.
	wantTr := append([]int{-1}, intRange(treeSize, false)...)
	if gotTr := intAll(tr); !reflect.DeepEqual(gotTr, wantTr) {
		t.Fatalf("original tree corrupted:\n got: %v\nwant: %v", gotTr, wantTr)
	}
	wantC2 := append(intRange(treeSize, false), 1000)
	if gotC2 := intAll(c2); !reflect.DeepEqual(gotC2, wantC2) {
		t.Fatalf("clone corrupted:\n got: %v\nwant: %v", gotC2, wantC2)
	}
}

// TestClearWithCallbackFullG verifies that a fully-successful callback is
// notified of exactly the items visible in the cleared tree, in ascending
// order, and that shared clone nodes are not disturbed.
func TestClearWithCallbackFullG(t *testing.T) {
	const treeSize = 100
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(treeSize) {
		tr.ReplaceOrInsert(v)
	}
	clone := tr.Clone()
	// Diverge: 7 and 8 are only visible in tr, -1 only in clone.
	clone.Delete(7)
	clone.Delete(8)
	clone.ReplaceOrInsert(-1)

	var got []int
	notified := clone.ClearWithCallback(true, func(a int) bool {
		got = append(got, a)
		return true
	})
	want := append([]int{-1}, intRange(treeSize, false)[0:7]...)
	want = append(want, intRange(treeSize, false)[9:]...)
	if wantNotified := treeSize - 1; notified != wantNotified {
		t.Fatalf("notified: got %d, want %d", notified, wantNotified)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notified items:\n got: %v\nwant: %v", got, want)
	}
	if clone.Len() != 0 {
		t.Fatalf("cleared tree length: got %d, want 0", clone.Len())
	}
	// The original tree is untouched.
	if gotTr := intAll(tr); !reflect.DeepEqual(gotTr, intRange(treeSize, false)) {
		t.Fatalf("original tree corrupted:\n got: %v", gotTr)
	}
}

// TestClearWithCallbackNilG verifies that a nil callback makes
// ClearWithCallback behave like Clear and report zero notifications.
func TestClearWithCallbackNilG(t *testing.T) {
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range rand.Perm(100) {
		tr.ReplaceOrInsert(v)
	}
	clone := tr.Clone()
	if notified := clone.ClearWithCallback(true, nil); notified != 0 {
		t.Fatalf("notified: got %d, want 0", notified)
	}
	if clone.Len() != 0 {
		t.Fatalf("cleared tree length: got %d, want 0", clone.Len())
	}
	if got := intAll(tr); !reflect.DeepEqual(got, intRange(100, false)) {
		t.Fatalf("original tree corrupted:\n got: %v", got)
	}
}

func ExampleBTreeG_ClearWithCallback() {
	tr := NewOrderedG[int](2)
	for i := 0; i < 5; i++ {
		tr.ReplaceOrInsert(i)
	}
	clone := tr.Clone()
	clone.Delete(2) // diverge: 2 is no longer visible in the clone
	notified := clone.ClearWithCallback(true, func(a int) bool {
		fmt.Println("notify:", a)
		return a != 1 // fail on the second notified item
	})
	fmt.Println("notified: ", notified)
	fmt.Println("clone len:", clone.Len())
	fmt.Println("orig:     ", intAll(tr))
	// Output:
	// notify: 0
	// notify: 1
	// notified:  2
	// clone len: 0
	// orig:      [0 1 2 3 4]
}

func BenchmarkInsertG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	b.StartTimer()
	i := 0
	for i < b.N {
		tr := NewOrderedG[int](*btreeDegree)
		for _, item := range insertP {
			tr.ReplaceOrInsert(item)
			i++
			if i >= b.N {
				return
			}
		}
	}
}

func BenchmarkSeekG(b *testing.B) {
	b.StopTimer()
	size := 100000
	insertP := rand.Perm(size)
	tr := NewOrderedG[int](*btreeDegree)
	for _, item := range insertP {
		tr.ReplaceOrInsert(item)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		tr.AscendGreaterOrEqual(i%size, func(i int) bool { return false })
	}
}

func BenchmarkDeleteInsertG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, item := range insertP {
		tr.ReplaceOrInsert(item)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		tr.Delete(insertP[i%benchmarkTreeSize])
		tr.ReplaceOrInsert(insertP[i%benchmarkTreeSize])
	}
}

func BenchmarkDeleteInsertCloneOnceG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, item := range insertP {
		tr.ReplaceOrInsert(item)
	}
	tr = tr.Clone()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		tr.Delete(insertP[i%benchmarkTreeSize])
		tr.ReplaceOrInsert(insertP[i%benchmarkTreeSize])
	}
}

func BenchmarkDeleteInsertCloneEachTimeG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, item := range insertP {
		tr.ReplaceOrInsert(item)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		tr = tr.Clone()
		tr.Delete(insertP[i%benchmarkTreeSize])
		tr.ReplaceOrInsert(insertP[i%benchmarkTreeSize])
	}
}

func BenchmarkDeleteG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	removeP := rand.Perm(benchmarkTreeSize)
	b.StartTimer()
	i := 0
	for i < b.N {
		b.StopTimer()
		tr := NewOrderedG[int](*btreeDegree)
		for _, v := range insertP {
			tr.ReplaceOrInsert(v)
		}
		b.StartTimer()
		for _, item := range removeP {
			tr.Delete(item)
			i++
			if i >= b.N {
				return
			}
		}
		if tr.Len() > 0 {
			panic(tr.Len())
		}
	}
}

func BenchmarkGetG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	removeP := rand.Perm(benchmarkTreeSize)
	b.StartTimer()
	i := 0
	for i < b.N {
		b.StopTimer()
		tr := NewOrderedG[int](*btreeDegree)
		for _, v := range insertP {
			tr.ReplaceOrInsert(v)
		}
		b.StartTimer()
		for _, item := range removeP {
			tr.Get(item)
			i++
			if i >= b.N {
				return
			}
		}
	}
}

func BenchmarkGetCloneEachTimeG(b *testing.B) {
	b.StopTimer()
	insertP := rand.Perm(benchmarkTreeSize)
	removeP := rand.Perm(benchmarkTreeSize)
	b.StartTimer()
	i := 0
	for i < b.N {
		b.StopTimer()
		tr := NewOrderedG[int](*btreeDegree)
		for _, v := range insertP {
			tr.ReplaceOrInsert(v)
		}
		b.StartTimer()
		for _, item := range removeP {
			tr = tr.Clone()
			tr.Get(item)
			i++
			if i >= b.N {
				return
			}
		}
	}
}

func BenchmarkAscendG(b *testing.B) {
	arr := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range arr {
		tr.ReplaceOrInsert(v)
	}
	sort.Ints(arr)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := 0
		tr.Ascend(func(item int) bool {
			if item != arr[j] {
				b.Fatalf("mismatch: expected: %v, got %v", arr[j], item)
			}
			j++
			return true
		})
	}
}

func BenchmarkDescendG(b *testing.B) {
	arr := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range arr {
		tr.ReplaceOrInsert(v)
	}
	sort.Ints(arr)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := len(arr) - 1
		tr.Descend(func(item int) bool {
			if item != arr[j] {
				b.Fatalf("mismatch: expected: %v, got %v", arr[j], item)
			}
			j--
			return true
		})
	}
}

func BenchmarkAscendRangeG(b *testing.B) {
	arr := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range arr {
		tr.ReplaceOrInsert(v)
	}
	sort.Ints(arr)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := 100
		tr.AscendRange(100, arr[len(arr)-100], func(item int) bool {
			if item != arr[j] {
				b.Fatalf("mismatch: expected: %v, got %v", arr[j], item)
			}
			j++
			return true
		})
		if j != len(arr)-100 {
			b.Fatalf("expected: %v, got %v", len(arr)-100, j)
		}
	}
}

func BenchmarkDescendRangeG(b *testing.B) {
	arr := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range arr {
		tr.ReplaceOrInsert(v)
	}
	sort.Ints(arr)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := len(arr) - 100
		tr.DescendRange(arr[len(arr)-100], 100, func(item int) bool {
			if item != arr[j] {
				b.Fatalf("mismatch: expected: %v, got %v", arr[j], item)
			}
			j--
			return true
		})
		if j != 100 {
			b.Fatalf("expected: %v, got %v", len(arr)-100, j)
		}
	}
}

func BenchmarkAscendGreaterOrEqualG(b *testing.B) {
	arr := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range arr {
		tr.ReplaceOrInsert(v)
	}
	sort.Ints(arr)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := 100
		k := 0
		tr.AscendGreaterOrEqual(100, func(item int) bool {
			if item != arr[j] {
				b.Fatalf("mismatch: expected: %v, got %v", arr[j], item)
			}
			j++
			k++
			return true
		})
		if j != len(arr) {
			b.Fatalf("expected: %v, got %v", len(arr), j)
		}
		if k != len(arr)-100 {
			b.Fatalf("expected: %v, got %v", len(arr)-100, k)
		}
	}
}

func BenchmarkDescendLessOrEqualG(b *testing.B) {
	arr := rand.Perm(benchmarkTreeSize)
	tr := NewOrderedG[int](*btreeDegree)
	for _, v := range arr {
		tr.ReplaceOrInsert(v)
	}
	sort.Ints(arr)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := len(arr) - 100
		k := len(arr)
		tr.DescendLessOrEqual(arr[len(arr)-100], func(item int) bool {
			if item != arr[j] {
				b.Fatalf("mismatch: expected: %v, got %v", arr[j], item)
			}
			j--
			k--
			return true
		})
		if j != -1 {
			b.Fatalf("expected: %v, got %v", -1, j)
		}
		if k != 99 {
			b.Fatalf("expected: %v, got %v", 99, k)
		}
	}
}

func cloneTestG(t *testing.T, b *BTreeG[int], start int, p []int, wg *sync.WaitGroup, trees *[]*BTreeG[int], lock *sync.Mutex) {
	t.Logf("Starting new clone at %v", start)
	lock.Lock()
	*trees = append(*trees, b)
	lock.Unlock()
	for i := start; i < cloneTestSize; i++ {
		b.ReplaceOrInsert(p[i])
		if i%(cloneTestSize/5) == 0 {
			wg.Add(1)
			go cloneTestG(t, b.Clone(), i+1, p, wg, trees, lock)
		}
	}
	wg.Done()
}

func TestCloneConcurrentOperationsG(t *testing.T) {
	b := NewOrderedG[int](*btreeDegree)
	trees := []*BTreeG[int]{}
	p := rand.Perm(cloneTestSize)
	var wg sync.WaitGroup
	wg.Add(1)
	go cloneTestG(t, b, 0, p, &wg, &trees, &sync.Mutex{})
	wg.Wait()
	want := intRange(cloneTestSize, false)
	t.Logf("Starting equality checks on %d trees", len(trees))
	for i, tree := range trees {
		if !reflect.DeepEqual(want, intAll(tree)) {
			t.Errorf("tree %v mismatch", i)
		}
	}
	t.Log("Removing half from first half")
	toRemove := intRange(cloneTestSize, false)[cloneTestSize/2:]
	for i := 0; i < len(trees)/2; i++ {
		tree := trees[i]
		wg.Add(1)
		go func() {
			for _, item := range toRemove {
				tree.Delete(item)
			}
			wg.Done()
		}()
	}
	wg.Wait()
	t.Log("Checking all values again")
	for i, tree := range trees {
		var wantpart []int
		if i < len(trees)/2 {
			wantpart = want[:cloneTestSize/2]
		} else {
			wantpart = want
		}
		if got := intAll(tree); !reflect.DeepEqual(wantpart, got) {
			t.Errorf("tree %v mismatch, want %v got %v", i, len(want), len(got))
		}
	}
}

func BenchmarkDeleteAndRestoreG(b *testing.B) {
	items := rand.Perm(16392)
	b.ResetTimer()
	b.Run(`CopyBigFreeList`, func(b *testing.B) {
		fl := NewFreeListG[int](16392)
		tr := NewWithFreeListG[int](*btreeDegree, Less[int](), fl)
		for _, v := range items {
			tr.ReplaceOrInsert(v)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dels := make([]int, 0, tr.Len())
			tr.Ascend(func(b int) bool {
				dels = append(dels, b)
				return true
			})
			for _, del := range dels {
				tr.Delete(del)
			}
			// tr is now empty, we make a new empty copy of it.
			tr = NewWithFreeListG[int](*btreeDegree, Less[int](), fl)
			for _, v := range items {
				tr.ReplaceOrInsert(v)
			}
		}
	})
	b.Run(`Copy`, func(b *testing.B) {
		tr := NewOrderedG[int](*btreeDegree)
		for _, v := range items {
			tr.ReplaceOrInsert(v)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dels := make([]int, 0, tr.Len())
			tr.Ascend(func(b int) bool {
				dels = append(dels, b)
				return true
			})
			for _, del := range dels {
				tr.Delete(del)
			}
			// tr is now empty, we make a new empty copy of it.
			tr = NewOrderedG[int](*btreeDegree)
			for _, v := range items {
				tr.ReplaceOrInsert(v)
			}
		}
	})
	b.Run(`ClearBigFreelist`, func(b *testing.B) {
		fl := NewFreeListG[int](16392)
		tr := NewWithFreeListG[int](*btreeDegree, Less[int](), fl)
		for _, v := range items {
			tr.ReplaceOrInsert(v)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tr.Clear(true)
			for _, v := range items {
				tr.ReplaceOrInsert(v)
			}
		}
	})
	b.Run(`Clear`, func(b *testing.B) {
		tr := NewOrderedG[int](*btreeDegree)
		for _, v := range items {
			tr.ReplaceOrInsert(v)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tr.Clear(true)
			for _, v := range items {
				tr.ReplaceOrInsert(v)
			}
		}
	})
}
