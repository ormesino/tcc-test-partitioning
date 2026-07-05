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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tcc-test-partitioning/internal/model"
)

const (
	CanonicalGOMAXPROCS = 1
	GOMAXPROCSPolicy    = "explicit-child-environment"
)

// Hook for testing cold cache fallback
var mkdirTemp = os.MkdirTemp
var removeAll = os.RemoveAll
var runGoTestCommand = runGoTest

// BaselineReport is the persisted form of a baseline measurement
// (sequential or native-parallel). It is written by the baseline
// modes and consumed by partitioning runs to obtain a methodologically
// sound T1 for speedup computation (ADR-011).
type BaselineReport struct {
	Mode                 string        `json:"mode"`        // "baseline-seq" or "baseline-par"
	Parallelism          int           `json:"parallelism"` // p for baseline-par; 1 for baseline-seq
	Duration             time.Duration `json:"duration_ns"` // wall-clock, in nanoseconds
	MeasuredAt           time.Time     `json:"measured_at"`
	ProjectPath          string        `json:"project_path"`
	PackageCount         int           `json:"package_count,omitempty"`
	PackageSource        string        `json:"package_source,omitempty"` // "./..." or a PackageInfo JSON path
	Success              bool          `json:"success"`
	Error                string        `json:"error,omitempty"`
	DataFileSHA256       string        `json:"data_file_sha256,omitempty"`
	CacheRegime          string        `json:"cache_regime,omitempty"`
	GOMAXPROCSConfigured int           `json:"gomaxprocs_configured,omitempty"`
	GOMAXPROCSEffective  int           `json:"gomaxprocs_effective,omitempty"`
	GOMAXPROCSPolicy     string        `json:"gomaxprocs_policy,omitempty"`
}

// WriteBaselineReport serializes the report to path as indented JSON.
// Publication is atomic and refuses to overwrite an existing report.
func WriteBaselineReport(path string, r BaselineReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary baseline: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("set temporary baseline permissions: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary baseline: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary baseline: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary baseline: %w", err)
	}

	// Linking publishes only a fully written file and fails atomically when the
	// destination already exists. The baseline collection script stages reports
	// under unique names before replacing canonical artifacts with a backup.
	if err := os.Link(tmpPath, path); err != nil {
		return fmt.Errorf("publish baseline without overwrite: %w", err)
	}
	return nil
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

	// executionStarted and executionFinished delimit only the measured go test
	// process. Cold-cache preparation and cleanup are intentionally excluded.
	executionStarted  time.Time
	executionFinished time.Time
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

	// Makespan is the wall-clock interval from the start of the first measured
	// go test process to the end of the last. Cache setup/cleanup is excluded.
	Makespan time.Duration

	// TotalElapsed is the sum of all workers' Elapsed times.
	// This approximates T1 when workers=1.
	TotalElapsed time.Duration
}

// Config holds execution parameters.
type Config struct {
	// ProjectPath is the root directory of the Go project under test.
	ProjectPath string

	// Timeout is the maximum duration of each go test command. In a
	// partitioned repetition, all worker commands receive the same limit and
	// start concurrently, bounding that repetition rather than the campaign.
	// Zero means no timeout.
	Timeout time.Duration

	// Count is the -count flag for go test (default: 1, per ADR-008).
	Count int

	// Verbose enables -v flag on go test.
	Verbose bool

	// WarmCache, when false, forces runWorker to use an isolated GOCACHE.
	WarmCache bool

	// GOMAXPROCS is exported to each child go test process. Zero selects the
	// canonical value (1). Non-canonical values exist only for workerdiag.
	GOMAXPROCS int

	// InheritGOMAXPROCSForDiagnostic bypasses the canonical policy exclusively
	// for the completed, non-canonical worker-semantics diagnostic.
	InheritGOMAXPROCSForDiagnostic bool
}

// RunPartitioned executes go test for each partition in parallel,
// one goroutine per worker, and measures wall-clock time.
//
// Warm-cache preparation, when desired, is performed by the caller before this
// function starts measuring the partitioned execution.
func RunPartitioned(cfg Config, partResult model.PartitionResult) ExecutionResult {
	workers := len(partResult.Partitions)
	resultCh := make(chan WorkerResult, workers)
	var wg sync.WaitGroup

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
	var firstExecutionStart time.Time
	var lastExecutionFinish time.Time
	for wr := range resultCh {
		workerResults[wr.WorkerID] = wr
		totalElapsed += wr.Elapsed
		if !wr.executionStarted.IsZero() &&
			(firstExecutionStart.IsZero() || wr.executionStarted.Before(firstExecutionStart)) {
			firstExecutionStart = wr.executionStarted
		}
		if wr.executionFinished.After(lastExecutionFinish) {
			lastExecutionFinish = wr.executionFinished
		}
	}

	var makespan time.Duration
	if !firstExecutionStart.IsZero() && !lastExecutionFinish.IsZero() {
		makespan = lastExecutionFinish.Sub(firstExecutionStart)
	}

	return ExecutionResult{
		Mode:          "partitioned",
		Workers:       workers,
		WorkerResults: workerResults,
		Makespan:      makespan,
		TotalElapsed:  totalElapsed,
	}
}

