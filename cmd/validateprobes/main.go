// Command validateprobes audits previously collected `go test -json` probes
// before they are aggregated into a characterization. It is intentionally
// independent from cmd/analyze so historical files can be checked without
// rewriting them.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type runMetadata struct {
	Command                 string    `json:"command,omitempty"`
	StartedAt               time.Time `json:"started_at,omitempty"`
	FinishedAt              time.Time `json:"finished_at,omitempty"`
	ExitCode                *int      `json:"exit_code,omitempty"`
	TimedOut                bool      `json:"timed_out,omitempty"`
	GOMAXPROCSConfigured    int       `json:"gomaxprocs_configured,omitempty"`
	GOMAXPROCSEffective     int       `json:"gomaxprocs_effective,omitempty"`
	GOMAXPROCSPolicy        string    `json:"gomaxprocs_policy,omitempty"`
	ChildEnvironmentApplied bool      `json:"child_environment_applied,omitempty"`
}

type runReport struct {
	File                    string            `json:"file"`
	MetadataFile            string            `json:"metadata_file,omitempty"`
	MetadataPresent         bool              `json:"metadata_present"`
	ExitCode                *int              `json:"exit_code,omitempty"`
	TimedOut                bool              `json:"timed_out"`
	TotalLines              int               `json:"total_lines"`
	JSONLines               int               `json:"json_lines"`
	MalformedLines          int               `json:"malformed_lines"`
	MalformedExamples       []string          `json:"malformed_examples,omitempty"`
	TerminalPackages        int               `json:"terminal_packages"`
	PassPackages            int               `json:"pass_packages"`
	FailPackages            int               `json:"fail_packages"`
	SkipPackages            int               `json:"skip_packages"`
	ZeroElapsedPasses       int               `json:"zero_elapsed_passes"`
	DuplicateTerminal       int               `json:"duplicate_terminal_events"`
	MissingExpected         []string          `json:"missing_expected,omitempty"`
	UnexpectedPackages      []string          `json:"unexpected_packages,omitempty"`
	StderrFile              string            `json:"stderr_file,omitempty"`
	StderrNonEmpty          bool              `json:"stderr_non_empty"`
	GOMAXPROCSConfigured    int               `json:"gomaxprocs_configured,omitempty"`
	GOMAXPROCSEffective     int               `json:"gomaxprocs_effective,omitempty"`
	GOMAXPROCSPolicy        string            `json:"gomaxprocs_policy,omitempty"`
	ChildEnvironmentApplied bool              `json:"child_environment_applied,omitempty"`
	TimeoutMetadataIgnored  bool              `json:"timeout_metadata_ignored,omitempty"`
	TerminalStatusByPkg     map[string]string `json:"-"`
}

type validationReport struct {
	Status            string      `json:"status"`
	Recommendation    string      `json:"recommendation"`
	GeneratedAt       time.Time   `json:"generated_at"`
	ProjectPath       string      `json:"project_path"`
	Pattern           string      `json:"pattern"`
	ExpectedRuns      int         `json:"expected_runs"`
	ReceivedRuns      int         `json:"received_runs"`
	ExpectedPackages  int         `json:"expected_packages"`
	CompleteRuns      int         `json:"complete_runs"`
	RunsWithMetadata  int         `json:"runs_with_metadata"`
	TotalMalformed    int         `json:"total_malformed_lines"`
	TotalMissing      int         `json:"total_missing_expected"`
	TotalFailStatuses int         `json:"total_fail_statuses"`
	TotalSkipStatuses int         `json:"total_skip_statuses"`
	FatalFindings     []string    `json:"fatal_findings,omitempty"`
	Warnings          []string    `json:"warnings,omitempty"`
	Runs              []runReport `json:"runs"`
}

