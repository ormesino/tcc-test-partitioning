// Command workerdiag runs a small, non-canonical diagnostic that compares the
// current worker semantics with child processes constrained by GOMAXPROCS=1.
// It does not replace characterization, baselines, or final campaigns.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"tcc-test-partitioning/internal/executor"
	"tcc-test-partitioning/internal/model"
	"tcc-test-partitioning/internal/partitioner"
)

type scenario struct {
	Name       string `json:"name"`
	GOMAXPROCS int    `json:"gomaxprocs_override"`
	SelfCheck  int    `json:"self_check_effective_gomaxprocs"`
}

type workerObservation struct {
	WorkerID     int    `json:"worker_id"`
	PackageCount int    `json:"package_count"`
	ElapsedNS    int64  `json:"elapsed_ns"`
	Error        string `json:"error,omitempty"`
}

type observation struct {
	Scenario   string              `json:"scenario"`
	Workers    int                 `json:"workers"`
	Repetition int                 `json:"repetition"`
	MakespanNS int64               `json:"makespan_ns"`
	Error      string              `json:"error,omitempty"`
	WorkerData []workerObservation `json:"worker_data"`
}

type summary struct {
	Workers             int     `json:"workers"`
	DefaultMedianNS     int64   `json:"default_median_ns"`
	GOMAXPROCS1MedianNS int64   `json:"gomaxprocs_1_median_ns"`
	RatioG1ToDefault    float64 `json:"ratio_gomaxprocs_1_to_default"`
	Interpretation      string  `json:"interpretation"`
}

type diagnosticEnvironment struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	NumCPU    int    `json:"num_cpu"`
	Hostname  string `json:"hostname,omitempty"`
}

type diagnosticReport struct {
	GeneratedAt      time.Time             `json:"generated_at"`
	Environment      diagnosticEnvironment `json:"environment"`
	ProjectPath      string                `json:"project_path"`
	DataFile         string                `json:"data_file"`
	WarmCache        bool                  `json:"warm_cache"`
	Repetitions      int                   `json:"repetitions"`
	SelectedPackages []model.PackageInfo   `json:"selected_packages"`
	Scenarios        []scenario            `json:"scenarios"`
	Observations     []observation         `json:"observations"`
	Summary          []summary             `json:"summary"`
	Caveat           string                `json:"caveat"`
}

