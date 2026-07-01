package executor

import (
	"fmt"
	"os"
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
		Mode:           "baseline-seq",
		Parallelism:    1,
		Duration:       1234567890 * time.Nanosecond,
		MeasuredAt:     time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC),
		ProjectPath:    "C:/src/cli",
		PackageCount:   233,
		PackageSource:  "data/characterization/cli.json",
		Success:        true,
		DataFileSHA256: "deadbeef",
		CacheRegime:    "cold",
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
		got.ProjectPath != want.ProjectPath ||
		got.PackageCount != want.PackageCount ||
		got.PackageSource != want.PackageSource ||
		got.Success != want.Success ||
		got.Error != want.Error ||
		got.DataFileSHA256 != want.DataFileSHA256 ||
		got.CacheRegime != want.CacheRegime {
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

func TestAppendPackageArgs(t *testing.T) {
	base := []string{"test", "-count=1"}

	got := appendPackageArgs(append([]string{}, base...), nil)
	if want := "./..."; got[len(got)-1] != want {
		t.Fatalf("fallback package = %q, want %q", got[len(got)-1], want)
	}

	pkgs := []string{"example.com/a", "example.com/b"}
	got = appendPackageArgs(append([]string{}, base...), pkgs)
	if strings.Join(got[len(got)-2:], ",") != strings.Join(pkgs, ",") {
		t.Fatalf("packages appended = %v, want suffix %v", got, pkgs)
	}
	if count := packageCount(pkgs); count != 2 {
		t.Fatalf("packageCount = %d, want 2", count)
	}
}

func TestRunWorker_ColdCacheFailure(t *testing.T) {
	origMkdirTemp := mkdirTemp
	defer func() { mkdirTemp = origMkdirTemp }()

	// Force mkdirTemp to fail
	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", fmt.Errorf("injected permission error")
	}

	cfg := Config{
		WarmCache: false, // ensure we trigger the cold cache creation
	}
	part := model.Partition{
		WorkerID: 42,
		Packages: []model.PackageInfo{{Name: "example.com/a"}},
	}

	wr := runWorker(cfg, part)
	if wr.WorkerID != 42 {
		t.Errorf("WorkerID = %d, want 42", wr.WorkerID)
	}
	if wr.Elapsed != 0 {
		t.Errorf("Elapsed = %v, want 0", wr.Elapsed)
	}
	if wr.Error == nil || !strings.Contains(wr.Error.Error(), "failed to create cold cache") {
		t.Errorf("Error = %v, want 'failed to create cold cache'", wr.Error)
	}
}

func TestRunWorker_ColdCacheCleanupFailureIsReported(t *testing.T) {
	originalRemoveAll := removeAll
	defer func() { removeAll = originalRemoveAll }()
	removeAll = func(path string) error {
		_ = originalRemoveAll(path)
		return fmt.Errorf("injected cleanup error")
	}

	wr := runWorker(Config{ProjectPath: t.TempDir(), Count: 1, WarmCache: false}, model.Partition{
		WorkerID: 0,
		Packages: []model.PackageInfo{{Name: "example.invalid/package"}},
	})
	if wr.Error == nil || !strings.Contains(wr.Error.Error(), "failed to remove cold cache") {
		t.Fatalf("Error = %v, want cleanup failure", wr.Error)
	}
}

func TestWriteBaselineReport_RefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	first := BaselineReport{Mode: "baseline-seq", Duration: time.Second, Success: true}
	if err := WriteBaselineReport(path, first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteBaselineReport(path, BaselineReport{Mode: "baseline-par", Duration: 2 * time.Second, Success: true}); err == nil {
		t.Fatal("second write unexpectedly replaced an existing baseline")
	}
	got, err := LoadBaselineReport(path)
	if err != nil {
		t.Fatalf("load preserved baseline: %v", err)
	}
	if got.Mode != first.Mode || got.Duration != first.Duration {
		t.Fatalf("existing baseline changed: got=%+v want=%+v", got, first)
	}
}

func TestRunBaselineSeq_ColdCacheFailurePreventsExecution(t *testing.T) {
	originalMkdirTemp := mkdirTemp
	originalRunGoTest := runGoTestCommand
	defer func() {
		mkdirTemp = originalMkdirTemp
		runGoTestCommand = originalRunGoTest
	}()

	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", fmt.Errorf("injected cache creation error")
	}
	executed := false
	runGoTestCommand = func(Config, []string, []string) (string, error) {
		executed = true
		return "", nil
	}

	result := RunBaselineSeqPackages(Config{WarmCache: false}, []string{"example.com/a"})
	if executed {
		t.Fatal("go test executed after cold cache creation failed")
	}
	if result.WorkerResults[0].Error == nil || !strings.Contains(result.WorkerResults[0].Error.Error(), "failed to create cold cache") {
		t.Fatalf("error = %v, want cold cache creation failure", result.WorkerResults[0].Error)
	}
}

func TestRunPartitioned_MakespanExcludesColdCacheCleanup(t *testing.T) {
	originalRunGoTest := runGoTestCommand
	originalRemoveAll := removeAll
	defer func() {
		runGoTestCommand = originalRunGoTest
		removeAll = originalRemoveAll
	}()

	var cacheDir string
	runGoTestCommand = func(_ Config, _ []string, env []string) (string, error) {
		for _, entry := range env {
			if strings.HasPrefix(entry, "GOCACHE=") {
				cacheDir = strings.TrimPrefix(entry, "GOCACHE=")
			}
		}
		if cacheDir == "" {
			return "", fmt.Errorf("isolated GOCACHE not provided")
		}
		if _, err := os.Stat(cacheDir); err != nil {
			return "", fmt.Errorf("cold cache unavailable during execution: %w", err)
		}
		time.Sleep(5 * time.Millisecond)
		return "ok", nil
	}
	removeAll = func(path string) error {
		time.Sleep(80 * time.Millisecond)
		return originalRemoveAll(path)
	}

	started := time.Now()
	result := RunPartitioned(Config{WarmCache: false}, model.PartitionResult{
		Workers: 1,
		Partitions: []model.Partition{{
			WorkerID: 0,
			Packages: []model.PackageInfo{{Name: "example.com/a"}},
		}},
	})
	wallClock := time.Since(started)

	if result.WorkerResults[0].Error != nil {
		t.Fatalf("worker error: %v", result.WorkerResults[0].Error)
	}
	if result.Makespan >= 50*time.Millisecond {
		t.Fatalf("makespan %v includes the 80ms cleanup", result.Makespan)
	}
	if wallClock < 80*time.Millisecond {
		t.Fatalf("test did not exercise delayed cleanup; wall-clock=%v", wallClock)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cold cache still exists after cleanup: err=%v", err)
	}
}

func TestWithEnvValue_ReplacesCaseInsensitiveDuplicates(t *testing.T) {
	got := withEnvValue([]string{"Path=C:/bin", "gocache=old", "GOCACHE=older"}, "GOCACHE", "isolated")
	count := 0
	for _, entry := range got {
		if strings.EqualFold(strings.SplitN(entry, "=", 2)[0], "GOCACHE") {
			count++
			if entry != "GOCACHE=isolated" {
				t.Fatalf("GOCACHE entry = %q, want canonical isolated value", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("GOCACHE entries = %d, want exactly 1: %v", count, got)
	}
}
