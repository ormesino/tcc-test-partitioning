package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"time"
)

// rawRecord captures the outcome of a single rep for one
// (project, algorithm, workers) combination. One row per rep.
//
// Pointer fields for the "exec_*" group are nil in simulate mode and
// populated only when mode == "run".
type rawRecord struct {
	Project   string `json:"project"`
	Mode      string `json:"mode"`
	Algorithm string `json:"algorithm"`
	Workers   int    `json:"workers"`
	Rep       int    `json:"rep"`

	PlannedMakespanNS      int64   `json:"planned_makespan_ns"`
	PlannedSpeedup         float64 `json:"planned_speedup"`
	PlannedEfficiency      float64 `json:"planned_efficiency"`
	PlannedLoadStdDevS     float64 `json:"planned_load_stddev_s"`
	PartitioningOverheadNS int64   `json:"partitioning_overhead_ns"`

	ExecMakespanNS  *int64   `json:"exec_makespan_ns,omitempty"`
	ExecSpeedup     *float64 `json:"exec_speedup,omitempty"`
	ExecEfficiency  *float64 `json:"exec_efficiency,omitempty"`
	ExecLoadStdDevS *float64 `json:"exec_load_stddev_s,omitempty"`
	ExecError       string   `json:"exec_error,omitempty"`
}

// aggregateRecord summarizes all reps for one
// (project, algorithm, workers) combination.
type aggregateRecord struct {
	Project   string `json:"project"`
	Mode      string `json:"mode"`
	Algorithm string `json:"algorithm"`
	Workers   int    `json:"workers"`
	Reps      int    `json:"reps"`

	// Planned makespan is deterministic in simulate mode, so median
	// equals every rep. We still emit it for uniform downstream
	// processing.
	PlannedMakespanMedianNS int64 `json:"planned_makespan_median_ns"`

	// Partitioning overhead varies even in simulate mode (CPU jitter).
	PartitioningOverheadMedianNS int64 `json:"partitioning_overhead_median_ns"`
	PartitioningOverheadMinNS    int64 `json:"partitioning_overhead_min_ns"`
	PartitioningOverheadMaxNS    int64 `json:"partitioning_overhead_max_ns"`

	// Execution stats — populated only when at least one rep produced
	// a successful execution measurement.
	ExecMakespanMedianNS  *int64   `json:"exec_makespan_median_ns,omitempty"`
	ExecMakespanMinNS     *int64   `json:"exec_makespan_min_ns,omitempty"`
	ExecMakespanMaxNS     *int64   `json:"exec_makespan_max_ns,omitempty"`
	ExecMakespanMeanNS    *int64   `json:"exec_makespan_mean_ns,omitempty"`
	ExecMakespanStdDevNS  *int64   `json:"exec_makespan_stddev_ns,omitempty"`
	ExecSpeedupMedian     *float64 `json:"exec_speedup_median,omitempty"`
	ExecErrorCount        int      `json:"exec_error_count,omitempty"`
}

// fullReport is the top-level JSON written to results.json.
type fullReport struct {
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Config     Config            `json:"config"`
	Raw        []rawRecord       `json:"raw"`
	Aggregate  []aggregateRecord `json:"aggregate"`
}

// aggregate groups raw records by (project, algorithm, workers) and
// computes summary statistics. Input order is irrelevant; output is
// sorted by (project, workers, algorithm) for stable diffs.
func aggregate(mode string, raw []rawRecord) []aggregateRecord {
	type key struct {
		project, algorithm string
		workers            int
	}
	groups := make(map[key][]rawRecord)
	for _, r := range raw {
		k := key{r.Project, r.Algorithm, r.Workers}
		groups[k] = append(groups[k], r)
	}

	out := make([]aggregateRecord, 0, len(groups))
	for k, reps := range groups {
		out = append(out, summarize(mode, k.project, k.algorithm, k.workers, reps))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].Workers != out[j].Workers {
			return out[i].Workers < out[j].Workers
		}
		return out[i].Algorithm < out[j].Algorithm
	})

	return out
}

