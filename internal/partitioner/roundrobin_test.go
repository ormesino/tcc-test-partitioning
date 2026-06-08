package partitioner

import (
	"testing"
	"time"
)

// TestRoundRobin_CyclicAssignment verifies the defining property of
// round-robin: package i is placed on worker (i mod workers), in
// input order.
func TestRoundRobin_CyclicAssignment(t *testing.T) {
	pkgs := mkPkgs(100, 200, 300, 400, 500, 600, 700)
	const workers = 3

	r := (&RoundRobin{}).Partition(pkgs, workers)

	// Expected per-worker package indexes (0-based, in input order):
	//   worker 0: pkg00, pkg03, pkg06
	//   worker 1: pkg01, pkg04
	//   worker 2: pkg02, pkg05
	want := map[int][]string{
		0: {"pkg00", "pkg03", "pkg06"},
		1: {"pkg01", "pkg04"},
		2: {"pkg02", "pkg05"},
	}

	for w, expected := range want {
		got := r.Partitions[w].Packages
		if len(got) != len(expected) {
			t.Fatalf("worker %d: %d packages, want %d", w, len(got), len(expected))
		}
		for i, name := range expected {
			if got[i].Name != name {
				t.Errorf("worker %d pos %d: got %s, want %s",
					w, i, got[i].Name, name)
			}
		}
	}
}

// TestRoundRobin_LoadComputed verifies that Load is the sum of the
// raw durations of assigned packages (round-robin is duration-blind
// during assignment, but Load still reflects real cost).
func TestRoundRobin_LoadComputed(t *testing.T) {
	pkgs := mkPkgs(100, 200, 300, 400) // workers=2 → w0=[0,2]=400ms, w1=[1,3]=600ms
	r := (&RoundRobin{}).Partition(pkgs, 2)

	if got, want := r.Partitions[0].Load, 400*time.Millisecond; got != want {
		t.Errorf("worker 0 Load = %v, want %v", got, want)
	}
	if got, want := r.Partitions[1].Load, 600*time.Millisecond; got != want {
		t.Errorf("worker 1 Load = %v, want %v", got, want)
	}
	if got, want := r.Makespan, 600*time.Millisecond; got != want {
		t.Errorf("Makespan = %v, want %v", got, want)
	}
}
