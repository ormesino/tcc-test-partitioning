package main

import "testing"

func TestParseWorkers(t *testing.T) {
	got, err := parseWorkers("1,2,4,2")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2, 4}
	if len(got) != len(want) {
		t.Fatalf("workers=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("workers=%v, want %v", got, want)
		}
	}
}

func TestMedian(t *testing.T) {
	if got := median([]int64{5, 1, 3}); got != 3 {
		t.Fatalf("median odd=%d, want 3", got)
	}
	if got := median([]int64{4, 2}); got != 3 {
		t.Fatalf("median even=%d, want 3", got)
	}
}
