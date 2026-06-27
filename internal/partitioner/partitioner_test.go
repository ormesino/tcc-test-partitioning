package partitioner

import (
	"fmt"
	"testing"
	"time"

	"tcc-test-partitioning/internal/model"
)

// mkPkgs builds a slice of PackageInfo with deterministic names
// ("pkg00", "pkg01", ...) and the given durations in milliseconds.
func mkPkgs(ms ...int) []model.PackageInfo {
	out := make([]model.PackageInfo, len(ms))
	for i, m := range ms {
		out[i] = model.PackageInfo{
			Name:     fmt.Sprintf("pkg%02d", i),
			Duration: time.Duration(m) * time.Millisecond,
		}
	}
	return out
}

// allAlgorithms returns one fresh instance of every Partitioner
// implementation, used to drive the shared contract tests.
func allAlgorithms() []Partitioner {
	return []Partitioner{
		&RoundRobin{},
		&Quantity{},
		&LPT{},
		&FFD{},
	}
}

// sumDurations returns the total processing time of the input set
// — equivalent to T1 (perfectly sequential execution time).
func sumDurations(pkgs []model.PackageInfo) time.Duration {
	var total time.Duration
	for _, p := range pkgs {
		total += p.Duration
	}
	return total
}

// sumLoads returns the total Load across all partitions, which must
// equal sumDurations for any correct algorithm (no package lost,
// no package duplicated).
func sumLoads(r model.PartitionResult) time.Duration {
	var total time.Duration
	for _, p := range r.Partitions {
		total += p.Load
	}
	return total
}

// collectNames returns the multiset of package names across all
// partitions, used to verify that every input package appears
// exactly once in the output.
func collectNames(r model.PartitionResult) map[string]int {
	m := make(map[string]int)
	for _, part := range r.Partitions {
		for _, pkg := range part.Packages {
			m[pkg.Name]++
		}
	}
	return m
}

// TestContract_EmptyInput verifies that every algorithm returns
// `workers` empty partitions and zero makespan when given no
// packages. This covers requirement #3 of the project's coding rules
// (empty list edge case).
func TestContract_EmptyInput(t *testing.T) {
	const workers = 4
	for _, alg := range allAlgorithms() {
		t.Run(alg.Name(), func(t *testing.T) {
			r := alg.Partition(nil, workers)

			if r.Algorithm != alg.Name() {
				t.Errorf("Algorithm = %q, want %q", r.Algorithm, alg.Name())
			}
			if r.Workers != workers {
				t.Errorf("Workers = %d, want %d", r.Workers, workers)
			}
			if len(r.Partitions) != workers {
				t.Fatalf("len(Partitions) = %d, want %d",
					len(r.Partitions), workers)
			}
			for i, p := range r.Partitions {
				if len(p.Packages) != 0 {
					t.Errorf("partition[%d] has %d packages, want 0",
						i, len(p.Packages))
				}
				if p.Load != 0 {
					t.Errorf("partition[%d].Load = %v, want 0",
						i, p.Load)
				}
			}
			if r.Makespan != 0 {
				t.Errorf("Makespan = %v, want 0", r.Makespan)
			}
		})
	}
}

func TestContract_InvalidWorkersReturnsEmptyResult(t *testing.T) {
	for _, alg := range allAlgorithms() {
		t.Run(alg.Name(), func(t *testing.T) {
			r := alg.Partition(mkPkgs(100), 0)
			if r.Workers != 0 {
				t.Errorf("Workers = %d, want 0", r.Workers)
			}
			if len(r.Partitions) != 0 {
				t.Errorf("len(Partitions) = %d, want 0", len(r.Partitions))
			}
		})
	}
}

// TestContract_SingleWorker verifies that, with exactly one worker,
// every algorithm assigns every package to that worker and the
// resulting Load (and Makespan) equals sum(Duration).
func TestContract_SingleWorker(t *testing.T) {
	pkgs := mkPkgs(100, 200, 300, 50, 75)
	want := sumDurations(pkgs)

	for _, alg := range allAlgorithms() {
		t.Run(alg.Name(), func(t *testing.T) {
			r := alg.Partition(pkgs, 1)

			if len(r.Partitions) != 1 {
				t.Fatalf("len(Partitions) = %d, want 1", len(r.Partitions))
			}
			if got := len(r.Partitions[0].Packages); got != len(pkgs) {
				t.Errorf("packages on sole worker = %d, want %d",
					got, len(pkgs))
			}
			if r.Partitions[0].Load != want {
				t.Errorf("Load = %v, want %v", r.Partitions[0].Load, want)
			}
			if r.Makespan != want {
				t.Errorf("Makespan = %v, want %v", r.Makespan, want)
			}
		})
	}
}

// TestContract_MoreWorkersThanPackages verifies that no algorithm
// crashes or produces invalid results when workers > len(packages):
// every package must be assigned exactly once and the extra workers
// must remain idle with Load == 0.
func TestContract_MoreWorkersThanPackages(t *testing.T) {
	pkgs := mkPkgs(100, 200, 300)
	const workers = 8

	for _, alg := range allAlgorithms() {
		t.Run(alg.Name(), func(t *testing.T) {
			r := alg.Partition(pkgs, workers)

			if len(r.Partitions) != workers {
				t.Fatalf("len(Partitions) = %d, want %d",
					len(r.Partitions), workers)
			}

			names := collectNames(r)
			if len(names) != len(pkgs) {
				t.Errorf("distinct packages = %d, want %d",
					len(names), len(pkgs))
			}
			for _, p := range pkgs {
				if names[p.Name] != 1 {
					t.Errorf("package %s appears %d times, want 1",
						p.Name, names[p.Name])
				}
			}

			if got, want := sumLoads(r), sumDurations(pkgs); got != want {
				t.Errorf("sum(Loads) = %v, want %v", got, want)
			}
		})
	}
}

// TestContract_PackagePreservation guarantees, for every algorithm:
//   - each input package appears in exactly one partition,
//   - sum(Loads) == sum(Durations),
//   - Makespan == max(Load).
//
// This is the core correctness contract of the Partitioner interface,
// independent of the specific scheduling strategy.
func TestContract_PackagePreservation(t *testing.T) {
	pkgs := mkPkgs(50, 120, 30, 800, 200, 75, 600, 90, 150, 1000, 40, 250)
	totalDur := sumDurations(pkgs)

	for _, workers := range []int{1, 2, 4, 8} {
		for _, alg := range allAlgorithms() {
			t.Run(fmt.Sprintf("%s/p=%d", alg.Name(), workers), func(t *testing.T) {
				r := alg.Partition(pkgs, workers)

				names := collectNames(r)
				if len(names) != len(pkgs) {
					t.Errorf("distinct packages = %d, want %d",
						len(names), len(pkgs))
				}
				for _, p := range pkgs {
					if names[p.Name] != 1 {
						t.Errorf("package %s appears %d times, want 1",
							p.Name, names[p.Name])
					}
				}

				if got := sumLoads(r); got != totalDur {
					t.Errorf("sum(Loads) = %v, want %v", got, totalDur)
				}

				var maxLoad time.Duration
				for _, p := range r.Partitions {
					if p.Load > maxLoad {
						maxLoad = p.Load
					}
				}
				if r.Makespan != maxLoad {
					t.Errorf("Makespan = %v, want max(Load) = %v",
						r.Makespan, maxLoad)
				}
			})
		}
	}
}
