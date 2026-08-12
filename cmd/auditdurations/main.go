// Command auditdurations independently rebuilds a characterization from probe
// NDJSON and compares it with the stored PackageInfo data. It preserves the raw
// decimal Elapsed lexemes, normalizes test2json's millisecond precision and
// reports population, duration and suite-level differences.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"tcc-test-partitioning/internal/model"
)

type event struct {
	Action  string      `json:"Action"`
	Package string      `json:"Package"`
	Test    string      `json:"Test"`
	Elapsed json.Number `json:"Elapsed"`
}

type outcome struct {
	status  string
	elapsed json.Number
}

type difference struct {
	Package         string   `json:"package"`
	RawSeconds      []string `json:"raw_seconds"`
	ExpectedNS      int64    `json:"expected_ns"`
	StoredNS        int64    `json:"stored_ns"`
	AbsoluteNS      int64    `json:"absolute_difference_ns"`
	Relative        float64  `json:"relative_difference"`
	WithinTolerance bool     `json:"within_tolerance"`
}

type report struct {
	Classification                 string         `json:"classification"`
	PrecisionSource                string         `json:"precision_source"`
	ToleranceNS                    int64          `json:"tolerance_ns"`
	ProbeFiles                     int            `json:"probe_files"`
	MalformedLines                 int            `json:"malformed_lines"`
	PackageTerminalEvents          int            `json:"package_terminal_events"`
	IndividualTestTerminalEvents   int            `json:"individual_test_terminal_events_ignored"`
	DuplicatePackageTerminalEvents int            `json:"duplicate_package_terminal_events"`
	DecimalPlaces                  map[string]int `json:"elapsed_decimal_places"`
	ScientificNotation             int            `json:"scientific_notation_values"`
	IntegerValues                  int            `json:"integer_values"`
	ZeroValues                     int            `json:"zero_values"`
	Below1Microsecond              int            `json:"below_1_microsecond"`
	Below1Millisecond              int            `json:"below_1_millisecond"`
	Below10Milliseconds            int            `json:"below_10_milliseconds"`
	Below100Milliseconds           int            `json:"below_100_milliseconds"`
	DistinctValues                 int            `json:"distinct_values"`
	RepeatedProportion             float64        `json:"repeated_proportion"`
	MinimumPositiveSeconds         float64        `json:"minimum_positive_seconds"`
	RawMedianSeconds               float64        `json:"raw_median_seconds"`
	RawMaximumSeconds              float64        `json:"raw_maximum_seconds"`
	PassOnlyPackages               int            `json:"pass_only_packages"`
	StoredPackages                 int            `json:"stored_packages"`
	ResidualDifferences            int            `json:"residual_differences"`
	Divergences                    int            `json:"divergences_beyond_tolerance"`
	LargestAbsoluteDifferenceNS    int64          `json:"largest_absolute_difference_ns"`
	LargestRelativeDifference      float64        `json:"largest_relative_difference"`
	Differences                    []difference   `json:"differences,omitempty"`
	SuiteMeanSeconds               float64        `json:"suite_mean_seconds"`
	SuiteMedianSeconds             float64        `json:"suite_median_seconds"`
	SuiteVarianceSeconds2          float64        `json:"suite_population_variance_seconds2"`
	SuiteStdDevSeconds             float64        `json:"suite_population_stddev_seconds"`
	SuiteCV                        float64        `json:"suite_cv"`
	SuiteMaxOverMedian             float64        `json:"suite_max_over_median"`
	PackagesFor50Percent           int            `json:"packages_for_50_percent"`
	PackagesFor80Percent           int            `json:"packages_for_80_percent"`
	PackagesFor90Percent           int            `json:"packages_for_90_percent"`
}

