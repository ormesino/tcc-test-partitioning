package partitioner

import (
	"testing"
	"time"

	"tcc-test-partitioning/internal/model"
)

func TestFFD_KnownOptimum(t *testing.T) {
	pkgs := mkPkgs(5000, 5000, 4000, 4000, 3000, 3000, 2000, 2000)
	r := (&FFD{}).Partition(pkgs, 3)

	if got, want := r.Makespan, 10*time.Second; got != want {
		t.Errorf("Makespan = %v, want %v", got, want)
	}
}

func TestFFD_NoWorseThanLPTOnHeavyTail(t *testing.T) {
	pkgs := mkPkgs(9000, 4000, 3000, 2000, 1000, 800, 500, 300, 200, 100)
	ffd := (&FFD{}).Partition(pkgs, 3)
	lpt := (&LPT{}).Partition(pkgs, 3)

	if ffd.Makespan > lpt.Makespan {
		t.Errorf("FFD makespan = %v, want <= LPT makespan %v", ffd.Makespan, lpt.Makespan)
	}
}

func TestFFD_FallbackCapacity(t *testing.T) {
	pkgs := mkPkgs(8000, 7000, 6000)
	r := (&FFD{}).partition(pkgs, 2, 0)

	if got, want := sumLoads(r), sumDurations(pkgs); got != want {
		t.Errorf("sum(Loads) = %v, want %v", got, want)
	}
	if got, want := r.Makespan, sumDurations(pkgs); got != want {
		t.Errorf("fallback Makespan = %v, want %v", got, want)
	}
}

// TestFFD_DoesNotMutateInput protects the caller from surprise:
// FFD sorts internally and must not reorder the caller's slice.
func TestFFD_DoesNotMutateInput(t *testing.T) {
	pkgs := mkPkgs(100, 200, 300, 400)
	original := make([]model.PackageInfo, len(pkgs))
	copy(original, pkgs)

	_ = (&FFD{}).Partition(pkgs, 2)

	for i := range pkgs {
		if pkgs[i].Name != original[i].Name {
			t.Errorf("input mutated at index %d: %s vs %s",
				i, pkgs[i].Name, original[i].Name)
		}
	}
}
