// Package model defines the core domain types shared across the
// test-partitioning tool. These types map directly to the formal
// P||Cmax scheduling problem (Graham, 1969).
package model

import "time"

// PackageInfo represents a single Go test package — the atomic unit
// that partitioning algorithms assign to workers.
//
// In scheduling theory, each PackageInfo is a job j_i with:
//   - processing time p_i  = Duration
type PackageInfo struct {
	// Name is the fully qualified Go package import path,
	// e.g. "github.com/cli/cli/v2/pkg/cmd/pr".
	Name string `json:"name"`

	// Duration is the median wall-clock time observed across
	// multiple test executions (typically 10 runs, per ADR-007).
	Duration time.Duration `json:"duration_ns"`
}

// Partition represents the workload assigned to a single worker
// (machine M_k in scheduling notation). The Load field is the sum
// of processing times (or weights) of its packages; the makespan
// of the overall schedule is max(Load) across all partitions.
type Partition struct {
	// WorkerID identifies this worker (0-indexed).
	WorkerID int `json:"worker_id"`

	// Packages is the ordered list of packages assigned to this worker.
	Packages []PackageInfo `json:"packages"`

	// Load is the total estimated processing time on this worker:
	// Load = sum(Packages[i].Duration). Every algorithm reports Load
	// in raw Duration so that Makespan is directly comparable across
	// strategies. Weight-based algorithms (FFD) may use internal
	// weights for assignment decisions, but those weights are not
	// exposed in this field.
	Load time.Duration `json:"load_ns"`
}

// PartitionResult encapsulates the complete output of a partitioning
// algorithm: which packages go to which worker, the resulting
// makespan, and the computational overhead of the algorithm itself.
type PartitionResult struct {
	// Algorithm is the human-readable name of the algorithm that
	// produced this result (e.g. "LPT", "Round-Robin").
	Algorithm string `json:"algorithm"`

	// Workers is the number of parallel workers (machines) used.
	Workers int `json:"workers"`

	// Partitions holds one Partition per worker.
	Partitions []Partition `json:"partitions"`

	// Makespan is the maximum Load across all partitions:
	//   Cmax = max{ Partitions[k].Load | 0 <= k < Workers }
	Makespan time.Duration `json:"makespan_ns"`

	// Overhead is the wall-clock time spent by the partitioning
	// algorithm itself (excluding test execution).
	Overhead time.Duration `json:"overhead_ns"`
}
