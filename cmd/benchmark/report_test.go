package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestMinMaxInt64Empty(t *testing.T) {
	if got := minInt64(nil); got != 0 {
		t.Errorf("minInt64(nil) = %d, want 0", got)
	}
	if got := maxInt64(nil); got != 0 {
		t.Errorf("maxInt64(nil) = %d, want 0", got)
	}
}

func TestSummarizeCountsOnlySuccessfulExecutions(t *testing.T) {
	one := int64(1)
	two := int64(2)
	got := summarize("run", "p", "LPT", 2, []rawRecord{
		{ExecMakespanNS: &one},
		{ExecError: "failed", ExecMakespanNS: &two},
		{ExecMakespanNS: &two},
	})
	if got.Reps != 3 || got.ExecSuccessCount != 2 || got.ExecErrorCount != 1 {
		t.Fatalf("summary counts = reps:%d success:%d errors:%d", got.Reps, got.ExecSuccessCount, got.ExecErrorCount)
	}
}

func TestWriteNativeBaselineCSVIncludesSequentialAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native_baselines.csv")
	records := []nativeBaselineRecord{
		{Project: "p", Mode: "baseline-seq", Workers: 1, DurationNS: 100, Speedup: 1, Efficiency: 1},
		{Project: "p", Mode: "baseline-par", Workers: 2, DurationNS: 60, Speedup: 1.5, Efficiency: 0.75},
	}
	if err := writeNativeBaselineCSV(path, records); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d, want header + 2 records", len(rows))
	}
	if rows[0][1] != "mode" || rows[1][1] != "baseline-seq" || rows[2][1] != "baseline-par" {
		t.Fatalf("mode column/values = %v / %q / %q", rows[0], rows[1][1], rows[2][1])
	}
	if rows[1][2] != "1" {
		t.Fatalf("sequential workers=%q, want 1", rows[1][2])
	}
}

func TestWriteRawCSVIncludesSequencePosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.csv")
	if err := writeRawCSV(path, []rawRecord{{Project: "p", Mode: "run", Algorithm: "LPT", Workers: 2, Rep: 1, SequencePosition: 4}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][5] != "sequence_position" || rows[1][5] != "4" {
		t.Fatalf("sequence position column/value = %q/%q", rows[0][5], rows[1][5])
	}
}
