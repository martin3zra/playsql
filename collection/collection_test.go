package collection

import (
	"cmp"
	"encoding/json"
	"slices"
	"testing"
)

type employee struct {
	ID     int
	Name   string
	Dept   string
	Salary int64 // cents
}

var staff = Collect([]employee{
	{1, "Ana", "Payroll", 5_000_000},
	{2, "Luis", "IT", 7_500_000},
	{3, "Rosa", "Payroll", 6_200_000},
	{4, "Juan", "IT", 4_800_000},
})

func TestFirstAndLastOnEmptyDoNotPanic(t *testing.T) {
	var empty Collection[employee]
	if _, ok := empty.First(); ok {
		t.Fatal("First on empty should report ok=false")
	}
	if _, ok := empty.Last(); ok {
		t.Fatal("Last on empty should report ok=false")
	}
}

func TestFirstLastGet(t *testing.T) {
	if e, _ := staff.First(); e.Name != "Ana" {
		t.Fatalf("First = %s", e.Name)
	}
	if e, _ := staff.Last(); e.Name != "Juan" {
		t.Fatalf("Last = %s", e.Name)
	}
	if e, _ := staff.Get(-2); e.Name != "Rosa" {
		t.Fatalf("Get(-2) = %s", e.Name)
	}
	if _, ok := staff.Get(10); ok {
		t.Fatal("Get out of range should report ok=false")
	}
}

func TestTimesAndRange(t *testing.T) {
	got := Times(3, func(n int) int { return n * 10 })
	if !slices.Equal(got, Collection[int]{10, 20, 30}) {
		t.Fatalf("Times = %v", got)
	}
	if Times(0, func(n int) int { return n }).IsNotEmpty() {
		t.Fatal("Times(0) should be empty")
	}
	if !slices.Equal(Range(1, 4), Collection[int]{1, 2, 3, 4}) {
		t.Fatalf("Range = %v", Range(1, 4))
	}
}

func TestChainingAndMap(t *testing.T) {
	names := Map(
		staff.
			Filter(func(e employee) bool { return e.Dept == "Payroll" }).
			SortBy(func(a, b employee) int { return cmp.Compare(b.Salary, a.Salary) }),
		func(e employee) string { return e.Name },
	)
	if !slices.Equal(names, Collection[string]{"Rosa", "Ana"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestSortByDoesNotMutate(t *testing.T) {
	original := slices.Clone(staff)
	_ = staff.SortBy(func(a, b employee) int { return cmp.Compare(a.Name, b.Name) })
	if !slices.Equal(staff, original) {
		t.Fatal("SortBy mutated the receiver")
	}
}

func TestTakeSkip(t *testing.T) {
	ids := func(c Collection[employee]) []int {
		return Map(c, func(e employee) int { return e.ID })
	}
	if got := ids(staff.Take(2)); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("Take(2) = %v", got)
	}
	if got := ids(staff.Take(-1)); !slices.Equal(got, []int{4}) {
		t.Fatalf("Take(-1) = %v", got)
	}
	if got := ids(staff.Skip(3)); !slices.Equal(got, []int{4}) {
		t.Fatalf("Skip(3) = %v", got)
	}
	if staff.Take(100).Count() != 4 || staff.Skip(100).Count() != 0 {
		t.Fatal("Take/Skip should clamp")
	}
}

func TestGroupBySumKeyBy(t *testing.T) {
	byDept := GroupBy(staff, func(e employee) string { return e.Dept })
	total := Sum(byDept["IT"], func(e employee) int64 { return e.Salary })
	if total != 12_300_000 {
		t.Fatalf("IT total = %d", total)
	}
	byID := KeyBy(staff, func(e employee) int { return e.ID })
	if byID[3].Name != "Rosa" {
		t.Fatalf("KeyBy[3] = %s", byID[3].Name)
	}
}

func TestUniqueBy(t *testing.T) {
	got := UniqueBy(staff, func(e employee) string { return e.Dept })
	if got.Count() != 2 || got[0].Name != "Ana" || got[1].Name != "Luis" {
		t.Fatalf("UniqueBy = %v", got)
	}
}

func TestChunk(t *testing.T) {
	chunks := Range(1, 5).Chunk(2)
	if len(chunks) != 3 || len(chunks[2]) != 1 {
		t.Fatalf("Chunk = %v", chunks)
	}
}

func TestJSONEmptyIsArray(t *testing.T) {
	var empty Collection[employee]
	b, _ := json.Marshal(map[string]any{"data": empty})
	if string(b) != `{"data":[]}` {
		t.Fatalf("json = %s", b)
	}
	b, _ = json.Marshal(Of(1, 2))
	if string(b) != `[1,2]` {
		t.Fatalf("json = %s", b)
	}
}

func TestLazy(t *testing.T) {
	calls := 0
	seq := MapSeq(Range(1, 1000).Values(), func(n int) int { calls++; return n * n })
	got := FromSeq(TakeSeq(FilterSeq(seq, func(n int) bool { return n%2 == 0 }), 2))
	if !slices.Equal(got, Collection[int]{4, 16}) {
		t.Fatalf("lazy = %v", got)
	}
	if calls != 4 {
		t.Fatalf("expected 4 map calls, got %d (not lazy)", calls)
	}
}

func BenchmarkFilterMap(b *testing.B) {
	data := Range(1, 10_000)
	for range b.N {
		_ = Map(data.Filter(func(n int) bool { return n%3 == 0 }), func(n int) int { return n * 2 })
	}
}

func BenchmarkPlainLoop(b *testing.B) {
	data := Range(1, 10_000)
	for range b.N {
		out := make([]int, 0, len(data))
		for _, n := range data {
			if n%3 == 0 {
				out = append(out, n*2)
			}
		}
		_ = out
	}
}