// summarize collapses N rep records into one aggregate.
func summarize(mode, project, algorithm string, workers int, reps []rawRecord) aggregateRecord {
	plannedMakespans := make([]int64, len(reps))
	overheads := make([]int64, len(reps))

	var execMakespans []int64
	var execSpeedups []float64
	var errCount int

	for i, r := range reps {
		plannedMakespans[i] = r.PlannedMakespanNS
		overheads[i] = r.PartitioningOverheadNS
		if r.ExecError != "" {
			errCount++
		}
		if r.ExecMakespanNS != nil {
			execMakespans = append(execMakespans, *r.ExecMakespanNS)
		}
		if r.ExecSpeedup != nil {
			execSpeedups = append(execSpeedups, *r.ExecSpeedup)
		}
	}

	ar := aggregateRecord{
		Project:                      project,
		Mode:                         mode,
		Algorithm:                    algorithm,
		Workers:                      workers,
		Reps:                         len(reps),
		PlannedMakespanMedianNS:      medianInt64(plannedMakespans),
		PartitioningOverheadMedianNS: medianInt64(overheads),
		PartitioningOverheadMinNS:    minInt64(overheads),
		PartitioningOverheadMaxNS:    maxInt64(overheads),
		ExecErrorCount:               errCount,
	}

	if len(execMakespans) > 0 {
		med := medianInt64(execMakespans)
		mn := minInt64(execMakespans)
		mx := maxInt64(execMakespans)
		mean := meanInt64(execMakespans)
		std := stddevInt64(execMakespans, mean)
		ar.ExecMakespanMedianNS = &med
		ar.ExecMakespanMinNS = &mn
		ar.ExecMakespanMaxNS = &mx
		ar.ExecMakespanMeanNS = &mean
		ar.ExecMakespanStdDevNS = &std
	}
	if len(execSpeedups) > 0 {
		s := medianFloat(execSpeedups)
		ar.ExecSpeedupMedian = &s
	}
	return ar
}

// writeFullReport serializes the report as indented JSON.
func writeFullReport(path string, r fullReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// writeRawCSV emits one row per rep, with stable column order suitable
// for spreadsheets and plotting tools.
func writeRawCSV(path string, raw []rawRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"project", "mode", "algorithm", "workers", "rep",
		"planned_makespan_ns", "planned_speedup", "planned_efficiency",
		"planned_load_stddev_s", "partitioning_overhead_ns",
		"exec_makespan_ns", "exec_speedup", "exec_efficiency",
		"exec_load_stddev_s", "exec_error",
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range raw {
		row := []string{
			r.Project, r.Mode, r.Algorithm,
			strconv.Itoa(r.Workers), strconv.Itoa(r.Rep),
			strconv.FormatInt(r.PlannedMakespanNS, 10),
			formatFloat(r.PlannedSpeedup),
			formatFloat(r.PlannedEfficiency),
			formatFloat(r.PlannedLoadStdDevS),
			strconv.FormatInt(r.PartitioningOverheadNS, 10),
			optInt64(r.ExecMakespanNS),
			optFloat(r.ExecSpeedup),
			optFloat(r.ExecEfficiency),
			optFloat(r.ExecLoadStdDevS),
			r.ExecError,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// writeAggregateCSV emits one row per (project, alg, workers).
func writeAggregateCSV(path string, agg []aggregateRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"project", "mode", "algorithm", "workers", "reps",
		"planned_makespan_median_ns",
		"partitioning_overhead_median_ns",
		"partitioning_overhead_min_ns",
		"partitioning_overhead_max_ns",
		"exec_makespan_median_ns", "exec_makespan_min_ns",
		"exec_makespan_max_ns", "exec_makespan_mean_ns",
		"exec_makespan_stddev_ns", "exec_speedup_median",
		"exec_error_count",
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range agg {
		row := []string{
			r.Project, r.Mode, r.Algorithm,
			strconv.Itoa(r.Workers), strconv.Itoa(r.Reps),
			strconv.FormatInt(r.PlannedMakespanMedianNS, 10),
			strconv.FormatInt(r.PartitioningOverheadMedianNS, 10),
			strconv.FormatInt(r.PartitioningOverheadMinNS, 10),
			strconv.FormatInt(r.PartitioningOverheadMaxNS, 10),
			optInt64(r.ExecMakespanMedianNS),
			optInt64(r.ExecMakespanMinNS),
			optInt64(r.ExecMakespanMaxNS),
			optInt64(r.ExecMakespanMeanNS),
			optInt64(r.ExecMakespanStdDevNS),
			optFloat(r.ExecSpeedupMedian),
			strconv.Itoa(r.ExecErrorCount),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// -- numeric helpers -------------------------------------------------

func medianInt64(in []int64) int64 {
	if len(in) == 0 {
		return 0
	}
	s := append([]int64(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianFloat(in []float64) float64 {
	if len(in) == 0 {
		return 0
	}
	s := append([]float64(nil), in...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func minInt64(in []int64) int64 {
	m := in[0]
	for _, v := range in[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxInt64(in []int64) int64 {
	m := in[0]
	for _, v := range in[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func meanInt64(in []int64) int64 {
	var sum int64
	for _, v := range in {
		sum += v
	}
	return sum / int64(len(in))
}

// stddevInt64 returns the population standard deviation of int64
// samples, rounded to the nearest int64. Suitable for nanosecond
// durations where sub-nanosecond precision is meaningless.
func stddevInt64(in []int64, mean int64) int64 {
	var sq float64
	for _, v := range in {
		d := float64(v - mean)
		sq += d * d
	}
	return int64(math.Round(math.Sqrt(sq / float64(len(in)))))
}

// -- CSV cell formatters ---------------------------------------------

func formatFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return ""
	}
	return strconv.FormatFloat(f, 'f', 6, 64)
}

func optInt64(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

func optFloat(p *float64) string {
	if p == nil {
		return ""
	}
	return formatFloat(*p)
}
