package partitioner

import (
	"testing"

	"tcc-test-partitioning/internal/model"
)

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