// RunBaselineSeq executes go test sequentially (-p 1 -parallel 1)
// over ./... to measure T1 for speedup calculation.
func RunBaselineSeq(cfg Config) ExecutionResult {
	return RunBaselineSeqPackages(cfg, nil)
}

// RunBaselineSeqPackages executes go test sequentially (-p 1 -parallel 1)
// over an explicit package list. When packages is empty, it falls back to ./...
// for backward compatibility.
func RunBaselineSeqPackages(cfg Config, packages []string) ExecutionResult {
	// Build command: go test -p 1 -parallel 1 -count=1 <packages|./...>
	args := []string{"test", "-p", "1", "-parallel", "1",
		"-count", fmt.Sprintf("%d", cfg.Count)}
	if cfg.Timeout > 0 {
		args = append(args, "-timeout", fmt.Sprintf("%dm", int(cfg.Timeout.Minutes())))
	}
	if cfg.Verbose {
		args = append(args, "-v")
	}
	args = appendPackageArgs(args, packages)

	wr := runTimedGoTest(cfg, args, 0, packageCount(packages), "tcc-baseline-seq-*")

	return ExecutionResult{
		Mode:          "baseline-seq",
		Workers:       1,
		WorkerResults: []WorkerResult{wr},
		Makespan:      wr.Elapsed,
		TotalElapsed:  wr.Elapsed,
	}
}

// RunBaselinePar executes go test with native parallelism (-p P)
// over ./... for direct comparison with partitioning algorithms at the same
// level of parallelism.
func RunBaselinePar(cfg Config, parallelism int) ExecutionResult {
	return RunBaselineParPackages(cfg, parallelism, nil)
}

// RunBaselineParPackages executes go test with native parallelism (-p P)
// over an explicit package list. When packages is empty, it falls back to ./...
// for backward compatibility.
func RunBaselineParPackages(cfg Config, parallelism int, packages []string) ExecutionResult {
	// Build command: go test -p P -parallel 1 -count=1 <packages|./...>
	args := []string{"test", "-p", fmt.Sprintf("%d", parallelism),
		"-parallel", "1",
		"-count", fmt.Sprintf("%d", cfg.Count)}
	if cfg.Timeout > 0 {
		args = append(args, "-timeout", fmt.Sprintf("%dm", int(cfg.Timeout.Minutes())))
	}
	if cfg.Verbose {
		args = append(args, "-v")
	}
	args = appendPackageArgs(args, packages)

	wr := runTimedGoTest(cfg, args, 0, packageCount(packages), "tcc-baseline-par-*")

	return ExecutionResult{
		Mode:          "baseline-par",
		Workers:       parallelism,
		WorkerResults: []WorkerResult{wr},
		Makespan:      wr.Elapsed,
		TotalElapsed:  wr.Elapsed,
	}
}

func appendPackageArgs(args []string, packages []string) []string {
	if len(packages) == 0 {
		return append(args, "./...")
	}
	return append(args, packages...)
}

func packageCount(packages []string) int {
	if len(packages) == 0 {
		return 0
	}
	return len(packages)
}

// runWorker executes go test for a single partition and returns
// the result with wall-clock timing.
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
	// These flags serialize packages within this command and tests that call
	// t.Parallel. They do not constrain ordinary goroutines or guarantee a
	// single CPU for the whole test binary; that materiality is evaluated by
	// cmd/workerdiag before any canonical GOMAXPROCS policy is changed.
	args := []string{"test", "-p", "1", "-parallel", "1", "-count", fmt.Sprintf("%d", cfg.Count)}
	if cfg.Timeout > 0 {
		args = append(args, "-timeout", fmt.Sprintf("%dm", int(cfg.Timeout.Minutes())))
	}
	if cfg.Verbose {
		args = append(args, "-v")
	}
	args = append(args, pkgPaths...)

	return runTimedGoTest(
		cfg,
		args,
		partition.WorkerID,
		len(partition.Packages),
		fmt.Sprintf("tcc-worker-%d-*", partition.WorkerID),
	)
}