func main() {
	projectPath := flag.String("project-path", "", "Go project root (required).")
	dataFile := flag.String("data-file", "", "Characterization PackageInfo JSON (required).")
	workersArg := flag.String("workers", "1,2,4,8", "Comma-separated worker counts.")
	repetitions := flag.Int("repetitions", 3, "Diagnostic repetitions per scenario and worker count.")
	topPackages := flag.Int("top-packages", 8, "Use the N longest characterized packages.")
	timeoutMinutes := flag.Int("timeout-minutes", 30, "Timeout per go test process.")
	warmCache := flag.Bool("warm-cache", true, "Pre-warm reusable build-cache artifacts before measurement.")
	outputDir := flag.String("output-dir", "", "Output directory. Default: diagnostics/worker-semantics/<timestamp>.")
	flag.Parse()

	if strings.TrimSpace(*projectPath) == "" || strings.TrimSpace(*dataFile) == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *repetitions < 1 || *topPackages < 1 || *timeoutMinutes < 1 {
		fatal("repetitions, top-packages, and timeout-minutes must be positive")
	}
	workers, err := parseWorkers(*workersArg)
	if err != nil {
		fatal("workers: %v", err)
	}
	packages, err := loadPackages(*dataFile)
	if err != nil {
		fatal("data file: %v", err)
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Duration != packages[j].Duration {
			return packages[i].Duration > packages[j].Duration
		}
		return packages[i].Name < packages[j].Name
	})
	if *topPackages < len(packages) {
		packages = packages[:*topPackages]
	}

	out := *outputDir
	if out == "" {
		out = filepath.Join("diagnostics", "worker-semantics", time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fatal("creating output directory: %v", err)
	}

	scenarios := []scenario{
		{Name: "inherited-default", GOMAXPROCS: 0},
		{Name: "gomaxprocs-1", GOMAXPROCS: 1},
	}
	for i := range scenarios {
		effective, err := selfCheckGOMAXPROCS(scenarios[i].GOMAXPROCS)
		if err != nil {
			fatal("GOMAXPROCS self-check for %s: %v", scenarios[i].Name, err)
		}
		scenarios[i].SelfCheck = effective
	}

	packageNames := names(packages)
	if *warmCache {
		fmt.Printf("Pre-warming selected packages (%d)...\n", len(packageNames))
		if err := executor.WarmBuildCachePackages(executor.Config{
			ProjectPath: *projectPath,
			Timeout:     time.Duration(*timeoutMinutes) * time.Minute,
		}, packageNames); err != nil {
			fatal("warm-up: %v", err)
		}
	}

	hostname, _ := os.Hostname()
	report := diagnosticReport{
		GeneratedAt: time.Now(),
		Environment: diagnosticEnvironment{
			GoVersion: runtime.Version(),
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			NumCPU:    runtime.NumCPU(),
			Hostname:  hostname,
		},
		ProjectPath:      *projectPath,
		DataFile:         *dataFile,
		WarmCache:        *warmCache,
		Repetitions:      *repetitions,
		SelectedPackages: packages,
		Scenarios:        scenarios,
		Caveat:           "This targeted diagnostic estimates whether constraining child go test processes to GOMAXPROCS=1 materially changes selected dominant packages. It does not prove the absence or presence of internal concurrency in every package and is not a replacement for canonical experiments.",
	}

	for _, workersCount := range workers {
		partition := (&partitioner.LPT{}).Partition(packages, workersCount)
		for rep := 1; rep <= *repetitions; rep++ {
			order := scenarios
			if rep%2 == 0 {
				order = []scenario{scenarios[1], scenarios[0]}
			}
			for _, sc := range order {
				fmt.Printf("workers=%d rep=%d scenario=%s\n", workersCount, rep, sc.Name)
				result := executor.RunPartitioned(executor.Config{
					ProjectPath:                    *projectPath,
					Timeout:                        time.Duration(*timeoutMinutes) * time.Minute,
					Count:                          1,
					WarmCache:                      *warmCache,
					GOMAXPROCS:                     sc.GOMAXPROCS,
					InheritGOMAXPROCSForDiagnostic: sc.GOMAXPROCS == 0,
				}, partition)
				obs := observation{Scenario: sc.Name, Workers: workersCount, Repetition: rep, MakespanNS: int64(result.Makespan)}
				var errors []string
				for _, wr := range result.WorkerResults {
					worker := workerObservation{WorkerID: wr.WorkerID, PackageCount: wr.PackageCount, ElapsedNS: int64(wr.Elapsed)}
					if wr.Error != nil {
						worker.Error = wr.Error.Error()
						errors = append(errors, fmt.Sprintf("worker %d: %v", wr.WorkerID, wr.Error))
					}
					obs.WorkerData = append(obs.WorkerData, worker)
				}
				obs.Error = strings.Join(errors, "; ")
				report.Observations = append(report.Observations, obs)
			}
		}
	}
	report.Summary = summarize(report.Observations, workers)

	if err := writeJSON(filepath.Join(out, "worker_semantics.json"), report); err != nil {
		fatal("writing JSON: %v", err)
	}
	if err := writeCSV(filepath.Join(out, "worker_semantics.csv"), report.Observations); err != nil {
		fatal("writing CSV: %v", err)
	}
	if err := writeSummary(filepath.Join(out, "summary.txt"), report); err != nil {
		fatal("writing summary: %v", err)
	}
	printSummary(report)
	fmt.Printf("Reports written under %s\n", out)
}

func parseWorkers(value string) ([]int, error) {
	seen := make(map[int]struct{})
	var out []int
	for _, part := range strings.Split(value, ",") {
		w, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || w < 1 {
			return nil, fmt.Errorf("invalid worker count %q", part)
		}
		if _, exists := seen[w]; !exists {
			seen[w] = struct{}{}
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no worker counts provided")
	}
	return out, nil
}

func loadPackages(path string) ([]model.PackageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var packages []model.PackageInfo
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("empty package population")
	}
	seen := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		if strings.TrimSpace(pkg.Name) == "" || pkg.Duration < 0 {
			return nil, fmt.Errorf("invalid package entry: %+v", pkg)
		}
		if _, exists := seen[pkg.Name]; exists {
			return nil, fmt.Errorf("duplicate package name %q", pkg.Name)
		}
		seen[pkg.Name] = struct{}{}
	}
	return packages, nil
}

