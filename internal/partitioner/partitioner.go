// Package partitioner defines the Partitioner interface and provides
// concrete implementations of test-suite partitioning algorithms.
//
// Each algorithm solves a variant of the P||Cmax problem (parallel
// identical machines, minimizing makespan) with different trade-offs
// between solution quality and computational cost.
//
// The baseline (native go test) is NOT a Partitioner — it delegates
// scheduling entirely to the Go toolchain and is handled as a
// special case in the executor package.
package partitioner

import "tcc-test-partitioning/internal/model"

// Partitioner is the strategy interface that all partitioning
// algorithms must implement.
//
// Design rationale: following Go's convention of small interfaces
// (cf. io.Reader, sort.Interface), Partitioner has only two methods.
// This makes it trivial to add new algorithms without modifying
// existing code (Open/Closed Principle).
type Partitioner interface {
	// Name returns a human-readable identifier for the algorithm,
	// e.g. "Round-Robin", "LPT". This value is stored in
	// PartitionResult.Algorithm for reporting purposes.
	Name() string

	// Partition distributes the given packages across the specified
	// number of workers and returns the complete schedule.
	//
	// Preconditions:
	//   - len(packages) >= 0  (empty input is valid; returns empty partitions)
	//   - workers >= 1
	//
	// The returned PartitionResult must have:
	//   - Algorithm set to Name()
	//   - Workers set to the workers argument
	//   - len(Partitions) == workers
	//   - Makespan == max(Partitions[k].Load)
	//   - Overhead measured internally by the algorithm
	Partition(packages []model.PackageInfo, workers int) model.PartitionResult
}
