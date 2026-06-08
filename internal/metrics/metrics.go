// Package metrics provides functions to compute performance metrics
// for test-suite partitioning results.
//
// All metrics map to standard parallel computing and scheduling
// theory concepts:
//   - Makespan (Cmax): max completion time across workers.
//   - Load variance: population variance of worker loads.
//   - Speedup S(p) = T1 / Tp (cf. Amdahl, 1967).
//   - Efficiency E(p) = S(p) / p (cf. Gustafson, 1988).
//   - Overhead: wall-clock time of the partitioning algorithm.
package metrics

import (
	"math"
	"time"

	"tcc-test-partitioning/internal/model"
)

// Report holds all computed metrics for a single partitioning result.
type Report struct {
	// Algorithm is the name of the algorithm that produced the result.
	Algorithm string

	// Workers is the number of parallel workers used.
	Workers int

	// Makespan is max(Load) across partitions.
	Makespan time.Duration

	// LoadVariance is the population variance of worker loads
	// (in seconds squared). A value of 0 means perfect balance.
	LoadVariance float64

	// Speedup is T1 / Tp, where T1 is the sequential baseline
	// time and Tp is the makespan with p workers.
	// Only meaningful when T1 > 0.
	Speedup float64

	// Efficiency is Speedup / p. Ideal value is 1.0.
	Efficiency float64

	// Overhead is the wall-clock time spent by the partitioning
	// algorithm itself.
	Overhead time.Duration
}

// Compute calculates all metrics for a PartitionResult.
//
// The seqDuration parameter is T1 — the total sequential execution
// time (sum of all package durations, or measured baseline-seq time).
// If seqDuration <= 0, Speedup and Efficiency are set to 0.
func Compute(result model.PartitionResult, seqDuration time.Duration) Report {
	makespan := Makespan(result)
	variance := LoadVariance(result)

	var speedup, efficiency float64
	if seqDuration > 0 && makespan > 0 {
		speedup = float64(seqDuration) / float64(makespan)
		efficiency = speedup / float64(result.Workers)
	}

	return Report{
		Algorithm:    result.Algorithm,
		Workers:      result.Workers,
		Makespan:     makespan,
		LoadVariance: variance,
		Speedup:      speedup,
		Efficiency:   efficiency,
		Overhead:     result.Overhead,
	}
}

// Makespan returns the maximum Load across all partitions in the
// result. This is Cmax in scheduling notation.
//
// If there are no partitions, returns 0.
func Makespan(result model.PartitionResult) time.Duration {
	var max time.Duration
	for _, p := range result.Partitions {
		if p.Load > max {
			max = p.Load
		}
	}
	return max
}

// LoadVariance computes the population variance of worker loads.
//
// Formula: σ² = (1/p) * Σ(L_k - L̄)²
//
// where L_k is the load of worker k and L̄ is the mean load.
// The result is in seconds squared (float64).
//
// We use population variance (not sample variance) because the
// partitions represent the entire population of workers, not a
// sample drawn from a larger population.
func LoadVariance(result model.PartitionResult) float64 {
	p := len(result.Partitions)
	if p == 0 {
		return 0
	}

	// Compute mean load in seconds.
	var sumSec float64
	loads := make([]float64, p)
	for i, part := range result.Partitions {
		loads[i] = part.Load.Seconds()
		sumSec += loads[i]
	}
	mean := sumSec / float64(p)

	// Compute population variance.
	var variance float64
	for _, l := range loads {
		diff := l - mean
		variance += diff * diff
	}
	variance /= float64(p)

	return variance
}

// TotalDuration computes the sum of all package durations across
// all partitions. This equals T1 (sequential execution time)
// assuming no overhead between packages.
func TotalDuration(result model.PartitionResult) time.Duration {
	var total time.Duration
	for _, p := range result.Partitions {
		for _, pkg := range p.Packages {
			total += pkg.Duration
		}
	}
	return total
}

// LoadStdDev returns the population standard deviation of worker
// loads in seconds. This is simply sqrt(LoadVariance).
func LoadStdDev(result model.PartitionResult) float64 {
	return math.Sqrt(LoadVariance(result))
}
