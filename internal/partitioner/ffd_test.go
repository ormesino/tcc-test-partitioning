package partitioner

import (
	"testing"
	"time"

	"tcc-test-partitioning/internal/model"
)

// TestFFD_EquivalentToLPTWhenCVIsZero verifies that, when every
// package has CV=0, the weight degenerates to Duration and FFD
// must produce the same Makespan as LPT. (Per-worker assignments
// may differ only by tie-breaking, but the makespan is invariant.)
func TestFFD_EquivalentToLPTWhenCVIsZero(t *testing.T) {
	pkgs := mkPkgs(900, 700, 600, 500, 400, 300, 250, 200, 150, 100)

	for _, p := range []int{2, 4, 8} {
		lpt := (&LPT{}).Partition(pkgs, p)
		ffd := (&FFD{}).Partition(pkgs, p)

		if lpt.Makespan != ffd.Makespan {
			t.Errorf("p=%d: LPT Makespan=%v vs FFD Makespan=%v (must match when CV=0)",
				p, lpt.Makespan, ffd.Makespan)
		}
	}
}

// TestFFD_WeightAltersOrder verifies that CV is actually consulted:
// a moderate-duration package with high CV must be placed BEFORE a
// longer-duration package with CV=0, because its weight is larger.
//
// Setup:
//
//	A: Duration=5s,  CV=0   → weight = 5
//	B: Duration=4s,  CV=2   → weight = 12   ← heaviest after weighting
//	C: Duration=3s,  CV=0   → weight = 3
//
// With workers=2 and stable tie-breaking on lowest index:
//   - FFD sorts by weight desc → B, A, C
//   - B → w0 (load=4); A → w1 (load=5); C → w0 (load=7)
//   - Worker 0's first package must be B.
//
// With LPT (sorted by Duration):
//   - A, B, C → A → w0; B → w1; C → w1
//   - Worker 0's first package would be A.
func TestFFD_WeightAltersOrder(t *testing.T) {
	pkgs := []model.PackageInfo{
		{Name: "A", Duration: 5 * time.Second, CV: 0},
		{Name: "B", Duration: 4 * time.Second, CV: 2},
		{Name: "C", Duration: 3 * time.Second, CV: 0},
	}

	r := (&FFD{}).Partition(pkgs, 2)

	if got := r.Partitions[0].Packages[0].Name; got != "B" {
		t.Errorf("worker 0 first pkg = %s, want B (CV weighting must reorder)",
			got)
	}
}

// TestFFD_LoadUsesRawDuration verifies the documented invariant of
// FFD: Load (and therefore Makespan) is summed from raw Durations,
// not weights. Without this, Makespans would not be comparable
// across algorithms.
func TestFFD_LoadUsesRawDuration(t *testing.T) {
	pkgs := []model.PackageInfo{
		{Name: "A", Duration: 5 * time.Second, CV: 0},
		{Name: "B", Duration: 4 * time.Second, CV: 2},
		{Name: "C", Duration: 3 * time.Second, CV: 0},
	}

	r := (&FFD{}).Partition(pkgs, 1)

	want := 12 * time.Second // raw sum, NOT 5 + 12 + 3 = 20
	if r.Partitions[0].Load != want {
		t.Errorf("Load = %v, want %v (must use raw Duration, not weight)",
			r.Partitions[0].Load, want)
	}
	if r.Makespan != want {
		t.Errorf("Makespan = %v, want %v", r.Makespan, want)
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
