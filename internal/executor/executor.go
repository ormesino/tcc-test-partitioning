// Package executor runs go test commands on partitioned packages
// and measures wall-clock execution time per worker.
//
// The executor bridges the gap between theoretical partitioning
// (which operates on estimated durations) and empirical measurement
// (which captures real execution times including I/O, compilation,
// and system noise).
//
// Concurrency model: one goroutine per worker, following Go's CSP
// model (Hoare, 1978). Results are collected via channels to avoid
// race conditions.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tcc-test-partitioning/internal/model"
)

// BaselineReport is the persisted form of a baseline measurement
// (sequential or native-parallel). It is written by the baseline
// modes and consumed by partitioning runs to obtain a methodologically
// sound T1 for speedup computation (ADR-011).
type BaselineReport struct {
	Mode        string        `json:"mode"`        // "baseline-seq" or "baseline-par"
	Parallelism int           `json:"parallelism"` // p for baseline-par; 1 for baseline-seq
	Duration    time.Duration `json:"duration_ns"` // wall-clock, in nanoseconds
	MeasuredAt  time.Time     `json:"measured_at"`
	ProjectPath string        `json:"project_path"`
}

// WriteBaselineReport serializes the report to path as indented JSON.
func WriteBaselineReport(path string, r BaselineReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadBaselineReport reads a BaselineReport previously written by
// WriteBaselineReport.
func LoadBaselineReport(path string) (BaselineReport, error) {
	var r BaselineReport
	data, err := os.ReadFile(path)
	if err != nil {
		return r, fmt.Errorf("reading baseline: %w", err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("parsing baseline: %w", err)
	}
	return r, nil
}

// WorkerResult holds the execution outcome of a single worker.
type WorkerResult struct {
	// WorkerID identifies which worker produced this result.
	WorkerID int

	// Elapsed is the wall-clock time from start to finish of
	// this worker's go test invocation.
	Elapsed time.Duration

	// PackageCount is the number of packages executed.
	PackageCount int

	// Error holds any error from the go test command.
	// A non-nil error does not necessarily mean test failure —
	// it could be a compilation error or timeout.
	Error error

	// Output holds the combined stdout+stderr of go test.
	Output string
}

// ExecutionResult holds the aggregated outcome of running all
// workers in parallel.
type ExecutionResult struct {
	// Mode describes how the execution was run
	// ("partitioned", "baseline-seq", "baseline-par").
	Mode string

	// Workers is the number of parallel workers.
	Workers int

	// WorkerResults holds one result per worker, indexed by WorkerID.
	WorkerResults []WorkerResult

	// Makespan is the wall-clock time from launching the first
	// worker to the last worker finishing.
	Makespan time.Duration

	// TotalElapsed is the sum of all workers' Elapsed times.
	// This approximates T1 when workers=1.
	TotalElapsed time.Duration
}

// Config holds execution parameters.
type Config struct {
	// ProjectPath is the root directory of the Go project under test.
	ProjectPath string

	// Timeout is the maximum time allowed for the entire execution.
	// Zero means no timeout.
	Timeout time.Duration

	// Count is the -count flag for go test (default: 1, per ADR-008).
	Count int

	// Verbose enables -v flag on go test.
	Verbose bool

	// WarmCache, when true, pre-compiles all test binaries before
	// launching workers. This populates Go's build cache so that
	// workers only measure test execution time, not compilation.
	// Simulates a CI environment with a warm build cache.
	WarmCache bool
}

// RunPartitioned executes go test for each partition in parallel,
// one goroutine per worker, and measures wall-clock time.
//
// When cfg.WarmCache is true, a pre-compilation step runs before
// launching workers. This compiles all test binaries into Go's
// build cache without executing any tests, so that the workers'
// wall-clock time reflects only test execution, not compilation.
// The pre-compilation uses default parallelism (all CPUs) for speed
// and is NOT included in the makespan measurement.
func RunPartitioned(cfg Config, partResult model.PartitionResult) ExecutionResult {
	if cfg.WarmCache {
		warmBuildCache(cfg)
	}

	workers := len(partResult.Partitions)
	resultCh := make(chan WorkerResult, workers)
	var wg sync.WaitGroup

	overallStart := time.Now()

	for _, partition := range partResult.Partitions {
		wg.Add(1)
		go func(p model.Partition) {
			defer wg.Done()

			wr := runWorker(cfg, p)
			resultCh <- wr
		}(partition)
	}

	// Close channel once all workers finish.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results.
	workerResults := make([]WorkerResult, workers)
	var totalElapsed time.Duration
	for wr := range resultCh {
		workerResults[wr.WorkerID] = wr
		totalElapsed += wr.Elapsed
	}

	makespan := time.Since(overallStart)

	return ExecutionResult{
		Mode:          "partitioned",
		Workers:       workers,
		WorkerResults: workerResults,
		Makespan:      makespan,
		TotalElapsed:  totalElapsed,
	}
}

// RunBaselineSeq executes go test sequentially (-p 1 -parallel 1)
// to measure T1 for speedup calculation.
//
// TODO: validar com ambiente Go
func RunBaselineSeq(cfg Config) ExecutionResult {
	start := time.Now()

	// Build command: go test -p 1 -parallel 1 -count=1 ./...
	args := []string{"test", "-p", "1", "-parallel", "1",
		"-count", fmt.Sprintf("%d", cfg.Count)}
	if cfg.Verbose {
		args = append(args, "-v")
	}
	args = append(args, "./...")

	output, err := runGoTest(cfg, args)
	elapsed := time.Since(start)

	wr := WorkerResult{
		WorkerID:     0,
		Elapsed:      elapsed,
		PackageCount: 0, // unknown without parsing output
		Error:        err,
		Output:       output,
	}

	return ExecutionResult{
		Mode:          "baseline-seq",
		Workers:       1,
		WorkerResults: []WorkerResult{wr},
		Makespan:      elapsed,
		TotalElapsed:  elapsed,
	}
}

// RunBaselinePar executes go test with native parallelism (-p P)
// for direct comparison with partitioning algorithms at the same
// level of parallelism.
//
// TODO: validar com ambiente Go
func RunBaselinePar(cfg Config, parallelism int) ExecutionResult {
	start := time.Now()

	// Build command: go test -p P -parallel 1 -count=1 ./...
	args := []string{"test", "-p", fmt.Sprintf("%d", parallelism),
		"-parallel", "1",
		"-count", fmt.Sprintf("%d", cfg.Count)}
	if cfg.Verbose {
		args = append(args, "-v")
	}
	args = append(args, "./...")

	output, err := runGoTest(cfg, args)
	elapsed := time.Since(start)

	wr := WorkerResult{
		WorkerID:     0,
		Elapsed:      elapsed,
		PackageCount: 0,
		Error:        err,
		Output:       output,
	}

	return ExecutionResult{
		Mode:          "baseline-par",
		Workers:       parallelism,
		WorkerResults: []WorkerResult{wr},
		Makespan:      elapsed,
		TotalElapsed:  elapsed,
	}
}

// runWorker executes go test for a single partition and returns
// the result with wall-clock timing.
//
// TODO: validar com ambiente Go
func runWorker(cfg Config, partition model.Partition) WorkerResult {
	if len(partition.Packages) == 0 {
		return WorkerResult{
			WorkerID:     partition.WorkerID,
			Elapsed:      0,
			PackageCount: 0,
		}
	}

	// Build the list of package paths.
	pkgPaths := make([]string, len(partition.Packages))
	for i, pkg := range partition.Packages {
		pkgPaths[i] = pkg.Name
	}

	// Build command: go test -p 1 -parallel 1 -count=1 [-v] pkg1 pkg2 ...
	// Restricting to -p 1 -parallel 1 ensures this worker acts as a single
	// sequential processor, matching the theoretical P||Cmax scheduling model
	// and avoiding combinatorial explosion of parallelism that causes OOM.
	args := []string{"test", "-p", "1", "-parallel", "1", "-count", fmt.Sprintf("%d", cfg.Count)}
	if cfg.Verbose {
		args = append(args, "-v")
	}
	args = append(args, pkgPaths...)

	start := time.Now()
	output, err := runGoTest(cfg, args)
	elapsed := time.Since(start)

	return WorkerResult{
		WorkerID:     partition.WorkerID,
		Elapsed:      elapsed,
		PackageCount: len(partition.Packages),
		Error:        err,
		Output:       output,
	}
}

// runGoTest executes a go test command with the given arguments
// and returns combined output. Respects cfg.Timeout.
//
// TODO: validar com ambiente Go
func runGoTest(cfg Config, args []string) (string, error) {
	var ctx context.Context
	var cancel context.CancelFunc

	if cfg.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), cfg.Timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = cfg.ProjectPath

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// warmBuildCache pre-compiles all test binaries in the project
// without running any tests. It uses `-run=^$` (matches no test
// names) to trigger compilation only. The default `-p` (all CPUs)
// is used for maximum compilation speed.
//
// After this function returns, Go's build cache (GOCACHE) contains
// the compiled test binaries. Subsequent `go test` invocations
// (even with `-p 1`) will skip compilation and only run tests.
//
// This simulates a CI environment where the build cache is warm
// from a previous pipeline stage or a cached Docker layer.
func warmBuildCache(cfg Config) {
	fmt.Fprintf(os.Stderr, "  [warm-cache] Pre-compiling test binaries for %s...\n", cfg.ProjectPath)
	start := time.Now()

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	// -run=^$ matches no test, so nothing executes.
	// -count=1 ensures the test cache is not used, but the build cache IS used.
	// Default -p (GOMAXPROCS) gives maximum compilation parallelism.
	cmd := exec.CommandContext(ctx, "go", "test", "-run=^$", "-count=1", "./...")
	cmd.Dir = cfg.ProjectPath
	cmd.Stdout = os.Stderr // Show compilation progress on stderr.
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [warm-cache] WARNING: pre-compilation had errors: %v\n", err)
		// Continue anyway — partial cache is still useful.
	}

	fmt.Fprintf(os.Stderr, "  [warm-cache] Done in %v\n", time.Since(start))
}

// FormatExecutionResult returns a human-readable summary of an
// ExecutionResult, suitable for printing to stdout.
func FormatExecutionResult(er ExecutionResult) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Mode: %s | Workers: %d | Makespan: %v\n",
		er.Mode, er.Workers, er.Makespan)
	fmt.Fprintf(&sb, "Total elapsed (sum): %v\n\n", er.TotalElapsed)

	fmt.Fprintf(&sb, "%-8s | %8s | %12s | %s\n",
		"Worker", "Packages", "Elapsed", "Error")
	fmt.Fprintf(&sb, "---------|----------|--------------|------\n")

	for _, wr := range er.WorkerResults {
		errStr := ""
		if wr.Error != nil {
			errStr = wr.Error.Error()
		}
		fmt.Fprintf(&sb, "%-8d | %8d | %12v | %s\n",
			wr.WorkerID, wr.PackageCount, wr.Elapsed, errStr)
	}

	return sb.String()
}