func names(packages []model.PackageInfo) []string {
	out := make([]string, len(packages))
	for i, pkg := range packages {
		out[i] = pkg.Name
	}
	return out
}

func selfCheckGOMAXPROCS(override int) (int, error) {
	dir, err := os.MkdirTemp("", "tcc-gomaxprocs-check-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\nimport (\"fmt\"; \"runtime\")\nfunc main(){fmt.Print(runtime.GOMAXPROCS(0))}\n"), 0o644); err != nil {
		return 0, err
	}
	cmd := exec.Command("go", "run", path)
	if override > 0 {
		cmd.Env = replaceEnv(os.Environ(), "GOMAXPROCS", strconv.Itoa(override))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func replaceEnv(environ []string, key, value string) []string {
	var out []string
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, key) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, key+"="+value)
}

func summarize(observations []observation, workers []int) []summary {
	var out []summary
	for _, w := range workers {
		values := map[string][]int64{"inherited-default": {}, "gomaxprocs-1": {}}
		for _, obs := range observations {
			if obs.Workers == w && obs.Error == "" && obs.MakespanNS > 0 {
				values[obs.Scenario] = append(values[obs.Scenario], obs.MakespanNS)
			}
		}
		def := median(values["inherited-default"])
		g1 := median(values["gomaxprocs-1"])
		ratio := 0.0
		interpretation := "INCONCLUSIVE: a scenario has no successful observations."
		if def > 0 && g1 > 0 {
			ratio = float64(g1) / float64(def)
			switch {
			case ratio >= 1.10:
				interpretation = "MATERIAL SIGNAL: GOMAXPROCS=1 was at least 10% slower; internal CPU parallelism may affect worker semantics."
			case ratio <= 0.90:
				interpretation = "MATERIAL REVERSE SIGNAL: GOMAXPROCS=1 was at least 10% faster; investigate contention or scheduling effects."
			default:
				interpretation = "SMALL SIGNAL ON THIS SAMPLE: median difference stayed within +/-10%."
			}
		}
		out = append(out, summary{Workers: w, DefaultMedianNS: def, GOMAXPROCS1MedianNS: g1, RatioG1ToDefault: ratio, Interpretation: interpretation})
	}
	return out
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	mid := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[mid]
	}
	return (copyValues[mid-1] + copyValues[mid]) / 2
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeCSV(path string, observations []observation) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write([]string{"scenario", "workers", "repetition", "makespan_ns", "error"}); err != nil {
		return err
	}
	for _, obs := range observations {
		if err := writer.Write([]string{obs.Scenario, strconv.Itoa(obs.Workers), strconv.Itoa(obs.Repetition), strconv.FormatInt(obs.MakespanNS, 10), obs.Error}); err != nil {
			return err
		}
	}
	return writer.Error()
}

func writeSummary(path string, report diagnosticReport) error {
	var builder strings.Builder
	builder.WriteString("WORKER SEMANTICS DIAGNOSTIC\n")
	for _, sc := range report.Scenarios {
		fmt.Fprintf(&builder, "%s: configured=%d self-check-effective=%d\n", sc.Name, sc.GOMAXPROCS, sc.SelfCheck)
	}
	builder.WriteString("\n")
	for _, item := range report.Summary {
		fmt.Fprintf(&builder, "workers=%d default=%v gomaxprocs1=%v ratio=%.3f\n  %s\n",
			item.Workers, time.Duration(item.DefaultMedianNS), time.Duration(item.GOMAXPROCS1MedianNS), item.RatioG1ToDefault, item.Interpretation)
	}
	builder.WriteString("\nCaveat: " + report.Caveat + "\n")
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func printSummary(report diagnosticReport) {
	fmt.Println("WORKER SEMANTICS DIAGNOSTIC")
	for _, sc := range report.Scenarios {
		fmt.Printf("  %s: effective GOMAXPROCS self-check=%d\n", sc.Name, sc.SelfCheck)
	}
	for _, item := range report.Summary {
		fmt.Printf("  workers=%d | default=%v | GOMAXPROCS=1=%v | ratio=%.3f\n    %s\n",
			item.Workers, time.Duration(item.DefaultMedianNS), time.Duration(item.GOMAXPROCS1MedianNS), item.RatioG1ToDefault, item.Interpretation)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
