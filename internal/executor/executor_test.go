package executor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tcc-test-partitioning/internal/model"
)

// TestBaselineReport_RoundTrip exercises ADR-011: a baseline report
// persisted by WriteBaselineReport must be losslessly recoverable by
// LoadBaselineReport, including the Duration field (which carries the
// snake_case + _ns convention from ADR-014).
func TestBaselineReport_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	want := BaselineReport{
		Mode:        "baseline-seq",
		Parallelism: 1,
		Duration:    1234567890 * time.Nanosecond,
		MeasuredAt:  time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC),
		ProjectPath: "C:/src/cli",
	}

	if err := WriteBaselineReport(path, want); err != nil {
		t.Fatalf("WriteBaselineReport: %v", err)
	}

	got, err := LoadBaselineReport(path)
	if err != nil {
		t.Fatalf("LoadBaselineReport: %v", err)
	}

	if got.Mode != want.Mode ||
		got.Parallelism != want.Parallelism ||
		got.Duration != want.Duration ||
		!got.MeasuredAt.Equal(want.MeasuredAt) ||
		got.ProjectPath != want.ProjectPath {
		t.Fatalf("round-trip mismatch:\nwant=%+v\n got=%+v", want, got)
	}
}

func TestLoadBaselineReport_MissingFile(t *testing.T) {
	_, err := LoadBaselineReport(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestRunPartitioned_EmptyPartitions covers the short-circuit path in
// runWorker (no packages assigned). It exercises the concurrency model
// (one goroutine per worker, channel collection, WaitGroup close) end
// to end without requiring a Go toolchain to be reachable.
func TestRunPartitioned_EmptyPartitions(t *testing.T) {
	partResult := model.PartitionResult{
		Algorithm: "test",
		Workers:   3,
		Partitions: []model.Partition{
			{WorkerID: 0},
			{WorkerID: 1},
			{WorkerID: 2},
		},
	}

	cfg := Config{ProjectPath: t.TempDir(), Count: 1}
	er := RunPartitioned(cfg, partResult)

	if er.Mode != "partitioned" {
		t.Errorf("Mode = %q, want %q", er.Mode, "partitioned")
	}
	if er.Workers != 3 {
		t.Errorf("Workers = %d, want 3", er.Workers)
	}
	if got := len(er.WorkerResults); got != 3 {
		t.Fatalf("len(WorkerResults) = %d, want 3", got)
	}
	if er.TotalElapsed != 0 {
		t.Errorf("TotalElapsed = %v, want 0 (no packages)", er.TotalElapsed)
	}
	for i, wr := range er.WorkerResults {
		if wr.WorkerID != i {
			t.Errorf("WorkerResults[%d].WorkerID = %d, want %d",
				i, wr.WorkerID, i)
		}
		if wr.PackageCount != 0 || wr.Elapsed != 0 || wr.Error != nil {
			t.Errorf("WorkerResults[%d] = %+v, want zero values", i, wr)
		}
	}
}

func TestFormatExecutionResult_ContainsKeyFields(t *testing.T) {
	er := ExecutionResult{
		Mode:         "baseline-seq",
		Workers:      1,
		Makespan:     500 * time.Millisecond,
		TotalElapsed: 500 * time.Millisecond,
		WorkerResults: []WorkerResult{
			{WorkerID: 0, PackageCount: 7, Elapsed: 500 * time.Millisecond},
		},
	}

	got := FormatExecutionResult(er)

	for _, want := range []string{
		"baseline-seq", "Workers: 1", "Makespan", "500ms", "Worker",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}