func main() {
	projectPath := flag.String("project-path", "", "Go project root used by the probes (required).")
	pattern := flag.String("pattern", "./...", "Package pattern passed to go list.")
	expectedRuns := flag.Int("expected-runs", 10, "Expected number of probe files.")
	requireGOMAXPROCS := flag.Int("require-gomaxprocs", 0, "Require sidecar proof of this effective GOMAXPROCS value (0 disables the check).")
	output := flag.String("output", "", "Optional path for the JSON validation report.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s -project-path DIR [options] RUN_JSON...\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if strings.TrimSpace(*projectPath) == "" || len(flag.Args()) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	expected, err := listExpectedPackages(*projectPath, *pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PROBE VALIDATION: FAIL\nUnable to enumerate expected packages: %v\n", err)
		os.Exit(1)
	}

	report := validate(*projectPath, *pattern, *expectedRuns, *requireGOMAXPROCS, expected, flag.Args())
	printSummary(report)
	if *output != "" {
		if err := writeJSON(*output, report); err != nil {
			fmt.Fprintf(os.Stderr, "writing validation report: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("JSON report: %s\n", *output)
	}
	if report.Status == "FAIL" {
		os.Exit(1)
	}
}

func listExpectedPackages(projectPath, pattern string) ([]string, error) {
	cmd := exec.Command("go", "list", pattern)
	cmd.Dir = projectPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w: %s", pattern, err, strings.TrimSpace(string(out)))
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	packages := make([]string, 0, len(seen))
	for name := range seen {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	if len(packages) == 0 {
		return nil, fmt.Errorf("go list returned no packages")
	}
	return packages, nil
}

func validate(projectPath, pattern string, expectedRuns, requiredGOMAXPROCS int, expected []string, paths []string) validationReport {
	report := validationReport{
		GeneratedAt:      time.Now(),
		ProjectPath:      projectPath,
		Pattern:          pattern,
		ExpectedRuns:     expectedRuns,
		ReceivedRuns:     len(paths),
		ExpectedPackages: len(expected),
	}
	if expectedRuns > 0 && len(paths) != expectedRuns {
		report.FatalFindings = append(report.FatalFindings,
			fmt.Sprintf("received %d probe files; expected %d", len(paths), expectedRuns))
	}

	expectedSet := make(map[string]struct{}, len(expected))
	for _, pkg := range expected {
		expectedSet[pkg] = struct{}{}
	}

	for _, path := range paths {
		if strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".meta.json") {
			report.FatalFindings = append(report.FatalFindings,
				fmt.Sprintf("%s is a metadata sidecar, not probe NDJSON; select only run_NN.json", path))
			continue
		}
		run, err := inspectRun(path, expectedSet)
		if err != nil {
			report.FatalFindings = append(report.FatalFindings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		report.Runs = append(report.Runs, run)
		if run.MetadataPresent {
			report.RunsWithMetadata++
		}
		if len(run.MissingExpected) == 0 && run.MalformedLines == 0 && !run.TimedOut {
			report.CompleteRuns++
		}
		report.TotalMalformed += run.MalformedLines
		report.TotalMissing += len(run.MissingExpected)
		report.TotalFailStatuses += run.FailPackages
		report.TotalSkipStatuses += run.SkipPackages

		if run.MalformedLines > 0 {
			report.FatalFindings = append(report.FatalFindings,
				fmt.Sprintf("%s has %d malformed non-empty NDJSON line(s)", path, run.MalformedLines))
		}
		if len(run.MissingExpected) > 0 {
			report.FatalFindings = append(report.FatalFindings,
				fmt.Sprintf("%s is missing terminal events for %d expected package(s)", path, len(run.MissingExpected)))
		}
		if run.TimedOut {
			report.FatalFindings = append(report.FatalFindings, fmt.Sprintf("%s records or indicates a timeout", path))
		}
		if run.TimeoutMetadataIgnored {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%s records timed_out=true with exit_code=0 and a complete terminal set; ignoring inconsistent legacy metadata", path))
		}
		if run.ExitCode != nil && *run.ExitCode != 0 && run.FailPackages == 0 && run.SkipPackages == 0 {
			report.FatalFindings = append(report.FatalFindings,
				fmt.Sprintf("%s exited with code %d without a package fail/skip explaining it", path, *run.ExitCode))
		}
		if !run.MetadataPresent {
			if requiredGOMAXPROCS > 0 {
				report.FatalFindings = append(report.FatalFindings, fmt.Sprintf("%s has no sidecar metadata proving GOMAXPROCS=%d", path, requiredGOMAXPROCS))
			} else {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s has no sidecar metadata; exit code cannot be verified retroactively", path))
			}
		} else if requiredGOMAXPROCS > 0 && (run.GOMAXPROCSConfigured != requiredGOMAXPROCS || run.GOMAXPROCSEffective != requiredGOMAXPROCS || !run.ChildEnvironmentApplied) {
			report.FatalFindings = append(report.FatalFindings,
				fmt.Sprintf("%s lacks valid GOMAXPROCS evidence: configured=%d effective=%d applied=%t expected=%d", path, run.GOMAXPROCSConfigured, run.GOMAXPROCSEffective, run.ChildEnvironmentApplied, requiredGOMAXPROCS))
		}
		if run.StderrNonEmpty {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s has non-empty stderr (%s)", path, run.StderrFile))
		}
		if run.DuplicateTerminal > 0 {
			report.FatalFindings = append(report.FatalFindings, fmt.Sprintf("%s has %d duplicate terminal event(s)", path, run.DuplicateTerminal))
		}
		if len(run.UnexpectedPackages) > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s contains %d package(s) outside current go list", path, len(run.UnexpectedPackages)))
		}
		if run.ZeroElapsedPasses > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s has %d successful package(s) with zero Elapsed", path, run.ZeroElapsedPasses))
		}
		if run.FailPackages > 0 || run.SkipPackages > 0 {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%s contains fail=%d skip=%d; these packages will be excluded by pass-only aggregation", path, run.FailPackages, run.SkipPackages))
		}
	}

	switch {
	case len(report.FatalFindings) > 0:
		report.Status = "FAIL"
		report.Recommendation = "DO NOT AGGREGATE: correct or recollect the failed runs, then validate again."
	case len(report.Warnings) > 0:
		report.Status = "WARN"
		report.Recommendation = "SAFE TO AGGREGATE WITH REVIEW: structural completeness passed, but inspect the warnings."
	default:
		report.Status = "PASS"
		report.Recommendation = "SAFE TO AGGREGATE: all expected runs and package terminal events are complete."
	}
	return report
}

func inspectRun(path string, expected map[string]struct{}) (runReport, error) {
	r := runReport{File: path, TerminalStatusByPkg: make(map[string]string)}
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	first := true
	for scanner.Scan() {
		r.TotalLines++
		line := scanner.Bytes()
		if first {
			line = bytes.TrimPrefix(line, []byte{0xEF, 0xBB, 0xBF})
			first = false
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event testEvent
		if err := json.Unmarshal(line, &event); err != nil {
			r.MalformedLines++
			if len(r.MalformedExamples) < 3 {
				example := string(line)
				if len(example) > 240 {
					example = example[:240] + "..."
				}
				r.MalformedExamples = append(r.MalformedExamples, example)
			}
			continue
		}
		r.JSONLines++
		if event.Test != "" || (event.Action != "pass" && event.Action != "fail" && event.Action != "skip") {
			continue
		}
		if strings.TrimSpace(event.Package) == "" {
			r.MalformedLines++
			continue
		}
		if _, exists := r.TerminalStatusByPkg[event.Package]; exists {
			r.DuplicateTerminal++
		}
		r.TerminalStatusByPkg[event.Package] = event.Action
		if event.Action == "pass" && event.Elapsed <= 0 {
			r.ZeroElapsedPasses++
		}
	}
	if err := scanner.Err(); err != nil {
		return r, err
	}

	for pkg, status := range r.TerminalStatusByPkg {
		switch status {
		case "pass":
			r.PassPackages++
		case "fail":
			r.FailPackages++
		case "skip":
			r.SkipPackages++
		}
		if _, ok := expected[pkg]; !ok {
			r.UnexpectedPackages = append(r.UnexpectedPackages, pkg)
		}
	}
	r.TerminalPackages = len(r.TerminalStatusByPkg)
	for pkg := range expected {
		if _, ok := r.TerminalStatusByPkg[pkg]; !ok {
			r.MissingExpected = append(r.MissingExpected, pkg)
		}
	}
	sort.Strings(r.MissingExpected)
	sort.Strings(r.UnexpectedPackages)

	base := strings.TrimSuffix(path, filepath.Ext(path))
	r.MetadataFile = base + ".meta.json"
	if data, err := os.ReadFile(r.MetadataFile); err == nil {
		var meta runMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			return r, fmt.Errorf("parsing metadata %s: %w", r.MetadataFile, err)
		}
		r.MetadataPresent = true
		r.ExitCode = meta.ExitCode
		r.TimedOut = meta.TimedOut
		if meta.TimedOut && meta.ExitCode != nil && *meta.ExitCode == 0 &&
			len(r.MissingExpected) == 0 && r.MalformedLines == 0 {
			r.TimedOut = false
			r.TimeoutMetadataIgnored = true
		}
		r.GOMAXPROCSConfigured = meta.GOMAXPROCSConfigured
		r.GOMAXPROCSEffective = meta.GOMAXPROCSEffective
		r.GOMAXPROCSPolicy = meta.GOMAXPROCSPolicy
		r.ChildEnvironmentApplied = meta.ChildEnvironmentApplied
	}
	r.StderrFile = base + ".err"
	if info, err := os.Stat(r.StderrFile); err == nil && info.Size() > 0 {
		r.StderrNonEmpty = true
		if data, err := os.ReadFile(r.StderrFile); err == nil &&
			bytes.Contains(bytes.ToLower(data), []byte("panic: test timed out after ")) {
			r.TimedOut = true
		}
	}
	return r, nil
}

func printSummary(r validationReport) {
	fmt.Printf("PROBE VALIDATION: %s\n", r.Status)
	fmt.Printf("Runs: %d/%d | Expected packages: %d | Structurally complete: %d/%d\n",
		r.ReceivedRuns, r.ExpectedRuns, r.ExpectedPackages, r.CompleteRuns, r.ReceivedRuns)
	fmt.Printf("Malformed NDJSON: %d | Missing terminal events: %d | fail statuses: %d | skip statuses: %d\n",
		r.TotalMalformed, r.TotalMissing, r.TotalFailStatuses, r.TotalSkipStatuses)
	fmt.Printf("Metadata available: %d/%d\n", r.RunsWithMetadata, r.ReceivedRuns)
	if len(r.FatalFindings) > 0 {
		fmt.Println("Blocking findings:")
		for _, finding := range r.FatalFindings {
			fmt.Printf("  - %s\n", finding)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Println("Warnings:")
		limit := len(r.Warnings)
		if limit > 12 {
			limit = 12
		}
		for _, warning := range r.Warnings[:limit] {
			fmt.Printf("  - %s\n", warning)
		}
		if len(r.Warnings) > limit {
			fmt.Printf("  - ... and %d additional warning(s); see the JSON report.\n", len(r.Warnings)-limit)
		}
	}
	fmt.Printf("Recommendation: %s\n", r.Recommendation)
}

func writeJSON(path string, report validationReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
