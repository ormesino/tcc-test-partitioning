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
//	environment.json execution environment and source identities
//	results.json    full structured report (config + raw + aggregate)
//	raw.csv         one row per rep
//	aggregate.csv   one row per (project, algorithm, workers)
//	native_baselines.csv validated Go-native baselines by worker count
//
// Usage:
//
//	go run cmd/benchmark/main.go --config benchmarks/example-config.json
//	go run cmd/benchmark/main.go --config bench.json --mode run
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
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
		"Override the per-repetition config.timeout_minutes. Zero keeps the config value.")
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
	environment := collectEnvironment(cfg)
	if err := writeJSON(filepath.Join(runDir, "environment.json"), environment); err != nil {
		fatal("writing environment manifest: %v", err)
	}

	fmt.Printf("Benchmark started: %s\n", startedAt.Format(time.RFC3339))
	fmt.Printf("Mode:       %s\n", cfg.Mode)
	fmt.Printf("Projects:   %d\n", len(cfg.Projects))
	fmt.Printf("Workers:    %v\n", cfg.Workers)
	fmt.Printf("Algorithms: %v\n", cfg.Algorithms)
	fmt.Printf("Reps:       %d\n", cfg.Repetitions)
	fmt.Printf("Output dir: %s\n\n", runDir)

	var raw []rawRecord
	var nativeBaselines []nativeBaselineRecord
	hadExecutionErrors := false
	totalCombinations := len(cfg.Projects) * cfg.Repetitions * len(cfg.Workers) * len(algorithms)
	combinationIndex := 0
	cacheRegime := "cold"
	if cfg.WarmCache {
		cacheRegime = "warm"
	}
	fmt.Printf("Total combinations: %d\n\n", totalCombinations)

	for _, proj := range cfg.Projects {
		fmt.Printf("=== Project: %s ===\n", proj.Name)

		packages, err := loadPackages(proj.DataFile)
		if err != nil {
			fatal("project %q: %v", proj.Name, err)
		}
		t1, t1Source := resolveT1(packages, proj.BaselineSeqFile, proj.DataFile, cfg.WarmCache)
		theoreticalT1 := sumPackageDurations(packages)
		fmt.Printf("  Packages: %d | T1 (%s): %v\n", len(packages), t1Source, t1)
		if cfg.Mode == "run" {
			records, err := loadNativeBaselines(cfg, proj, packages, t1)
			if err != nil {
				fatal("project %q: %v", proj.Name, err)
			}
			nativeBaselines = append(nativeBaselines, records...)
		}

		if cfg.WarmCache {
			if err := executor.WarmBuildCachePackages(executor.Config{
				ProjectPath: proj.ProjectPath,
				Timeout:     time.Duration(cfg.TimeoutMinutes) * time.Minute,
			}, packageNames(packages)); err != nil {
				fatal("warm cache for project %q failed: %v", proj.Name, err)
			}
		}

		for rep := 1; rep <= cfg.Repetitions; rep++ {
			for _, w := range cfg.Workers {
				for _, alg := range algorithms {
					combinationIndex++
					combinationStarted := time.Now()
					fmt.Printf("[%s] START combination %d/%d | project=%s regime=%s rep=%d/%d workers=%d algorithm=%s timeout=%dm\n",
						combinationStarted.Format(time.RFC3339), combinationIndex, totalCombinations,
						proj.Name, cacheRegime, rep, cfg.Repetitions, w, alg.Name(), cfg.TimeoutMinutes)
					rec := runOne(cfg, proj, packages, theoreticalT1, t1, alg, w, rep)
					raw = append(raw, rec)
					status := "success"
					if rec.ExecError != "" {
						hadExecutionErrors = true
						status = "error: " + rec.ExecError
					}
					fmt.Printf("[%s] END   combination %d/%d | status=%s elapsed=%v planned_makespan=%v overhead=%v\n\n",
						time.Now().Format(time.RFC3339), combinationIndex, totalCombinations,
						status, time.Since(combinationStarted).Round(time.Millisecond),
						time.Duration(rec.PlannedMakespanNS),
						time.Duration(rec.PartitioningOverheadNS))
				}
			}
		}
		fmt.Printf("=== Project completed: %s (%d/%d combinations finished) ===\n\n", proj.Name, combinationIndex, totalCombinations)
	}

	finishedAt := time.Now()
	agg := aggregate(cfg.Mode, raw)

	full := fullReport{
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		Config:          cfg,
		Environment:     environment,
		NativeBaselines: nativeBaselines,
		Raw:             raw,
		Aggregate:       agg,
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
	if err := writeNativeBaselineCSV(filepath.Join(runDir, "native_baselines.csv"), nativeBaselines); err != nil {
		fatal("writing native_baselines.csv: %v", err)
	}

	fmt.Printf("Finished: %s (elapsed %v)\n",
		finishedAt.Format(time.RFC3339), finishedAt.Sub(startedAt))
	fmt.Printf("Reports written under %s\n", runDir)
	if hadExecutionErrors {
		fatal("one or more benchmark executions failed; reports were preserved for diagnosis")
	}
}

