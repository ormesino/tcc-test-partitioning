package main

// JSON output schema for `cmd/demo --output-json`.
//
// The demo iterates datasets × worker counts × algorithms, so the
// report is nested by dataset. Each dataset entry contains one
// workerRun per worker count, which holds the per-algorithm metrics
// plus the "best" algorithm summary.

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tcc-test-partitioning/internal/metrics"
	"tcc-test-partitioning/internal/model"
)

type demoReport struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Datasets    []datasetEntry `json:"datasets"`
}

type datasetEntry struct {
	Name         string      `json:"name"`
	PackageCount int         `json:"package_count"`
	T1NS         int64       `json:"t1_ns"`
	WorkerRuns   []workerRun `json:"worker_runs"`
}

type workerRun struct {
	Workers         int           `json:"workers"`
	IdealMakespanNS int64         `json:"ideal_makespan_ns"`
	Algorithms      []algMetrics  `json:"algorithms"`
	Best            bestAlgorithm `json:"best"`
}

type algMetrics struct {
	Algorithm   string  `json:"algorithm"`
	MakespanNS  int64   `json:"makespan_ns"`
	Speedup     float64 `json:"speedup"`
	Efficiency  float64 `json:"efficiency"`
	LoadStdDevS float64 `json:"load_stddev_s"`
	OverheadNS  int64   `json:"partitioning_overhead_ns"`
}

type bestAlgorithm struct {
	Algorithm  string  `json:"algorithm"`
	MakespanNS int64   `json:"makespan_ns"`
	Speedup    float64 `json:"speedup"`
}

// buildDatasetEntry materializes one dataset entry from the per-(ds,w)
// cache populated in main(). It reuses cached PartitionResults — it
// never re-invokes Partition().
func buildDatasetEntry(ds dataset, cache map[int][]model.PartitionResult, workerCounts []int) datasetEntry {
	var seq time.Duration
	for _, p := range ds.Packages {
		seq += p.Duration
	}

	runs := make([]workerRun, 0, len(workerCounts))
	for _, w := range workerCounts {
		results := cache[w]
		algs := make([]algMetrics, 0, len(results))

		var (
			bestName     string
			bestMakespan time.Duration
			bestSpeedup  float64
		)
		for _, r := range results {
			rep := metrics.Compute(r, seq)
			algs = append(algs, algMetrics{
				Algorithm:   rep.Algorithm,
				MakespanNS:  int64(rep.Makespan),
				Speedup:     rep.Speedup,
				Efficiency:  rep.Efficiency,
				LoadStdDevS: metrics.LoadStdDev(r),
				OverheadNS:  int64(rep.Overhead),
			})
			if bestMakespan == 0 || rep.Makespan < bestMakespan {
				bestMakespan = rep.Makespan
				bestName = rep.Algorithm
				bestSpeedup = rep.Speedup
			}
		}

		runs = append(runs, workerRun{
			Workers:         w,
			IdealMakespanNS: int64(seq / time.Duration(w)),
			Algorithms:      algs,
			Best: bestAlgorithm{
				Algorithm:  bestName,
				MakespanNS: int64(bestMakespan),
				Speedup:    bestSpeedup,
			},
		})
	}

	return datasetEntry{
		Name:         ds.Name,
		PackageCount: len(ds.Packages),
		T1NS:         int64(seq),
		WorkerRuns:   runs,
	}
}

func writeDemoReport(path string, r demoReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal demo report: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
