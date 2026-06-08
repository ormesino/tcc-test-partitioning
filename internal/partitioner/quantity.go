package partitioner

// Equal-count (quantity-based) partitioning algorithm.
//
// Reference: this is the straightforward "block distribution"
// strategy described in most parallel computing textbooks,
// e.g., Pacheco (2011), §2.4.1, as the simplest static
// decomposition for loop iterations.
//
// Complexity:
//   - Time:  O(n)  where n = len(packages)
//   - Space: O(n)  for storing the partitions

import (
	"time"

	"tcc-test-partitioning/internal/model"
)

// Quantity divides packages into p contiguous blocks of approximately
// equal size, ignoring package durations entirely.
//
// Given n packages and p workers:
//   - the first (n mod p) workers receive ceil(n/p) packages
//   - the remaining workers receive floor(n/p) packages
//
// This is a purely count-based strategy: it guarantees balanced
// package counts but offers no guarantees on makespan because it
// ignores processing times.
type Quantity struct{}

// Name returns the algorithm identifier.
func (q *Quantity) Name() string {
	return "Quantity"
}

// Partition distributes packages in contiguous blocks.
//
// Preconditions:
//   - workers >= 1
//   - packages may be empty (returns empty partitions)
func (q *Quantity) Partition(packages []model.PackageInfo, workers int) model.PartitionResult {
	start := time.Now()
	n := len(packages)

	// Initialize empty partitions for each worker.
	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}

	// Distribute packages in contiguous blocks.
	// First (n % workers) workers get ceil(n/workers) packages,
	// the rest get floor(n/workers).
	base := n / workers   // floor(n/p)
	extra := n % workers  // number of workers that get one extra

	offset := 0
	for w := 0; w < workers; w++ {
		size := base
		if w < extra {
			size++
		}

		for j := 0; j < size && offset < n; j++ {
			partitions[w].Packages = append(partitions[w].Packages, packages[offset])
			partitions[w].Load += packages[offset].Duration
			offset++
		}
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
		Algorithm:  q.Name(),
		Workers:    workers,
		Partitions: partitions,
		Makespan:   makespan,
		Overhead:   overhead,
	}
}
