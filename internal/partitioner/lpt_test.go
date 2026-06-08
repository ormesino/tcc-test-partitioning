package partitioner

import (
	"testing"
	"time"
)

// TestLPT_FirstPGoToDistinctWorkers verifies an essential property
// of LPT: the first p packages (heaviest after sorting) end up on
// p distinct workers, because every worker starts at Load=0 and the
// "minimum load" tie-breaking is stable on index.
func TestLPT_FirstPGoToDistinctWorkers(t *testing.T) {
	pkgs := mkPkgs(800, 700, 600, 500, 400, 300, 200, 100)
	const workers = 4

	r := (&LPT{}).Partition(pkgs, workers)

	// The 4 heaviest (pkg00..pkg03) must each be the FIRST package
	// on a distinct worker.
	seen := make(map[string]int)
	for w := 0; w < workers; w++ {
		first := r.Partitions[w].Packages[0].Name
		seen[first]++
	}
	heaviest := []string{"pkg00", "pkg01", "pkg02", "pkg03"}
	for _, n := range heaviest {
		if seen[n] != 1 {
			t.Errorf("heaviest %s should appear as first pkg on exactly one worker, seen=%d",
				n, seen[n])
		}
	}
}

// TestLPT_KnownOptimum constructs an instance with a known optimal
// makespan and asserts that LPT finds it. The instance is
//
//	durations = {5, 5, 4, 4, 3, 3, 2, 2}  (sum = 28)
//	workers   = 3
//	OPT       = ceil(28/3) = 10 (achievable: {5,3,2}, {5,3,2}, {4,4})
//
// LPT produces loads {10, 10, 8} → Cmax = 10 = OPT.
func TestLPT_KnownOptimum(t *testing.T) {
	pkgs := mkPkgs(5000, 5000, 4000, 4000, 3000, 3000, 2000, 2000)
	r := (&LPT{}).Partition(pkgs, 3)

	if got, want := r.Makespan, 10*time.Second; got != want {
		t.Errorf("Makespan = %v, want %v (LPT must hit the optimum on this instance)",
			got, want)
	}
}

// TestLPT_GrahamBound checks that LPT's makespan respects the
// Graham 1969 bound: Cmax(LPT) <= (4/3 - 1/(3p)) * OPT.
//
// We use sum(D)/p as a lower bound on OPT (i.e. perfect balance);
// the bound then becomes:
//
//	Cmax(LPT) <= (4/3 - 1/(3p)) * sum(D) / p   (weaker but safe)
//
// Failing this would mean either the algorithm is broken or our
// understanding of the bound is wrong.
func TestLPT_GrahamBound(t *testing.T) {
	pkgs := mkPkgs(900, 700, 600, 500, 400, 300, 250, 200, 150, 100, 80, 50)
	const workers = 4

	r := (&LPT{}).Partition(pkgs, workers)

	total := sumDurations(pkgs)
	lowerBoundOPT := float64(total) / float64(workers)
	bound := (4.0/3.0 - 1.0/(3.0*float64(workers))) * lowerBoundOPT

	if float64(r.Makespan) > bound {
		t.Errorf("Makespan = %v exceeds (4/3 - 1/(3p)) * (sum/p) = %v ns",
			r.Makespan, bound)
	}
}