// runTimedGoTest prepares the cache regime, measures only the go test process,
// and performs cleanup after the measured region. Cold runs always receive a
// fresh isolated GOCACHE; warm runs inherit the cache populated by the caller.
func runTimedGoTest(cfg Config, args []string, workerID, packageCount int, tempPattern string) WorkerResult {
	env := childEnvironment(cfg, os.Environ())
	var coldCacheDir string
	if !cfg.WarmCache {
		tempDir, err := mkdirTemp("", tempPattern)
		if err != nil {
			return WorkerResult{
				WorkerID:     workerID,
				PackageCount: packageCount,
				Error:        fmt.Errorf("failed to create cold cache: %w", err),
			}
		}
		coldCacheDir = tempDir
		env = withEnvValue(env, "GOCACHE", tempDir)
	}

	started := time.Now()
	output, err := runGoTestCommand(cfg, args, env)
	finished := time.Now()
	elapsed := finished.Sub(started)
	if coldCacheDir != "" {
		if cleanupErr := removeAll(coldCacheDir); cleanupErr != nil {
			if err != nil {
				err = fmt.Errorf("go test failed: %v; failed to remove cold cache: %w", err, cleanupErr)
			} else {
				err = fmt.Errorf("failed to remove cold cache: %w", cleanupErr)
			}
		}
	}

	return WorkerResult{
		WorkerID:          workerID,
		Elapsed:           elapsed,
		PackageCount:      packageCount,
		Error:             err,
		Output:            output,
		executionStarted:  started,
		executionFinished: finished,
	}
}

func childEnvironment(cfg Config, environ []string) []string {
	if cfg.InheritGOMAXPROCSForDiagnostic {
		return append([]string(nil), environ...)
	}
	value := cfg.GOMAXPROCS
	if value == 0 {
		value = CanonicalGOMAXPROCS
	}
	return withEnvValue(environ, "GOMAXPROCS", strconv.Itoa(value))
}

// CanonicalEnvironment preserves the inherited environment, removes every
// GOMAXPROCS definition, and appends exactly the canonical value.
func CanonicalEnvironment(environ []string) []string {
	return childEnvironment(Config{}, environ)
}

// withEnvValue replaces all inherited definitions of key and appends exactly
// one canonical value. EqualFold is required because environment keys are
// case-insensitive on Windows.
func withEnvValue(environ []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, key) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+value)
}

// runGoTest executes a go test command with the given arguments
// and returns combined output. Respects cfg.Timeout.
func runGoTest(cfg Config, args []string, env []string) (string, error) {
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
	if len(env) > 0 {
		cmd.Env = env
	}

	out, err := cmd.CombinedOutput()
	// CommandContext waits for the child process to terminate. Checking the
	// context only after CombinedOutput returns avoids starting cleanup or a
	// retry while the previous go test process is still shutting down.
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("timeout or context canceled: %w", ctx.Err())
	}
	return string(out), err
}

// WarmBuildCache requests compilation of the selected test packages without
// intentionally running named tests. It uses `-run=^$` and leaves Go free to
// reuse the resulting build-cache artifacts in later measurements.
//
// This warm-up reduces reusable compilation work, but it does not claim that
// every later `go test` invocation performs zero build, link, initialization,
// or package setup work. It approximates a CI environment with a pre-populated
// build cache.
func WarmBuildCache(cfg Config) error {
	return WarmBuildCachePackages(cfg, nil)
}

// WarmBuildCachePackages pre-compiles test binaries for an explicit package
// list. When packages is empty, it falls back to ./...
func WarmBuildCachePackages(cfg Config, packages []string) error {
	fmt.Fprintf(os.Stderr, "  [warm-cache] Pre-compiling test binaries for %s...\n", cfg.ProjectPath)
	start := time.Now()

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	// -run=^$ matches no named test. Package initialization or framework-level
	// setup may still occur. -count=1 avoids relying on cached test results while
	// allowing reusable build artifacts to populate GOCACHE. The default -p is
	// retained so the warm-up itself is not part of the measured region.
	args := appendPackageArgs([]string{"test", "-run=^$", "-count=1"}, packages)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = cfg.ProjectPath
	cmd.Env = childEnvironment(cfg, os.Environ())
	cmd.Stdout = os.Stderr // Show compilation progress on stderr.
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("warm-cache pre-compilation failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  [warm-cache] Done in %v\n", time.Since(start))
	return nil
}

// VerifyCanonicalGOMAXPROCS compiles and runs a minimal Go child under the
// canonical child environment and returns the runtime value it observed.
func VerifyCanonicalGOMAXPROCS(ctx context.Context) (int, error) {
	dir, err := os.MkdirTemp("", "tcc-gomaxprocs-preflight-*")
	if err != nil {
		return 0, fmt.Errorf("create GOMAXPROCS preflight directory: %w", err)
	}
	defer os.RemoveAll(dir)

	source := filepath.Join(dir, "main.go")
	program := []byte("package main\nimport (\"fmt\"; \"runtime\")\nfunc main(){fmt.Print(runtime.GOMAXPROCS(0))}\n")
	if err := os.WriteFile(source, program, 0o644); err != nil {
		return 0, fmt.Errorf("write GOMAXPROCS preflight: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "run", source)
	cmd.Env = CanonicalEnvironment(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("run GOMAXPROCS preflight: %w: %s", err, strings.TrimSpace(string(out)))
	}
	effective, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse GOMAXPROCS preflight output %q: %w", out, err)
	}
	if effective != CanonicalGOMAXPROCS {
		return effective, fmt.Errorf("GOMAXPROCS preflight observed %d, expected %d", effective, CanonicalGOMAXPROCS)
	}
	return effective, nil
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
