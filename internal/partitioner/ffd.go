package partitioner

// FFD Multifit algorithm.
//
// Reference:
//   Coffman, E. G., Garey, M. R., & Johnson, D. S. (1978).
//   "An Application of Bin-Packing to Multiprocessor Scheduling."
//   SIAM Journal on Computing, 7(1), 1-17.
//
//   Multifit uses binary search on the target makespan C, and applies
//   First Fit Decreasing (FFD) to try to pack all jobs into p bins
//   of capacity C. It has a theoretical upper bound of 11/9 OPT + 6/9,
//   which is tighter than LPT's 4/3 OPT - 1/3p.
//
// Complexity:
//   - Time:  O(n log n) for sorting + O(k * n * p) for k binary search iterations
//   - Space: O(n)

import (
	"sort"
	"time"

	"tcc-test-partitioning/internal/model"
)

// FFD distributes packages using the Multifit algorithm, which
// performs a binary search over the possible makespan capacities
// and applies First Fit Decreasing to pack the packages.
type FFD struct{}

// Name returns the algorithm identifier.
func (f *FFD) Name() string {
	return "FFD-Multifit"
}

// Partition distributes packages using the Multifit heuristic.
func (f *FFD) Partition(packages []model.PackageInfo, workers int) model.PartitionResult {
	return f.partition(packages, workers, 40)
}

func (f *FFD) partition(packages []model.PackageInfo, workers, searchIterations int) model.PartitionResult {
	start := time.Now()
	if workers < 1 {
		return invalidWorkersResult(f.Name(), workers, time.Since(start))
	}

	if len(packages) == 0 {
		return emptyPartitionResult(f.Name(), workers, time.Since(start))
	}

	// 1. Sort packages descending by duration
	sortedPkgs := make([]model.PackageInfo, len(packages))
	copy(sortedPkgs, packages)
	sort.Slice(sortedPkgs, func(i, j int) bool {
		return sortedPkgs[i].Duration > sortedPkgs[j].Duration
	})

	// 2. Determine bounds for binary search
	var maxDuration time.Duration
	var sumDuration time.Duration
	for _, p := range sortedPkgs {
		if p.Duration > maxDuration {
			maxDuration = p.Duration
		}
		sumDuration += p.Duration
	}

	lowerBound := maxDuration
	avgDuration := time.Duration(int64(sumDuration) / int64(workers))
	if avgDuration > lowerBound {
		lowerBound = avgDuration
	}
	upperBound := sumDuration

	var bestAllocation []model.Partition
	var bestMakespan time.Duration

	// 3. Binary Search (Multifit loop) - typically 40 iterations is enough for high precision
	for iter := 0; iter < searchIterations; iter++ {
		capacity := lowerBound + (upperBound-lowerBound)/2

		allocation, fits := tryFFD(sortedPkgs, workers, capacity)
		if fits {
			bestAllocation = allocation
			upperBound = capacity // Try to find a tighter packing
		} else {
			lowerBound = capacity // Need more capacity
		}
	}

	// 4. Fallback in case binary search didn't find a perfect fit
	// (or bounds were too tight initially).
	if bestAllocation == nil {
		bestAllocation, _ = tryFFD(sortedPkgs, workers, sumDuration)
	}

	// Re-calculate precise makespan of the valid allocation
	bestMakespan = 0
	for _, p := range bestAllocation {
		if p.Load > bestMakespan {
			bestMakespan = p.Load
		}
	}

	overhead := time.Since(start)

	return model.PartitionResult{
		Algorithm:  f.Name(),
		Workers:    workers,
		Partitions: bestAllocation,
		Makespan:   bestMakespan,
		Overhead:   overhead,
	}
}

func tryFFD(sortedPkgs []model.PackageInfo, workers int, capacity time.Duration) ([]model.Partition, bool) {
	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: make([]model.PackageInfo, 0),
		}
	}

	for _, pkg := range sortedPkgs {
		placed := false
		for w := 0; w < workers; w++ {
			if partitions[w].Load+pkg.Duration <= capacity {
				partitions[w].Packages = append(partitions[w].Packages, pkg)
				partitions[w].Load += pkg.Duration
				placed = true
				break
			}
		}
		if !placed {
			return nil, false
		}
	}
	return partitions, true
}

func emptyPartitionResult(name string, workers int, overhead time.Duration) model.PartitionResult {
	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}
	return model.PartitionResult{
		Algorithm:  name,
		Workers:    workers,
		Partitions: partitions,
		Overhead:   overhead,
	}
}

func invalidWorkersResult(name string, workers int, overhead time.Duration) model.PartitionResult {
	return model.PartitionResult{
		Algorithm:  name,
		Workers:    workers,
		Partitions: []model.Partition{},
		Overhead:   overhead,
	}
}
