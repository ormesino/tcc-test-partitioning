package partitioner

import (
	"testing"
	"time"

	"tcc-test-partitioning/internal/model"
)

// TestLPT_FirstPGoToDistinctWorkers verifies an essential property
// of LPT: the first p packages (heaviest after sorting) end up on
// p distinct workers, because every worker starts at Load=0 and the
// "minimum load" tie-breaking is stable on index.
func TestLPT_FirstPGoToDistinctWorkers(t *testing.T) {
	pkgs := mkPkgs(800, 700, 600, 500, 400, 300, 200, 100)
	const workers = 4

	r := (&LPT{}).Partition(pkgs, workers)

	seen := make(map[string]int)
	for w := 0; w < workers; w++ {
		first := r.Partitions[w].Packages[0].Name
		seen[first]++
	}
	heaviest := []string{"pkg00", "pkg01", "pkg02", "pkg03"}
	for _, n := range heaviest {
		if seen[n] != 1 {
			t.Errorf("heaviest %s should appear as first pkg on exactly one worker, seen=%d", n, seen[n])
		}
	}
}

func TestLPT_KnownOptimum(t *testing.T) {
	pkgs := mkPkgs(5000, 5000, 4000, 4000, 3000, 3000, 2000, 2000)
	r := (&LPT{}).Partition(pkgs, 3)

	if got, want := r.Makespan, 10*time.Second; got != want {
		t.Errorf("Makespan = %v, want %v (LPT must hit the optimum on this instance)", got, want)
	}
}

// TestLPT_GrahamBound computes the exact optimum for a small instance before
// applying Graham's bound. sum(D)/p is only a lower bound and therefore cannot
// replace OPT in this assertion.
func TestLPT_GrahamBound(t *testing.T) {
	pkgs := mkPkgs(900, 700, 600, 500, 400, 300, 250, 200)
	const workers = 3

	lpt := (&LPT{}).Partition(pkgs, workers).Makespan
	opt := exactMakespan(pkgs, workers)

	// Cmax(LPT) <= ((4p-1)/(3p)) * OPT, compared with integer arithmetic.
	left := int64(lpt) * int64(3*workers)
	right := int64(opt) * int64(4*workers-1)
	if left > right {
		t.Fatalf("LPT=%v exceeds Graham bound for OPT=%v and p=%d", lpt, opt, workers)
	}
}

func TestDurationTiesUsePackageName(t *testing.T) {
	pkgs := []model.PackageInfo{
		{Name: "z", Duration: time.Second},
		{Name: "a", Duration: time.Second},
		{Name: "m", Duration: time.Second},
	}
	for _, alg := range []Partitioner{&LPT{}, &FFD{}} {
		t.Run(alg.Name(), func(t *testing.T) {
			r := alg.Partition(pkgs, 1)
			got := []string{
				r.Partitions[0].Packages[0].Name,
				r.Partitions[0].Packages[1].Name,
				r.Partitions[0].Packages[2].Name,
			}
			want := []string{"a", "m", "z"}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("order=%v, want %v", got, want)
				}
			}
		})
	}
}

// exactMakespan solves a small identical-machine scheduling instance by
// branch-and-bound. It is deliberately test-only and suitable for tiny inputs.
func exactMakespan(pkgs []model.PackageInfo, workers int) time.Duration {
	loads := make([]time.Duration, workers)
	best := sumDurations(pkgs)

	var search func(int)
	search = func(i int) {
		if i == len(pkgs) {
			var makespan time.Duration
			for _, load := range loads {
				if load > makespan {
					makespan = load
				}
			}
			if makespan < best {
				best = makespan
			}
			return
		}

		seenLoads := make(map[time.Duration]struct{}, workers)
		for w := range loads {
			if _, seen := seenLoads[loads[w]]; seen {
				continue // symmetric worker state
			}
			seenLoads[loads[w]] = struct{}{}

			loads[w] += pkgs[i].Duration
			if loads[w] < best {
				search(i + 1)
			}
			loads[w] -= pkgs[i].Duration
		}
	}
	search(0)
	return best
}