// runOne executes one (project, algorithm, workers, rep) tuple and
// returns the corresponding raw record. Independent of every other
// tuple: it always calls Partition() fresh, and (in run mode) starts
// a new executor.RunPartitioned.
func runOne(cfg Config, proj ProjectSpec, packages []model.PackageInfo, theoreticalT1, measuredT1 time.Duration, alg partitioner.Partitioner, workers, rep int) rawRecord {
	partResult := alg.Partition(packages, workers)
	plannedReport := metrics.Compute(partResult, theoreticalT1)

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
		WarmCache:   cfg.WarmCache,
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
	realReport := metrics.Compute(realResult, measuredT1)

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

func sumPackageDurations(packages []model.PackageInfo) time.Duration {
	var total time.Duration
	for _, pkg := range packages {
		total += pkg.Duration
	}
	return total
}

func loadNativeBaselines(cfg Config, proj ProjectSpec, packages []model.PackageInfo, sequential time.Duration) ([]nativeBaselineRecord, error) {
	records := make([]nativeBaselineRecord, 0, len(cfg.Workers))
	for _, workers := range cfg.Workers {
		path := proj.BaselineParFiles[strconv.Itoa(workers)]
		report, err := executor.LoadBaselineReport(path)
		if err != nil {
			return nil, fmt.Errorf("loading native baseline %q: %w", path, err)
		}
		if err := validateParallelBaselineReport(path, report, len(packages), proj.DataFile, cfg.WarmCache, workers); err != nil {
			return nil, err
		}
		speedup := float64(sequential) / float64(report.Duration)
		records = append(records, nativeBaselineRecord{
			Project:        proj.Name,
			Workers:        workers,
			DurationNS:     int64(report.Duration),
			Speedup:        speedup,
			Efficiency:     speedup / float64(workers),
			BaselineFile:   path,
			CacheRegime:    report.CacheRegime,
			PackageCount:   report.PackageCount,
			DataFileSHA256: report.DataFileSHA256,
			MeasuredAt:     report.MeasuredAt,
		})
	}
	return records, nil
}

func validateParallelBaselineReport(path string, r executor.BaselineReport, expectedPackageCount int, dataFile string, warmCache bool, workers int) error {
	if r.Mode != "baseline-par" {
		return fmt.Errorf("baseline file %s has mode %q, expected 'baseline-par'", path, r.Mode)
	}
	if r.Parallelism != workers {
		return fmt.Errorf("baseline file %s has parallelism=%d, expected %d", path, r.Parallelism, workers)
	}
	if r.Duration <= 0 || !r.Success || r.Error != "" {
		return fmt.Errorf("baseline file %s is not a successful positive-duration run", path)
	}
	if r.PackageCount != expectedPackageCount || r.PackageSource == "" || r.PackageSource == "./..." {
		return fmt.Errorf("baseline file %s has an incompatible package population", path)
	}
	if expectedHash := hashFile(dataFile); r.DataFileSHA256 != expectedHash {
		return fmt.Errorf("baseline file %s has data_file_sha256=%q, expected %q", path, r.DataFileSHA256, expectedHash)
	}
	expectedRegime := "cold"
	if warmCache {
		expectedRegime = "warm"
	}
	if r.CacheRegime != expectedRegime {
		return fmt.Errorf("baseline file %s has cache_regime=%q, expected %q", path, r.CacheRegime, expectedRegime)
	}
	return nil
}

// resolveT1 mirrors cmd/partitioner's resolveT1 but is intentionally
// duplicated here to keep cmd/benchmark dependency-free from the
// CLI binary. Preference order:
//  1. BaselineReport JSON (methodologically sound).
//  2. sum(packages.Duration) (approximation; emits a stderr warning).
func resolveT1(packages []model.PackageInfo, baselineSeqFile string, dataFile string, warmCache bool) (time.Duration, string) {
	if baselineSeqFile != "" {
		r, err := executor.LoadBaselineReport(baselineSeqFile)
		if err != nil {
			fatal("loading baseline %q: %v", baselineSeqFile, err)
		}
		if err := validateBaselineReport(baselineSeqFile, r, len(packages), dataFile, warmCache); err != nil {
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

func validateBaselineReport(path string, r executor.BaselineReport, expectedPackageCount int, dataFile string, warmCache bool) error {
	if r.Duration <= 0 {
		return fmt.Errorf("baseline file %s has non-positive duration", path)
	}
	if r.Mode != "baseline-seq" {
		return fmt.Errorf("baseline file %s has mode %q, expected 'baseline-seq'", path, r.Mode)
	}
	if r.Error != "" {
		return fmt.Errorf("baseline file %s records a failed run: %s", path, r.Error)
	}
	if !r.Success {
		return fmt.Errorf("baseline file %s records success=false", path)
	}
	if r.PackageCount != expectedPackageCount {
		return fmt.Errorf("baseline file %s has package_count=%d, expected %d from the data file",
			path, r.PackageCount, expectedPackageCount)
	}
	if r.PackageSource == "" || r.PackageSource == "./..." {
		return fmt.Errorf("baseline file %s is not pass-only (package_source=%q)", path, r.PackageSource)
	}

	expectedHash := hashFile(dataFile)
	if r.DataFileSHA256 != expectedHash {
		return fmt.Errorf("baseline file %s has data_file_sha256=%q, expected %q from current data file", path, r.DataFileSHA256, expectedHash)
	}

	expectedRegime := "cold"
	if warmCache {
		expectedRegime = "warm"
	}
	if r.CacheRegime != expectedRegime {
		return fmt.Errorf("baseline file %s has cache_regime=%q, expected %q", path, r.CacheRegime, expectedRegime)
	}

	return nil
}

func hashFile(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
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
	for _, pkg := range packages {
		if pkg.Duration < 0 {
			return nil, fmt.Errorf("package %q in data file %s has negative duration: %v", pkg.Name, path, pkg.Duration)
		}
		if pkg.Duration == 0 {
			fmt.Fprintf(os.Stderr, "Warning: package %q in data file %s has zero duration\n", pkg.Name, path)
		}
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
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	dir := filepath.Join(base, t.Format("20060102-150405"))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func collectEnvironment(cfg Config) environmentReport {
	report := environmentReport{
		GoVersion:        runtime.Version(),
		GOOS:             runtime.GOOS,
		GOARCH:           runtime.GOARCH,
		NumCPU:           runtime.NumCPU(),
		CPUModel:         os.Getenv("PROCESSOR_IDENTIFIER"),
		TotalMemoryBytes: totalMemoryBytes(),
		ProjectCommits:   make(map[string]string),
		CollectedAt:      time.Now(),
	}
	report.Hostname, _ = os.Hostname()
	if info, ok := debug.ReadBuildInfo(); ok {
		report.ApplicationVersion = info.Main.Version
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				report.ApplicationCommit = setting.Value
			case "vcs.modified":
				report.ApplicationDirty = setting.Value == "true"
			}
		}
	}
	if report.ApplicationCommit == "" {
		report.ApplicationCommit = gitValue(".", "rev-parse", "HEAD")
		report.ApplicationDirty = gitValue(".", "status", "--porcelain") != ""
	}
	for _, project := range cfg.Projects {
		report.ProjectCommits[project.Name] = gitValue(project.ProjectPath, "rev-parse", "HEAD")
	}
	return report
}

func totalMemoryBytes() uint64 {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("powershell", "-NoProfile", "-Command", "Add-Type -AssemblyName Microsoft.VisualBasic; (New-Object Microsoft.VisualBasic.Devices.ComputerInfo).TotalPhysicalMemory").Output()
		if err == nil {
			value, parseErr := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
			if parseErr == nil {
				return value
			}
		}
		return 0
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		var kib uint64
		if _, err := fmt.Sscanf(line, "MemTotal: %d kB", &kib); err == nil {
			return kib * 1024
		}
	}
	return 0
}

func gitValue(dir string, args ...string) string {
	cmdArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", cmdArgs...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
