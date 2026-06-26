// Command benchmark drives the experimental matrix described in the
// TCC methodology: a sweep over
//
//	projects × workers × algorithms × repetitions
//
// for either the simulate mode (deterministic, partitioning-only) or
// the run mode (real `go test` execution via the executor package).
//
// Each (project, algorithm, workers, rep) tuple is processed
// independently; no state is shared across tuples beyond the
// immutable inputs (loaded PackageInfo and BaselineReport). Each
// invocation of Partition() and the executor is fresh.
//
// Outputs (under <output_dir>/<timestamp>/):
//
//	config.json     copy of the resolved configuration
//	results.json    full structured report (config + raw + aggregate)
//	raw.csv         one row per rep
//	aggregate.csv   one row per (project, algorithm, workers)
//
// Usage:
//
//	go run cmd/benchmark/main.go --config benchmarks/example-config.json
//	go run cmd/benchmark/main.go --config bench.json --mode run
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tcc-test-partitioning/internal/executor"
	"tcc-test-partitioning/internal/metrics"
	"tcc-test-partitioning/internal/model"
	"tcc-test-partitioning/internal/partitioner"
)

func main() {
	configPath := flag.String("config", "",
		"Path to the JSON config file (required).")
	modeOverride := flag.String("mode", "",
		"Override config.mode (\"simulate\" or \"run\"). Empty keeps config value.")
	outputDirOverride := flag.String("output-dir", "",
		"Override config.output_dir. Empty keeps config value.")
	timeoutMinutesOverride := flag.Int("timeout-minutes", 0,
		"Override config.timeout_minutes. Zero keeps config value.")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: --config is required")
		flag.Usage()
		os.Exit(1)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fatal("config: %v", err)
	}
	if *modeOverride != "" {
		cfg.Mode = *modeOverride
		if err := cfg.validate(); err != nil {
			fatal("config (after --mode override): %v", err)
		}
	}
	if *outputDirOverride != "" {
		cfg.OutputDir = *outputDirOverride
	}
	if *timeoutMinutesOverride > 0 {
		cfg.TimeoutMinutes = *timeoutMinutesOverride
	}

	algorithms, err := resolveAlgorithms(cfg.Algorithms)
	if err != nil {
		fatal("algorithms: %v", err)
	}

	startedAt := time.Now()
	runDir, err := makeRunDir(cfg.OutputDir, startedAt)
	if err != nil {
		fatal("creating run dir: %v", err)
	}

	// Persist the resolved config alongside the results for
	// reproducibility audits.
	if err := writeJSON(filepath.Join(runDir, "config.json"), cfg); err != nil {
		fatal("writing config copy: %v", err)
	}

	fmt.Printf("Benchmark started: %s\n", startedAt.Format(time.RFC3339))
	fmt.Printf("Mode:       %s\n", cfg.Mode)
	fmt.Printf("Projects:   %d\n", len(cfg.Projects))
	fmt.Printf("Workers:    %v\n", cfg.Workers)
	fmt.Printf("Algorithms: %v\n", cfg.Algorithms)
	fmt.Printf("Reps:       %d\n", cfg.Repetitions)
	fmt.Printf("Output dir: %s\n\n", runDir)

	var raw []rawRecord

	for _, proj := range cfg.Projects {
		fmt.Printf("=== Project: %s ===\n", proj.Name)

		packages, err := loadPackages(proj.DataFile)
		if err != nil {
			fatal("project %q: %v", proj.Name, err)
		}
		t1, t1Source := resolveT1(packages, proj.BaselineSeqFile)
		fmt.Printf("  Packages: %d | T1 (%s): %v\n", len(packages), t1Source, t1)

		if cfg.WarmCache {
			executor.WarmBuildCachePackages(executor.Config{
				ProjectPath: proj.ProjectPath,
				Timeout:     time.Duration(cfg.TimeoutMinutes) * time.Minute,
			}, packageNames(packages))
		}

		for _, w := range cfg.Workers {
			for _, alg := range algorithms {
				for rep := 1; rep <= cfg.Repetitions; rep++ {
					rec := runOne(cfg, proj, packages, t1, alg, w, rep)
					raw = append(raw, rec)
					fmt.Printf("  [%s w=%d rep=%d] makespan(planned)=%v overhead=%v\n",
						alg.Name(), w, rep,
						time.Duration(rec.PlannedMakespanNS),
						time.Duration(rec.PartitioningOverheadNS))
				}
			}
		}
		fmt.Println()
	}

	finishedAt := time.Now()
	agg := aggregate(cfg.Mode, raw)

	full := fullReport{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Config:     cfg,
		Raw:        raw,
		Aggregate:  agg,
	}

	if err := writeFullReport(filepath.Join(runDir, "results.json"), full); err != nil {
		fatal("writing results.json: %v", err)
	}
	if err := writeRawCSV(filepath.Join(runDir, "raw.csv"), raw); err != nil {
		fatal("writing raw.csv: %v", err)
	}
	if err := writeAggregateCSV(filepath.Join(runDir, "aggregate.csv"), agg); err != nil {
		fatal("writing aggregate.csv: %v", err)
	}

	fmt.Printf("Finished: %s (elapsed %v)\n",
		finishedAt.Format(time.RFC3339), finishedAt.Sub(startedAt))
	fmt.Printf("Reports written under %s\n", runDir)
}