func main() {
	characterization := flag.String("characterization", "", "Characterization JSON to reconcile (required).")
	output := flag.String("output", "", "Optional JSON output path; default is stdout.")
	tolerance := flag.Int64("tolerance-ns", 1, "Maximum accepted serialization residue in nanoseconds.")
	flag.Parse()
	if *characterization == "" || len(flag.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "usage: auditdurations -characterization FILE [options] RUN_JSON...")
		os.Exit(2)
	}
	r, err := audit(flag.Args(), *characterization, *tolerance)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	data = append(data, '\n')
	if *output == "" {
		_, err = os.Stdout.Write(data)
	} else {
		err = os.WriteFile(*output, data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if r.Divergences > 0 {
		os.Exit(1)
	}
}

// audit reconstructs the pass-only population and medians without calling the
// production analyzer, keeping the verification path independent.
func audit(paths []string, characterization string, tolerance int64) (report, error) {
	r := report{Classification: "A — sem erro", PrecisionSource: "go test/test2json rounds package terminal Elapsed to 1 ms", ToleranceNS: tolerance, DecimalPlaces: map[string]int{}}
	runs := make([]map[string]outcome, 0, len(paths))
	allValues := make([]float64, 0)
	distinct := map[string]struct{}{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return r, err
		}
		run, err := parseProbe(f, &r, &allValues, distinct)
		f.Close()
		if err != nil {
			return r, fmt.Errorf("%s: %w", path, err)
		}
		runs = append(runs, run)
	}
	r.ProbeFiles = len(runs)
	if r.MalformedLines > 0 {
		return r, fmt.Errorf("found %d malformed NDJSON line(s)", r.MalformedLines)
	}

	data, err := os.ReadFile(characterization)
	if err != nil {
		return r, err
	}
	var packages []model.PackageInfo
	if err := json.Unmarshal(data, &packages); err != nil {
		return r, err
	}
	stored := make(map[string]int64, len(packages))
	for _, p := range packages {
		if strings.TrimSpace(p.Name) == "" {
			return r, fmt.Errorf("characterization contains empty package name")
		}
		if _, ok := stored[p.Name]; ok {
			return r, fmt.Errorf("characterization contains duplicate package %q", p.Name)
		}
		if p.Duration < 0 {
			return r, fmt.Errorf("characterization contains negative duration for %q", p.Name)
		}
		stored[p.Name] = int64(p.Duration)
	}
	r.StoredPackages = len(stored)

	union := map[string]struct{}{}
	for _, run := range runs {
		for name := range run {
			union[name] = struct{}{}
		}
	}
	expected := map[string]int64{}
	for name := range union {
		raw := make([]string, 0, len(runs))
		milliseconds := make([]int64, 0, len(runs))
		valid := true
		for _, run := range runs {
			o, ok := run[name]
			if !ok || o.status != "pass" {
				valid = false
				break
			}
			raw = append(raw, o.elapsed.String())
			ms, err := roundedMilliseconds(o.elapsed.String())
			if err != nil {
				return r, fmt.Errorf("package %s: %w", name, err)
			}
			milliseconds = append(milliseconds, ms)
		}
		if !valid {
			continue
		}
		sort.Slice(milliseconds, func(i, j int) bool { return milliseconds[i] < milliseconds[j] })
		n := len(milliseconds)
		var ns int64
		if n%2 == 1 {
			ns = milliseconds[n/2] * int64(time.Millisecond)
		} else {
			ns = (milliseconds[n/2-1] + milliseconds[n/2]) * int64(time.Millisecond) / 2
		}
		expected[name] = ns
		actual, ok := stored[name]
		if !ok {
			continue
		}
		delta := actual - ns
		abs := delta
		if abs < 0 {
			abs = -abs
		}
		if abs > 0 {
			d := difference{Package: name, RawSeconds: raw, ExpectedNS: ns, StoredNS: actual, AbsoluteNS: abs, WithinTolerance: abs <= tolerance}
			if ns != 0 {
				d.Relative = float64(abs) / float64(ns)
			}
			r.Differences = append(r.Differences, d)
			r.ResidualDifferences++
			if abs > tolerance {
				r.Divergences++
			}
			if abs > r.LargestAbsoluteDifferenceNS {
				r.LargestAbsoluteDifferenceNS = abs
			}
			if d.Relative > r.LargestRelativeDifference {
				r.LargestRelativeDifference = d.Relative
			}
		}
	}
	r.PassOnlyPackages = len(expected)
	if len(expected) != len(stored) {
		return r, fmt.Errorf("population mismatch: probes=%d characterization=%d", len(expected), len(stored))
	}
	for name := range expected {
		if _, ok := stored[name]; !ok {
			return r, fmt.Errorf("characterization is missing pass-only package %q", name)
		}
	}
	for name := range stored {
		if _, ok := expected[name]; !ok {
			return r, fmt.Errorf("characterization has non-pass-only package %q", name)
		}
	}

	fillRawStats(&r, allValues, distinct)
	durations := make([]int64, 0, len(expected))
	for _, ns := range expected {
		durations = append(durations, ns)
	}
	fillSuiteStats(&r, durations)
	if r.Divergences > 0 {
		r.Classification = "C — erro real de cálculo ou conversão"
	}
	sort.Slice(r.Differences, func(i, j int) bool { return r.Differences[i].Package < r.Differences[j].Package })
	return r, nil
}

