package partitioner

// Round-Robin partitioning algorithm.
//
// Reference: classical cyclic distribution, widely used in operating
// systems and networking (e.g., Linux kernel task scheduling).
// Not specific to a single paper — it is the simplest possible
// load-distribution strategy.
//
// Complexity:
//   - Time:  O(n)  where n = len(packages)
//   - Space: O(n)  for storing the partitions

import (
	"time"

	"tcc-test-partitioning/internal/model"
)

// RoundRobin distributes packages cyclically among workers without
// considering package durations. Package j_i is assigned to worker
// (i mod p), where p = workers.
//
// This algorithm serves as a naive baseline: it provides fair
// distribution by count but offers no guarantees on makespan
// because it ignores processing times entirely.
type RoundRobin struct{}

// Name returns the algorithm identifier.
func (r *RoundRobin) Name() string {
	return "Round-Robin"
}

// Partition distributes packages in round-robin order.
//
// Preconditions:
//   - workers >= 1
//   - packages may be empty (returns empty partitions)
func (r *RoundRobin) Partition(packages []model.PackageInfo, workers int) model.PartitionResult {
	start := time.Now()
	if workers < 1 {
		return invalidWorkersResult(r.Name(), workers, time.Since(start))
	}

	// Initialize empty partitions for each worker.
	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}

	// Assign each package to worker (i mod workers).
	for i, pkg := range packages {
		w := i % workers
		partitions[w].Packages = append(partitions[w].Packages, pkg)
		partitions[w].Load += pkg.Duration
	}

	// Compute makespan = max(Load) across all partitions.
	var makespan time.Duration
	for _, p := range partitions {
		if p.Load > makespan {
			makespan = p.Load
		}
	}

	overhead := time.Since(start)

	return model.PartitionResult{
		Algorithm:  r.Name(),
		Workers:    workers,
		Partitions: partitions,
		Makespan:   makespan,
		Overhead:   overhead,
	}
}
