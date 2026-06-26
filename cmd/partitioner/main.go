// Command partitioner is the main CLI tool for partitioning Go test
// suites across parallel workers and measuring execution performance.
//
// Usage:
//
//	go run cmd/partitioner/main.go [flags]
//
// Modes:
//
//	simulate      Use pre-collected durations (JSON) to compute
//	              theoretical metrics without executing go test.
//	run           Execute go test on the partitioned packages.
//	baseline-seq  Run go test -p 1 (sequential baseline for T1).
//	baseline-par  Run go test -p P (native parallelism baseline).
//
// Examples:
//
//	# Simulate all algorithms on collected data
//	go run cmd/partitioner/main.go --mode simulate --data-file data/characterization/cli.json --algorithm all --workers 4
//
//	# Run LPT on a real project
//	go run cmd/partitioner/main.go --mode run --project-path /tmp/cli --algorithm lpt --workers 4
//
//	# Sequential baseline
//	go run cmd/partitioner/main.go --mode baseline-seq --project-path /tmp/cli
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"tcc-test-partitioning/internal/executor"
	"tcc-test-partitioning/internal/metrics"
	"tcc-test-partitioning/internal/model"
	"tcc-test-partitioning/internal/partitioner"
)

func main() {
	// Define CLI flags.
	algorithm := flag.String("algorithm", "all",
		"Partitioning algorithm: round-robin, quantity, lpt, ffd, all")
	workers := flag.Int("workers", 4,
		"Number of parallel workers (machines)")
	mode := flag.String("mode", "simulate",
		"Execution mode: simulate, run, baseline-seq, baseline-par")
	dataFile := flag.String("data-file", "",
		"Path to JSON file with pre-collected package durations (simulate/run; optional pass-only scope for baseline modes)")
	projectPath := flag.String("project-path", "",
		"Path to Go project root (for run/baseline modes)")
	timeout := flag.Int("timeout", 30,
		"Timeout in minutes for go test execution")
	verbose := flag.Bool("verbose", false,
		"Enable verbose output from go test (-v flag)")
	baselineSeqFile := flag.String("baseline-seq-file", "",
		"Path to a BaselineReport JSON (written by --mode baseline-seq --output). "+
			"Used as T1 for speedup in --mode run. Without it, T1 is approximated by sum(Duration).")
	output := flag.String("output", "",
		"Path to write the BaselineReport JSON in --mode baseline-seq / baseline-par.")
	outputJSON := flag.String("output-json", "",
		"Path to write a structured JSON report in --mode simulate / run.")
	listPackages := flag.Bool("list-packages", false,
		"Include the full package list per partition in --output-json (default: omit).")
	warmCache := flag.Bool("warm-cache", false,
		"Pre-compile test binaries before running to separate compilation cost (run/baseline modes).")

	flag.Parse()

	// Validate flags.
	switch *mode {
	case "simulate":
		if *dataFile == "" {
			fmt.Fprintln(os.Stderr, "Error: --data-file is required for simulate mode")
			flag.Usage()
			os.Exit(1)
		}
	case "run":
		if *projectPath == "" {
			fmt.Fprintln(os.Stderr, "Error: --project-path is required for run mode")
			flag.Usage()
			os.Exit(1)
		}
		if *dataFile == "" {
			fmt.Fprintln(os.Stderr, "Error: --data-file is required for run mode (need durations for partitioning)")
			flag.Usage()
			os.Exit(1)
		}
	case "baseline-seq", "baseline-par":
		if *projectPath == "" {
			fmt.Fprintln(os.Stderr, "Error: --project-path is required for baseline modes")
			flag.Usage()
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown mode %q\n", *mode)
		flag.Usage()
		os.Exit(1)
	}

	// Dispatch based on mode.
	switch *mode {
	case "simulate":
		runSimulate(*dataFile, *algorithm, *workers, *baselineSeqFile, *outputJSON, *listPackages)
	case "run":
		runExecution(*dataFile, *projectPath, *algorithm, *workers, *timeout, *verbose, *warmCache, *baselineSeqFile, *outputJSON, *listPackages)
	case "baseline-seq":
		runBaselineSeq(*projectPath, *dataFile, *timeout, *verbose, *warmCache, *output)
	case "baseline-par":
		runBaselinePar(*projectPath, *dataFile, *workers, *timeout, *verbose, *warmCache, *output)
	}
}

// resolveAlgorithms returns the list of Partitioner implementations
// matching the algorithm flag value.
func resolveAlgorithms(name string) []partitioner.Partitioner {
	switch strings.ToLower(name) {
	case "round-robin", "roundrobin", "rr":
		return []partitioner.Partitioner{&partitioner.RoundRobin{}}
	case "quantity", "qty":
		return []partitioner.Partitioner{&partitioner.Quantity{}}
	case "lpt":
		return []partitioner.Partitioner{&partitioner.LPT{}}
	case "ffd", "ffd-weighted":
		return []partitioner.Partitioner{&partitioner.FFD{}}
	case "all":
		return []partitioner.Partitioner{
			&partitioner.RoundRobin{},
			&partitioner.Quantity{},
			&partitioner.LPT{},
			&partitioner.FFD{},
		}
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown algorithm %q\n", name)
		fmt.Fprintln(os.Stderr, "Valid options: round-robin, quantity, lpt, ffd, all")
		os.Exit(1)
		return nil
	}
}

// loadPackages reads a JSON file containing an array of PackageInfo
// objects in the canonical convention (ADR-014):
//
//	[
//	  {"name": "pkg/path", "duration_ns": 1500000000, "cv": 0.15},
//	  ...
//	]
//
// duration_ns is time.Duration serialized as int64 (nanoseconds).
func loadPackages(path string) ([]model.PackageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading data file: %w", err)
	}

	var packages []model.PackageInfo
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("parsing data file: %w", err)
	}

	return packages, nil
}

