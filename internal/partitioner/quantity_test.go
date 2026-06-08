package partitioner

import (
	"testing"
)

// TestQuantity_ContiguousBlocks verifies the defining property of
// the quantity algorithm: packages are split into p contiguous
// blocks. The first (n mod p) blocks have ceil(n/p) packages, the
// remaining blocks have floor(n/p).
func TestQuantity_ContiguousBlocks(t *testing.T) {
	// n = 10, p = 3 → base=3, extra=1 → sizes [4, 3, 3]
	pkgs := mkPkgs(10, 20, 30, 40, 50, 60, 70, 80, 90, 100)
	r := (&Quantity{}).Partition(pkgs, 3)

	wantSizes := []int{4, 3, 3}
	for w, want := range wantSizes {
		if got := len(r.Partitions[w].Packages); got != want {
			t.Errorf("worker %d size = %d, want %d", w, got, want)
		}
	}

	// Worker 0 must contain the first 4 packages, in order.
	expected := []string{"pkg00", "pkg01", "pkg02", "pkg03"}
	for i, name := range expected {
		if r.Partitions[0].Packages[i].Name != name {
			t.Errorf("worker 0 pos %d: got %s, want %s",
				i, r.Partitions[0].Packages[i].Name, name)
		}
	}
	// Worker 1 starts at pkg04.
	if r.Partitions[1].Packages[0].Name != "pkg04" {
		t.Errorf("worker 1 first pkg = %s, want pkg04",
			r.Partitions[1].Packages[0].Name)
	}
}

// TestQuantity_EvenDivision verifies the simpler path where
// n is divisible by p (extra == 0): every block must have n/p items.
func TestQuantity_EvenDivision(t *testing.T) {
	pkgs := mkPkgs(100, 100, 100, 100, 100, 100) // n=6, p=3 → sizes [2,2,2]
	r := (&Quantity{}).Partition(pkgs, 3)

	for w, p := range r.Partitions {
		if len(p.Packages) != 2 {
			t.Errorf("worker %d size = %d, want 2", w, len(p.Packages))
		}
	}
}
