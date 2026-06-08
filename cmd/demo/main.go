// Demo program — final end-to-end comparison of all partitioning
// algorithms across multiple datasets and worker counts.
//
// This demo simulates the full experimental pipeline:
//  1. Load synthetic datasets (stand-in for real collected data).
//  2. Run all 4 algorithms with p ∈ {2, 4, 8} workers.
//  3. Compute and display metrics for each combination.
//  4. Print a summary highlighting best algorithm per dataset.
//
// Usage: go run cmd/demo/main.go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"tcc-test-partitioning/data/synthetic"
	"tcc-test-partitioning/internal/metrics"
	"tcc-test-partitioning/internal/model"
	"tcc-test-partitioning/internal/partitioner"
)

// dataset pairs a name with its package list.
type dataset struct {
	Name     string
	Packages []model.PackageInfo
}

func main() {
	outputJSON := flag.String("output-json", "",
		"Path to write the structured demo report as JSON. Empty disables.")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║   Test Partitioning — Final End-to-End Comparison Demo      ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// All four algorithms.
	algorithms := []partitioner.Partitioner{
		&partitioner.RoundRobin{},
		&partitioner.Quantity{},
		&partitioner.LPT{},
		&partitioner.FFD{},
	}

	// All three synthetic datasets.
	datasets := []dataset{
		{"Moderate (cli/cli, grpc-go profile)", synthetic.ProfileModerate()},
		{"Heavy-Tail (hugo, goreleaser profile)", synthetic.ProfileHeavyTail()},
		{"Mixed (robustness test)", synthetic.ProfileMixed()},
	}

	// Worker counts to test (same as planned experiments).
	workerCounts := []int{2, 4, 8}

	var dsEntries []datasetEntry

	for _, ds := range datasets {
		printDatasetHeader(ds)

		// Cache PartitionResult per (dataset, workers, algorithm) inside
		// this invocation so the summary table and the best-of summary
		// share identical numbers. Each (ds, w, alg) triple is computed
		// exactly once; no state crosses datasets or worker counts.
		cache := make(map[int][]model.PartitionResult, len(workerCounts))
		for _, w := range workerCounts {
			results := make([]model.PartitionResult, len(algorithms))
			for i, alg := range algorithms {
				results[i] = alg.Partition(ds.Packages, w)
			}
			cache[w] = results
			runComparison(ds, results, w)
		}

		printDatasetSummary(ds, cache, workerCounts)

		if *outputJSON != "" {
			dsEntries = append(dsEntries, buildDatasetEntry(ds, cache, workerCounts))
		}
	}

	if *outputJSON != "" {
		rep := demoReport{
			GeneratedAt: time.Now(),
			Datasets:    dsEntries,
		}
		if err := writeDemoReport(*outputJSON, rep); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing JSON report: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("JSON report written to %s\n", *outputJSON)
	}
}

// printDatasetHeader prints dataset info.
func printDatasetHeader(ds dataset) {
	var seqDuration time.Duration
	for _, p := range ds.Packages {
		seqDuration += p.Duration
	}

	fmt.Println("================================================================")
	fmt.Printf("  DATASET: %s\n", ds.Name)
	fmt.Printf("  Packages: %d | T1 (sequential): %v\n", len(ds.Packages), seqDuration)
	fmt.Println("================================================================")
	fmt.Println()
}

// runComparison renders the per-algorithm metrics table for one
// (dataset, workers) pair using pre-computed PartitionResults. It
// does NOT call Partition() — see main() for the cache.
func runComparison(ds dataset, results []model.PartitionResult, workers int) {
	// Compute T1 (sequential time) = sum of all durations.
	var seqDuration time.Duration
	for _, p := range ds.Packages {
		seqDuration += p.Duration
	}
	ideal := seqDuration / time.Duration(workers)

	fmt.Printf("  --- Workers: %d | Ideal makespan: %v ---\n\n", workers, ideal)

	// Summary table header.
	fmt.Printf("  %-14s | %12s | %7s | %7s | %10s\n",
		"Algorithm", "Makespan", "S(p)", "E(p)", "StdDev(s)")
	fmt.Printf("  ---------------|--------------|---------|---------|----------\n")

	for _, result := range results {
		report := metrics.Compute(result, seqDuration)
		stddev := metrics.LoadStdDev(result)

		fmt.Printf("  %-14s | %12v | %7.2f | %7.2f | %10.4f\n",
			report.Algorithm,
			report.Makespan,
			report.Speedup,
			report.Efficiency,
			stddev,
		)
	}
	fmt.Println()
}

// printDatasetSummary prints which algorithm achieved the best
// makespan for each worker count on this dataset, reading from the
// cache populated in main().
func printDatasetSummary(ds dataset, cache map[int][]model.PartitionResult, workerCounts []int) {
	var seqDuration time.Duration
	for _, p := range ds.Packages {
		seqDuration += p.Duration
	}

	fmt.Printf("  SUMMARY — Best makespan per worker count:\n")
	fmt.Printf("  %-8s | %-14s | %12s | %7s\n", "Workers", "Best Algorithm", "Makespan", "S(p)")
	fmt.Printf("  ---------|----------------|--------------|--------\n")

	for _, w := range workerCounts {
		var bestName string
		var bestMakespan time.Duration
		var bestSpeedup float64

		for _, result := range cache[w] {
			report := metrics.Compute(result, seqDuration)

			if bestMakespan == 0 || report.Makespan < bestMakespan {
				bestMakespan = report.Makespan
				bestName = report.Algorithm
				bestSpeedup = report.Speedup
			}
		}

		fmt.Printf("  %-8d | %-14s | %12v | %7.2f\n",
			w, bestName, bestMakespan, bestSpeedup)
	}
	fmt.Println()
	fmt.Println()
}
