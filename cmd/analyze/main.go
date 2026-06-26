// Command analyze parses one or more `go test -json` output files,
// aggregates per-package median durations and coefficient of variation
// across runs, and emits a PackageInfo[] JSON consumable by the
// partitioner CLI (cmd/partitioner --mode simulate).
//
// Behavior is governed by the project's ADRs:
//   - ADR-006: packages whose status is "fail" or "skip" in *any* run
//     are excluded from the output. Packages missing from any
//     run are also excluded (we need N samples for CV).
//   - ADR-007: expects N runs (default workflow uses N=10).
//   - ADR-008: median across the N runs is the canonical duration.
//
// Input format:
//
//	`go test -json` emits NDJSON (one event per line). See
//	`go doc cmd/test2json` for the full event schema.
//
// Usage:
//
//	# Aggregate N pre-collected run files:
//	go run ./cmd/analyze -output data/characterization/cli.json \
//	    data/probe/cli/run_01.json data/probe/cli/run_02.json ...
//
//	# Single run via stdin:
//	go test -json ./... | go run ./cmd/analyze -output out.json
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"

	"tcc-test-partitioning/internal/model"
)

// testEvent mirrors the subset of fields emitted by `go test -json`
// that we consume. Schema: `go doc cmd/test2json`.
type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`  // start, run, pause, cont, pass, fail, skip, output, bench
	Package string    `json:"Package"` // import path
	Test    string    `json:"Test"`    // empty for package-level events
	Elapsed float64   `json:"Elapsed"` // seconds, on pass/fail/skip
	Output  string    `json:"Output"`  // raw test stdout, ignored
}

// pkgOutcome captures the final state of one package within a single run.
type pkgOutcome struct {
	Status  string        // pass, fail, skip
	Elapsed time.Duration // wall-clock per `go test -json`
}

func main() {
	output := flag.String("output", "",
		"Path to write the aggregated PackageInfo JSON. Default: stdout.")
	verbose := flag.Bool("v", false,
		"Print per-package inclusion/exclusion details to stderr.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"Usage: %s [-output FILE] [-v] RUN_JSON [RUN_JSON ...]\n",
			os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	runPaths := flag.Args()

	// Per-run outcomes: runs[i][pkg] = pkgOutcome.
	var runs []map[string]pkgOutcome

	if len(runPaths) == 0 {
		// Read a single run from stdin.
		r, err := parseRun(os.Stdin)
		if err != nil {
			fail("reading stdin: %v", err)
		}
		runs = append(runs, r)
	} else {
		for _, p := range runPaths {
			f, err := os.Open(p)
			if err != nil {
				fail("opening %s: %v", p, err)
			}
			r, err := parseRun(f)
			f.Close()
			if err != nil {
				fail("parsing %s: %v", p, err)
			}
			runs = append(runs, r)
		}
	}

	pkgs := aggregate(runs, *verbose)

	if err := emit(*output, pkgs); err != nil {
		fail("writing output: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Runs: %d | Packages emitted: %d\n",
		len(runs), len(pkgs))
}

// parseRun consumes an NDJSON stream from a single `go test -json`
// invocation and returns the final outcome per package.
//
// Strategy: process events in order. The last package-level
// pass/fail/skip event for each package wins (in practice each
// package produces exactly one such event).
func parseRun(r io.Reader) (map[string]pkgOutcome, error) {
	out := make(map[string]pkgOutcome)

	sc := bufio.NewScanner(r)
	// `go test -json` Output events can be long; bump the buffer.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	first := true
	for sc.Scan() {
		line := sc.Bytes()

		// Strip UTF-8 BOM that PowerShell's Out-File may emit on the
		// first line.
		if first {
			line = bytes.TrimPrefix(line, []byte{0xEF, 0xBB, 0xBF})
			first = false
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Tolerate non-JSON noise (e.g. compile errors printed
			// outside the JSON stream by older toolchains).
			continue
		}

		// We only care about package-level terminal events.
		if ev.Test != "" {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			out[ev.Package] = pkgOutcome{
				Status:  ev.Action,
				Elapsed: secondsToDuration(ev.Elapsed),
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanner: %w", err)
	}
	return out, nil
}

// secondsToDuration converts a float seconds value (as emitted by
// `go test -json`) into time.Duration without precision loss beyond
// nanosecond resolution.
func secondsToDuration(s float64) time.Duration {
	if s <= 0 || math.IsNaN(s) || math.IsInf(s, 0) {
		return 0
	}
	return time.Duration(s * float64(time.Second))
}

// aggregate merges per-run outcomes into the final PackageInfo list.
//
// Inclusion rule (ADR-006):
//   - The package must appear in every run.
//   - It must have Status == "pass" in every run.
//
// Otherwise the package is dropped and (in verbose mode) the reason
// is reported to stderr.
//
// Duration:  median across runs (ADR-008).
func aggregate(runs []map[string]pkgOutcome, verbose bool) []model.PackageInfo {
	if len(runs) == 0 {
		return nil
	}

	// Union of all packages observed in any run.
	seen := make(map[string]struct{})
	for _, r := range runs {
		for pkg := range r {
			seen[pkg] = struct{}{}
		}
	}

	var (
		out       []model.PackageInfo
		excludedF int
		excludedS int
		excludedM int
	)

	for pkg := range seen {
		samples := make([]time.Duration, 0, len(runs))
		reason := ""
		for _, r := range runs {
			o, ok := r[pkg]
			if !ok {
				reason = "missing-in-run"
				excludedM++
				break
			}
			switch o.Status {
			case "fail":
				reason = "fail"
				excludedF++
			case "skip":
				reason = "skip"
				excludedS++
			}
			if reason != "" {
				break
			}
			samples = append(samples, o.Elapsed)
		}

		if reason != "" {
			if verbose {
				fmt.Fprintf(os.Stderr, "  exclude %s (%s)\n", pkg, reason)
			}
			continue
		}

		med := median(samples)
		out = append(out, model.PackageInfo{
			Name:     pkg,
			Duration: med,
		})
		if verbose {
			fmt.Fprintf(os.Stderr, "  include %s median=%v\n",
				pkg, med)
		}
	}

	// Deterministic order so diffs across collections are meaningful.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	fmt.Fprintf(os.Stderr,
		"Excluded: fail=%d skip=%d missing=%d\n",
		excludedF, excludedS, excludedM)

	return out
}

// median returns the median of a non-empty duration slice. Mutates
// a local copy; the caller's slice is preserved.
func median(in []time.Duration) time.Duration {
	if len(in) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}



// emit serializes pkgs as indented JSON to the given path, or stdout
// when path is empty.
func emit(path string, pkgs []model.PackageInfo) error {
	data, err := json.MarshalIndent(pkgs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if path == "" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