// runSimulate loads pre-collected durations and computes theoretical
// metrics without executing go test. This is the primary mode when
// Go is not installed or for rapid iteration.
//
// Each algorithm's Partition() is invoked exactly once per call;
// the resulting PartitionResult is cached locally and reused for
// every output (summary table, per-worker detail, JSON report).
// No state crosses different invocations or different algorithms.
func runSimulate(dataFile, algName string, workers int, baselineSeqFile, outputJSON string, listPackages bool) {
	packages, err := loadPackages(dataFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	algorithms := resolveAlgorithms(algName)

	seqDuration, seqSource := resolveT1(packages, baselineSeqFile)
	ideal := seqDuration / time.Duration(workers)

	fmt.Println("================================================================")
	fmt.Printf("  Mode: simulate | Data: %s\n", dataFile)
	fmt.Printf("  Packages: %d | Workers: %d\n", len(packages), workers)
	fmt.Printf("  T1 (%s): %v\n", seqSource, seqDuration)
	fmt.Printf("  Ideal makespan (T1/%d): %v\n", workers, ideal)
	fmt.Println("================================================================")
	fmt.Println()

	// Compute once per algorithm and cache. The cache is local to
	// this invocation and indexed by algorithm position; it is never
	// reused across modes, datasets, or worker counts.
	results := make([]model.PartitionResult, len(algorithms))
	for i, alg := range algorithms {
		results[i] = alg.Partition(packages, workers)
	}

	// Summary table.
	fmt.Printf("%-14s | %12s | %12s | %7s | %7s | %10s | %10s\n",
		"Algorithm", "Makespan", "Ideal", "S(p)", "E(p)", "StdDev(s)", "Overhead")
	fmt.Println("---------------|--------------|--------------|---------|---------|------------|----------")

	for _, r := range results {
		report := metrics.Compute(r, seqDuration)
		stddev := metrics.LoadStdDev(r)

		fmt.Printf("%-14s | %12v | %12v | %7.2f | %7.2f | %10.4f | %10v\n",
			report.Algorithm,
			report.Makespan,
			ideal,
			report.Speedup,
			report.Efficiency,
			stddev,
			report.Overhead,
		)
	}
	fmt.Println()

	// Per-worker detail (reuses the cached results — no re-partitioning).
	for _, r := range results {
		fmt.Printf("  [%s] Per-worker detail:\n", r.Algorithm)
		fmt.Printf("  %-8s | %8s | %12s\n", "Worker", "Packages", "Load")
		fmt.Printf("  ---------|----------|------------\n")
		for _, p := range r.Partitions {
			fmt.Printf("  %-8d | %8d | %12v\n", p.WorkerID, len(p.Packages), p.Load)
		}
		fmt.Println()
	}

	if outputJSON != "" {
		entries := make([]algEntry, len(results))
		for i, r := range results {
			entries[i] = buildPlannedEntry(r, seqDuration, listPackages)
		}
		rep := outputReport{
			Mode:         "simulate",
			DataFile:     dataFile,
			Workers:      workers,
			PackageCount: len(packages),
			T1NS:         int64(seqDuration),
			T1Source:     seqSource,
			GeneratedAt:  time.Now(),
			Algorithms:   entries,
		}
		if err := writeOutputReport(outputJSON, rep); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("JSON report written to %s\n", outputJSON)
	}
}

// runExecution loads durations, partitions, then executes go test
// on each partition and reports real metrics.
//
// T1 (sequential baseline) is taken from baselineSeqFile when
// provided; otherwise it falls back to sum(Duration), which
// over-estimates Speedup because it ignores per-package go test
// setup/build cost. A warning is emitted in that case.
//
// Each algorithm's Partition() is called once and the result is
// reused for both the human-readable text and the JSON report.
func runExecution(dataFile, projectPath, algName string, workers, timeoutMin int, verbose, warmCache bool, baselineSeqFile, outputJSON string, listPackages bool) {
	packages, err := loadPackages(dataFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	algorithms := resolveAlgorithms(algName)
	cfg := executor.Config{
		ProjectPath: projectPath,
		Timeout:     time.Duration(timeoutMin) * time.Minute,
		Count:       1,
		Verbose:     verbose,
	}

	if warmCache {
		executor.WarmBuildCachePackages(cfg, packageNames(packages))
	}

	seqDuration, seqSource := resolveT1(packages, baselineSeqFile)
	fmt.Printf("T1 source: %s | T1 = %v\n\n", seqSource, seqDuration)

	entries := make([]algEntry, 0, len(algorithms))

	for _, alg := range algorithms {
		fmt.Printf("=== Running %s with %d workers ===\n\n", alg.Name(), workers)

		// Step 1: Partition (computed once, reused for both text and JSON).
		partResult := alg.Partition(packages, workers)
		fmt.Printf("Partitioning overhead: %v\n", partResult.Overhead)

		// Step 2: Execute.
		execResult := executor.RunPartitioned(cfg, partResult)

		// Step 3: Report (text).
		fmt.Println(executor.FormatExecutionResult(execResult))

		// Step 4: Build real metrics from measured execution times.
		realPartitions := make([]model.Partition, len(execResult.WorkerResults))
		for i, wr := range execResult.WorkerResults {
			realPartitions[i] = model.Partition{
				WorkerID: wr.WorkerID,
				Packages: partResult.Partitions[i].Packages,
				Load:     wr.Elapsed,
			}
		}
		realResult := model.PartitionResult{
			Algorithm:  alg.Name(),
			Workers:    workers,
			Partitions: realPartitions,
			Makespan:   execResult.Makespan,
			Overhead:   partResult.Overhead,
		}

		report := metrics.Compute(realResult, seqDuration)
		fmt.Printf("Metrics (real execution):\n")
		fmt.Printf("  Makespan:   %v\n", report.Makespan)
		fmt.Printf("  Speedup:    %.2f\n", report.Speedup)
		fmt.Printf("  Efficiency: %.2f\n", report.Efficiency)
		fmt.Printf("  Load StdDev: %.4f s\n", metrics.LoadStdDev(realResult))
		fmt.Println()

		if outputJSON != "" {
			entry := buildPlannedEntry(partResult, seqDuration, listPackages)
			attachExecution(&entry, partResult, execResult, seqDuration)
			entries = append(entries, entry)
		}
	}

	if outputJSON != "" {
		rep := outputReport{
			Mode:         "run",
			DataFile:     dataFile,
			ProjectPath:  projectPath,
			Workers:      workers,
			PackageCount: len(packages),
			T1NS:         int64(seqDuration),
			T1Source:     seqSource,
			GeneratedAt:  time.Now(),
			Algorithms:   entries,
		}
		if err := writeOutputReport(outputJSON, rep); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("JSON report written to %s\n", outputJSON)
	}
}

// runBaselineSeq executes go test in sequential mode (-p 1). When dataFile is
// provided, it uses exactly the packages present in the characterization JSON
// instead of ./..., producing a pass-only baseline comparable to partitioned runs.
// When output is non-empty, the wall-clock T1 is persisted as a BaselineReport
// JSON for later reuse by --mode run.
func runBaselineSeq(projectPath, dataFile string, timeoutMin int, verbose, warmCache bool, output string) {
	cfg := executor.Config{
		ProjectPath: projectPath,
		Timeout:     time.Duration(timeoutMin) * time.Minute,
		Count:       1,
		Verbose:     verbose,
	}

	packages, packageSource := loadBaselinePackageScope(dataFile)

	if warmCache {
		executor.WarmBuildCachePackages(cfg, packages)
	}

	fmt.Println("=== Baseline Sequential (go test -p 1 -parallel 1) ===")
	fmt.Printf("Package scope: %s", packageSource)
	if len(packages) > 0 {
		fmt.Printf(" (%d packages)", len(packages))
	}
	fmt.Println()
	fmt.Println()

	result := executor.RunBaselineSeqPackages(cfg, packages)
	fmt.Println(executor.FormatExecutionResult(result))

	if output != "" {
		wr := result.WorkerResults[0]
		writeBaselineReport(output, executor.BaselineReport{
			Mode:          "baseline-seq",
			Parallelism:   1,
			Duration:      result.Makespan,
			MeasuredAt:    time.Now(),
			ProjectPath:   projectPath,
			PackageCount:  wr.PackageCount,
			PackageSource: packageSource,
			Success:       wr.Error == nil,
			Error:         errorString(wr.Error),
		})
	}
}

// runBaselinePar executes go test with native parallelism (-p P). When dataFile
// is provided, it uses exactly the packages present in the characterization JSON
// instead of ./..., producing a pass-only native baseline.
// When output is non-empty, the wall-clock is persisted as a BaselineReport JSON.
func runBaselinePar(projectPath, dataFile string, workers, timeoutMin int, verbose, warmCache bool, output string) {
	cfg := executor.Config{
		ProjectPath: projectPath,
		Timeout:     time.Duration(timeoutMin) * time.Minute,
		Count:       1,
		Verbose:     verbose,
	}

	packages, packageSource := loadBaselinePackageScope(dataFile)

	if warmCache {
		executor.WarmBuildCachePackages(cfg, packages)
	}

	fmt.Printf("=== Baseline Parallel (go test -p %d -parallel 1) ===\n", workers)
	fmt.Printf("Package scope: %s", packageSource)
	if len(packages) > 0 {
		fmt.Printf(" (%d packages)", len(packages))
	}
	fmt.Println()
	fmt.Println()

	result := executor.RunBaselineParPackages(cfg, workers, packages)
	fmt.Println(executor.FormatExecutionResult(result))

	if output != "" {
		wr := result.WorkerResults[0]
		writeBaselineReport(output, executor.BaselineReport{
			Mode:          "baseline-par",
			Parallelism:   workers,
			Duration:      result.Makespan,
			MeasuredAt:    time.Now(),
			ProjectPath:   projectPath,
			PackageCount:  wr.PackageCount,
			PackageSource: packageSource,
			Success:       wr.Error == nil,
			Error:         errorString(wr.Error),
		})
	}
}

func loadBaselinePackageScope(dataFile string) ([]string, string) {
	if dataFile == "" {
		return nil, "./..."
	}
	packages, err := loadPackages(dataFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(packages) == 0 {
		fmt.Fprintf(os.Stderr, "Error: --data-file %s has no packages\n", dataFile)
		os.Exit(1)
	}
	return packageNames(packages), dataFile
}

func packageNames(packages []model.PackageInfo) []string {
	names := make([]string, len(packages))
	for i, pkg := range packages {
		names[i] = pkg.Name
	}
	return names
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// resolveT1 returns the canonical sequential baseline used in
// speedup computations and a label describing where it came from.
//
// Preference order:
//  1. BaselineReport JSON at baselineSeqFile (methodologically sound).
//  2. sum(packages.Duration) (approximation; emits a stderr warning).
func resolveT1(packages []model.PackageInfo, baselineSeqFile string) (time.Duration, string) {
	if baselineSeqFile != "" {
		r, err := executor.LoadBaselineReport(baselineSeqFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading baseline file: %v\n", err)
			os.Exit(1)
		}
		if err := validateBaselineReport(baselineSeqFile, r); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return r.Duration, fmt.Sprintf("measured (%s)", baselineSeqFile)
	}

	var sum time.Duration
	for _, p := range packages {
		sum += p.Duration
	}
	fmt.Fprintln(os.Stderr,
		"WARN: --baseline-seq-file not provided. T1 = sum(Duration) is an\n"+
			"      optimistic approximation (ignores go test setup, build, I/O).\n"+
			"      Reported Speedup will be biased upward. Run --mode baseline-seq\n"+
			"      --output FILE once per project and pass --baseline-seq-file FILE.")
	return sum, "approx (sum of durations)"
}

func validateBaselineReport(path string, r executor.BaselineReport) error {
	if r.Duration <= 0 {
		return fmt.Errorf("baseline file %s has non-positive duration", path)
	}
	if r.Error != "" {
		return fmt.Errorf("baseline file %s records a failed run: %s", path, r.Error)
	}
	if r.PackageCount > 0 && !r.Success {
		return fmt.Errorf("baseline file %s records success=false", path)
	}
	return nil
}

// writeBaselineReport persists a BaselineReport and reports the
// outcome to stdout.
func writeBaselineReport(path string, r executor.BaselineReport) {
	if err := executor.WriteBaselineReport(path, r); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing baseline report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Baseline report written to %s\n", path)
}
