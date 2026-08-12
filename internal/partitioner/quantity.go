package partitioner

// Quantity is the duration-unaware contiguous block strategy.
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
// It is a simple comparison strategy: package counts differ by at most one,
// but makespan can remain uneven because durations are ignored.
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
	if workers < 1 {
		return invalidWorkersResult(q.Name(), workers, time.Since(start))
	}
	n := len(packages)

	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}

	// The first n%workers blocks receive one additional package.
	base := n / workers  // floor(n/p)
	extra := n % workers // number of workers that get one extra

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