// runOne executes one (project, algorithm, workers, rep) tuple and
// returns the corresponding raw record. Independent of every other
// tuple: it always calls Partition() fresh, and (in run mode) starts
// a new executor.RunPartitioned.
func runOne(cfg Config, proj ProjectSpec, packages []model.PackageInfo, t1 time.Duration, alg partitioner.Partitioner, workers, rep int) rawRecord {
	partResult := alg.Partition(packages, workers)
	plannedReport := metrics.Compute(partResult, t1)

	rec := rawRecord{
		Project:                proj.Name,
		Mode:                   cfg.Mode,
		Algorithm:              alg.Name(),
		Workers:                workers,
		Rep:                    rep,
		PlannedMakespanNS:      int64(plannedReport.Makespan),
		PlannedSpeedup:         plannedReport.Speedup,
		PlannedEfficiency:      plannedReport.Efficiency,
		PlannedLoadStdDevS:     metrics.LoadStdDev(partResult),
		PartitioningOverheadNS: int64(plannedReport.Overhead),
	}

	if cfg.Mode != "run" {
		return rec
	}

	execCfg := executor.Config{
		ProjectPath: proj.ProjectPath,
		Timeout:     time.Duration(cfg.TimeoutMinutes) * time.Minute,
		Count:       1,
		Verbose:     cfg.Verbose,
	}
	execResult := executor.RunPartitioned(execCfg, partResult)

	// Real metrics: synthesize a PartitionResult whose Loads are
	// measured Elapsed times so the metrics layer can compute Speedup
	// and Efficiency from real wall-clock.
	realParts := make([]model.Partition, len(execResult.WorkerResults))
	for i, wr := range execResult.WorkerResults {
		realParts[i] = model.Partition{
			WorkerID: wr.WorkerID,
			Packages: partResult.Partitions[i].Packages,
			Load:     wr.Elapsed,
		}
	}
	realResult := model.PartitionResult{
		Algorithm:  alg.Name(),
		Workers:    workers,
		Partitions: realParts,
		Makespan:   execResult.Makespan,
		Overhead:   partResult.Overhead,
	}
	realReport := metrics.Compute(realResult, t1)

	makespanNS := int64(execResult.Makespan)
	speedup := realReport.Speedup
	efficiency := realReport.Efficiency
	stddev := metrics.LoadStdDev(realResult)
	rec.ExecMakespanNS = &makespanNS
	rec.ExecSpeedup = &speedup
	rec.ExecEfficiency = &efficiency
	rec.ExecLoadStdDevS = &stddev

	for _, wr := range execResult.WorkerResults {
		if wr.Error != nil {
			rec.ExecError = wr.Error.Error()
			break
		}
	}
	return rec
}

// resolveT1 mirrors cmd/partitioner's resolveT1 but is intentionally
// duplicated here to keep cmd/benchmark dependency-free from the
// CLI binary. Preference order:
//  1. BaselineReport JSON (methodologically sound).
//  2. sum(packages.Duration) (approximation; emits a stderr warning).
func resolveT1(packages []model.PackageInfo, baselineSeqFile string) (time.Duration, string) {
	if baselineSeqFile != "" {
		r, err := executor.LoadBaselineReport(baselineSeqFile)
		if err != nil {
			fatal("loading baseline %q: %v", baselineSeqFile, err)
		}
		if err := validateBaselineReport(baselineSeqFile, r); err != nil {
			fatal("%v", err)
		}
		return r.Duration, fmt.Sprintf("measured (%s)", baselineSeqFile)
	}
	var sum time.Duration
	for _, p := range packages {
		sum += p.Duration
	}
	fmt.Fprintln(os.Stderr,
		"WARN: baseline_seq_file not set for a project. T1 = sum(Duration)\n"+
			"      is an optimistic approximation; reported Speedup is biased upward.")
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

// loadPackages reads a JSON file containing []PackageInfo.
func loadPackages(path string) ([]model.PackageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading data file %s: %w", path, err)
	}
	var packages []model.PackageInfo
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("parsing data file %s: %w", path, err)
	}
	return packages, nil
}

func packageNames(packages []model.PackageInfo) []string {
	names := make([]string, len(packages))
	for i, pkg := range packages {
		names[i] = pkg.Name
	}
	return names
}

// makeRunDir creates "<base>/<YYYYmmdd-HHMMSS>/" and returns its path.
func makeRunDir(base string, t time.Time) (string, error) {
	dir := filepath.Join(base, t.Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
