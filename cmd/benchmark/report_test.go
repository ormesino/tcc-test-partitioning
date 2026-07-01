package main

import "testing"

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
