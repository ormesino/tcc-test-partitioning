package main

// JSON output schema for --output-json. Kept separate from main.go
// to make the data model easy to evolve without touching the CLI
// glue code.
//
// Conventions:
//   - All durations are int64 nanoseconds (time.Duration's wire form).
//     Field names use the _ns suffix to flag the unit.
//   - Variance is in seconds^2; stddev in seconds.
//   - Optional fields use omitempty so simulate-mode reports do not
//     carry empty execution blocks.

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tcc-test-partitioning/internal/executor"
	"tcc-test-partitioning/internal/metrics"
	"tcc-test-partitioning/internal/model"
)

// outputReport is the top-level payload written to --output-json.
type outputReport struct {
	Mode         string     `json:"mode"`
	DataFile     string     `json:"data_file,omitempty"`
	ProjectPath  string     `json:"project_path,omitempty"`
	Workers      int        `json:"workers"`
	PackageCount int        `json:"package_count"`
	T1NS         int64      `json:"t1_ns"`
	PlannedT1NS  int64      `json:"planned_t1_ns,omitempty"`
	T1Source     string     `json:"t1_source"`
	GeneratedAt  time.Time  `json:"generated_at"`
	Algorithms   []algEntry `json:"algorithms"`
}

// algEntry captures everything the run knows about a single
// algorithm: the planned (theoretical) schedule plus, when in
// --mode run, the measured execution outcome.
type algEntry struct {
	Algorithm              string             `json:"algorithm"`
	PlannedMakespanNS      int64              `json:"planned_makespan_ns"`
	PlannedLoadVarianceS2  float64            `json:"planned_load_variance_s2"`
	PlannedLoadStdDevS     float64            `json:"planned_load_stddev_s"`
	PlannedSpeedup         float64            `json:"planned_speedup"`
	PlannedEfficiency      float64            `json:"planned_efficiency"`
	PartitioningOverheadNS int64              `json:"partitioning_overhead_ns"`
	Partitions             []partitionSummary `json:"partitions"`

	// Execution is only populated by --mode run.
	Execution *executionSummary `json:"execution,omitempty"`
}

// partitionSummary describes one worker's planned workload.
type partitionSummary struct {
	WorkerID     int      `json:"worker_id"`
	PackageCount int      `json:"package_count"`
	LoadNS       int64    `json:"load_ns"`
	Packages     []string `json:"packages,omitempty"`
}

// executionSummary captures the measured outcome of running the
// partitioned workload via `go test`.
type executionSummary struct {
	MakespanNS     int64           `json:"makespan_ns"`
	TotalElapsedNS int64           `json:"total_elapsed_ns"`
	Speedup        float64         `json:"speedup"`
	Efficiency     float64         `json:"efficiency"`
	LoadStdDevS    float64         `json:"load_stddev_s"`
	Workers        []workerSummary `json:"workers"`
}

// workerSummary captures one worker's measured outcome.
type workerSummary struct {
	WorkerID     int    `json:"worker_id"`
	PackageCount int    `json:"package_count"`
	ElapsedNS    int64  `json:"elapsed_ns"`
	Error        string `json:"error,omitempty"`
}

// buildPlannedEntry materializes the planned (theoretical) portion of
// an algEntry from a cached PartitionResult plus the canonical T1.
// includePackageNames controls whether each partition embeds the full
// package list (useful for auditing partitions, noisy for big runs).
func buildPlannedEntry(r model.PartitionResult, t1 time.Duration, includePackageNames bool) algEntry {
	report := metrics.Compute(r, t1)

	parts := make([]partitionSummary, len(r.Partitions))
	for i, p := range r.Partitions {
		ps := partitionSummary{
			WorkerID:     p.WorkerID,
			PackageCount: len(p.Packages),
			LoadNS:       int64(p.Load),
		}
		if includePackageNames {
			ps.Packages = make([]string, len(p.Packages))
			for j, pkg := range p.Packages {
				ps.Packages[j] = pkg.Name
			}
		}
		parts[i] = ps
	}

	return algEntry{
		Algorithm:              r.Algorithm,
		PlannedMakespanNS:      int64(report.Makespan),
		PlannedLoadVarianceS2:  report.LoadVariance,
		PlannedLoadStdDevS:     metrics.LoadStdDev(r),
		PlannedSpeedup:         report.Speedup,
		PlannedEfficiency:      report.Efficiency,
		PartitioningOverheadNS: int64(report.Overhead),
		Partitions:             parts,
	}
}

// attachExecution adds the measured execution outcome to an entry,
// re-using the planned partitions for package mapping.
func attachExecution(entry *algEntry, planned model.PartitionResult, exec executor.ExecutionResult, t1 time.Duration) {
	// Build a synthetic PartitionResult whose Loads are the measured
	// worker elapsed times, so metrics.Compute reflects real wall-clock.
	realParts := make([]model.Partition, len(exec.WorkerResults))
	for i, wr := range exec.WorkerResults {
		realParts[i] = model.Partition{
			WorkerID: wr.WorkerID,
			Packages: planned.Partitions[i].Packages,
			Load:     wr.Elapsed,
		}
	}
	realResult := model.PartitionResult{
		Algorithm:  planned.Algorithm,
		Workers:    planned.Workers,
		Partitions: realParts,
		Makespan:   exec.Makespan,
		Overhead:   planned.Overhead,
	}
	rep := metrics.Compute(realResult, t1)

	workers := make([]workerSummary, len(exec.WorkerResults))
	for i, wr := range exec.WorkerResults {
		ws := workerSummary{
			WorkerID:     wr.WorkerID,
			PackageCount: wr.PackageCount,
			ElapsedNS:    int64(wr.Elapsed),
		}
		if wr.Error != nil {
			ws.Error = wr.Error.Error()
		}
		workers[i] = ws
	}

	entry.Execution = &executionSummary{
		MakespanNS:     int64(exec.Makespan),
		TotalElapsedNS: int64(exec.TotalElapsed),
		Speedup:        rep.Speedup,
		Efficiency:     rep.Efficiency,
		LoadStdDevS:    metrics.LoadStdDev(realResult),
		Workers:        workers,
	}
}

// writeOutputReport serializes the report to path as indented JSON.
func writeOutputReport(path string, r outputReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