// parseProbe reads one NDJSON probe and retains its package-level terminal
// events. Test-level events contribute only to the audit counters.
func parseProbe(rd io.Reader, r *report, all *[]float64, distinct map[string]struct{}) (map[string]outcome, error) {
	out := map[string]outcome{}
	s := bufio.NewScanner(rd)
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	first := true
	for s.Scan() {
		line := s.Bytes()
		if first {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			first = false
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var ev event
		if err := dec.Decode(&ev); err != nil {
			r.MalformedLines++
			continue
		}
		if ev.Action != "pass" && ev.Action != "fail" && ev.Action != "skip" {
			continue
		}
		if ev.Test != "" {
			r.IndividualTestTerminalEvents++
			continue
		}
		if strings.TrimSpace(ev.Package) == "" {
			r.MalformedLines++
			continue
		}
		if _, ok := out[ev.Package]; ok {
			r.DuplicatePackageTerminalEvents++
		}
		out[ev.Package] = outcome{ev.Action, ev.Elapsed}
		r.PackageTerminalEvents++
		text := ev.Elapsed.String()
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return out, err
		}
		*all = append(*all, value)
		distinct[text] = struct{}{}
		if strings.ContainsAny(text, "eE") {
			r.ScientificNotation++
		}
		if dot := strings.IndexByte(text, '.'); dot >= 0 {
			r.DecimalPlaces[strconv.Itoa(len(text)-dot-1)]++
		} else {
			r.IntegerValues++
		}
	}
	return out, s.Err()
}

// roundedMilliseconds parses the original decimal exactly and reproduces the
// millisecond rounding applied by test2json before median aggregation.
func roundedMilliseconds(text string) (int64, error) {
	rat, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, fmt.Errorf("invalid decimal Elapsed %q", text)
	}
	rat.Mul(rat, big.NewRat(1000, 1))
	q := new(big.Int)
	rem := new(big.Int)
	q.QuoRem(rat.Num(), rat.Denom(), rem)
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	if twice.Cmp(rat.Denom()) >= 0 {
		if rat.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("Elapsed out of range")
	}
	return q.Int64(), nil
}

func fillRawStats(r *report, values []float64, distinct map[string]struct{}) {
	if len(values) == 0 {
		return
	}
	sort.Float64s(values)
	r.DistinctValues = len(distinct)
	r.RepeatedProportion = 1 - float64(len(distinct))/float64(len(values))
	r.RawMedianSeconds = medianFloat(values)
	r.RawMaximumSeconds = values[len(values)-1]
	for _, v := range values {
		if v == 0 {
			r.ZeroValues++
		}
		if v < 1e-6 {
			r.Below1Microsecond++
		}
		if v < 1e-3 {
			r.Below1Millisecond++
		}
		if v < 1e-2 {
			r.Below10Milliseconds++
		}
		if v < .1 {
			r.Below100Milliseconds++
		}
		if v > 0 && (r.MinimumPositiveSeconds == 0 || v < r.MinimumPositiveSeconds) {
			r.MinimumPositiveSeconds = v
		}
	}
}

// fillSuiteStats computes the descriptive distribution measures reported in
// the characterization audit; these values are not partitioner inputs.
func fillSuiteStats(r *report, ns []int64) {
	if len(ns) == 0 {
		return
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
	sum := float64(0)
	for _, v := range ns {
		sum += float64(v) / 1e9
	}
	mean := sum / float64(len(ns))
	variance := 0.0
	for _, v := range ns {
		d := float64(v)/1e9 - mean
		variance += d * d
	}
	variance /= float64(len(ns))
	med := medianInt64(ns)
	r.SuiteMeanSeconds = mean
	r.SuiteMedianSeconds = float64(med) / 1e9
	r.SuiteVarianceSeconds2 = variance
	r.SuiteStdDevSeconds = math.Sqrt(variance)
	if mean > 0 {
		r.SuiteCV = r.SuiteStdDevSeconds / mean
	}
	if med > 0 {
		r.SuiteMaxOverMedian = float64(ns[len(ns)-1]) / float64(med)
	}
	desc := append([]int64(nil), ns...)
	sort.Slice(desc, func(i, j int) bool { return desc[i] > desc[j] })
	r.PackagesFor50Percent = packagesFor(desc, .5)
	r.PackagesFor80Percent = packagesFor(desc, .8)
	r.PackagesFor90Percent = packagesFor(desc, .9)
}
func packagesFor(ns []int64, fraction float64) int {
	total := int64(0)
	for _, v := range ns {
		total += v
	}
	acc := int64(0)
	for i, v := range ns {
		acc += v
		if float64(acc) >= float64(total)*fraction {
			return i + 1
		}
	}
	return 0
}
func medianInt64(v []int64) int64 {
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}
func medianFloat(v []float64) float64 {
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}
