package partitioner

// FFD Weighted (First Fit Decreasing with weighted cost) algorithm.
//
// Reference:
//   The FFD heuristic originates from bin packing literature:
//   Johnson, D. S. "Near-Optimal Bin Packing Algorithms."
//   PhD thesis, MIT, 1973.
//   Original guarantee: FFD <= 11/9 * OPT + 6/9 for bin packing.
//
//   Our adaptation replaces the "first bin that fits" rule with
//   "worker with least accumulated weight" (greedy assignment),
//   and adds a CV-based weighting factor to penalize high-variance
//   packages.
//
// Complexity:
//   - Time:  O(n log n) for sorting + O(n*p) for greedy assignment
//   - Space: O(n)

import (
	"sort"
	"time"

	"tcc-test-partitioning/internal/model"
)

// weightedPackage pairs a PackageInfo with its computed weight.
// The weight incorporates variability: w = Duration * (1 + CV).
type weightedPackage struct {
	Info   model.PackageInfo
	Weight float64 // Duration_ns * (1 + CV)
}

// FFD distributes packages using a weighted variant of the First Fit
// Decreasing heuristic. Each package's cost is:
//
//	w_i = Duration_i * (1 + CV_i)
//
// Packages are sorted by weight (descending) and greedily assigned
// to the worker with the smallest accumulated weight.
//
// Compared to LPT, FFD penalizes packages with high variability
// (high CV), isolating them in separate workers to reduce the risk
// of makespan blowup. This is expected to outperform LPT on
// heavy-tailed suites (hypothesis H2).
type FFD struct{}

// Name returns the algorithm identifier.
func (f *FFD) Name() string {
	return "FFD-Weighted"
}

// Partition distributes packages using the FFD weighted heuristic.
//
// Preconditions:
//   - workers >= 1
//   - packages may be empty (returns empty partitions)
//
// Note: Load in the returned Partitions is the sum of raw Durations
// (not weights), so that makespan is comparable across algorithms.
// The weight-based load is used only internally for assignment
// decisions.
func (f *FFD) Partition(packages []model.PackageInfo, workers int) model.PartitionResult {
	start := time.Now()

	// Initialize empty partitions for each worker.
	partitions := make([]model.Partition, workers)
	for i := range partitions {
		partitions[i] = model.Partition{
			WorkerID: i,
			Packages: []model.PackageInfo{},
		}
	}

	// Compute weights and sort descending.
	weighted := make([]weightedPackage, len(packages))
	for i, pkg := range packages {
		weighted[i] = weightedPackage{
			Info:   pkg,
			Weight: float64(pkg.Duration) * (1.0 + pkg.CV),
		}
	}
	sort.Slice(weighted, func(i, j int) bool {
		return weighted[i].Weight > weighted[j].Weight
	})

	// Track accumulated weight per worker for assignment decisions.
	weightLoads := make([]float64, workers)

	// Greedy assignment: for each package (heaviest weight first),
	// assign to the worker with the smallest accumulated weight.
	for _, wp := range weighted {
		w := minWeightWorker(weightLoads)
		partitions[w].Packages = append(partitions[w].Packages, wp.Info)
		partitions[w].Load += wp.Info.Duration // raw duration for comparable makespan
		weightLoads[w] += wp.Weight            // weight for assignment decisions
	}

	// Compute makespan = max(Load) across all partitions.
	// Load is raw Duration, so makespan is directly comparable
	// with other algorithms.
	var makespan time.Duration
	for _, p := range partitions {
		if p.Load > makespan {
			makespan = p.Load
		}
	}

	overhead := time.Since(start)

	return model.PartitionResult{
		Algorithm:  f.Name(),
		Workers:    workers,
		Partitions: partitions,
		Makespan:   makespan,
		Overhead:   overhead,
	}
}

// minWeightWorker returns the index of the worker with the smallest
// accumulated weight. Ties are broken by lowest index.
func minWeightWorker(loads []float64) int {
	minIdx := 0
	for i := 1; i < len(loads); i++ {
		if loads[i] < loads[minIdx] {
			minIdx = i
		}
	}
	return minIdx
}
