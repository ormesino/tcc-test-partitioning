package partitioner

// Round-Robin is the duration-unaware cyclic distribution strategy.
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
// It is a simple comparison strategy: assignments are balanced by position,
// but makespan can remain uneven because durations are ignored.
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

	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}

	for i, pkg := range packages {
		w := i % workers
		partitions[w].Packages = append(partitions[w].Packages, pkg)
		partitions[w].Load += pkg.Duration
	}

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
