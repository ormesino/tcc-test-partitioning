package partitioner

// LPT (Longest Processing Time First) partitioning algorithm.
//
// Reference:
//   Graham, R. L. "Bounds on Multiprocessing Timing Anomalies."
//   SIAM Journal on Applied Mathematics, 17(2):416–429, 1969.
//
// Guarantee:
//   Cmax(LPT) <= (4/3 - 1/(3p)) * Cmax(OPT)
//   For p=4: at most 1.25x the optimal makespan.
//
// Complexity:
//   - Time:  O(n log n) for sorting + O(n*p) for greedy assignment
//   - Space: O(n)
//
// We use linear search for the least-loaded worker instead of a
// min-heap because p is small in our context (2, 4, 8). The
// difference between O(n*p) and O(n log p) is negligible for p <= 8.

import (
	"sort"
	"time"

	"tcc-test-partitioning/internal/model"
)

// LPT sorts packages by duration (descending) and greedily assigns
// each package to the worker with the smallest accumulated load.
//
// This is the first algorithm in our study that uses Duration as a
// decision criterion. It is expected to outperform Round-Robin and
// Quantity on datasets with high variance (hypothesis H1).
type LPT struct{}

// Name returns the algorithm identifier.
func (l *LPT) Name() string {
	return "LPT"
}

// Partition distributes packages using the LPT heuristic.
//
// Preconditions:
//   - workers >= 1
//   - packages may be empty (returns empty partitions)
func (l *LPT) Partition(packages []model.PackageInfo, workers int) model.PartitionResult {
	start := time.Now()

	// Initialize empty partitions for each worker.
	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}

	// Make a sorted copy (descending by Duration) to avoid
	// mutating the caller's slice.
	sorted := make([]model.PackageInfo, len(packages))
	copy(sorted, packages)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Duration > sorted[j].Duration
	})

	// Greedy assignment: for each package (heaviest first),
	// assign to the worker with the smallest current load.
	for _, pkg := range sorted {
		w := minLoadWorker(partitions)
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
		Algorithm:  l.Name(),
		Workers:    workers,
		Partitions: partitions,
		Makespan:   makespan,
		Overhead:   overhead,
	}
}

// minLoadWorker returns the index of the partition with the smallest
// Load. Ties are broken by lowest index (stable assignment).
// This is a linear scan — O(p) per call, acceptable for small p.
func minLoadWorker(partitions []model.Partition) int {
	minIdx := 0
	for i := 1; i < len(partitions); i++ {
		if partitions[i].Load < partitions[minIdx].Load {
			minIdx = i
		}
	}
	return minIdx
}
