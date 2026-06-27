package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseRun(t *testing.T) {
	input := "\ufeff{\"Action\":\"pass\",\"Package\":\"example/a\",\"Elapsed\":1.25}\n" +
		"compiler noise\n" +
		"{\"Action\":\"pass\",\"Package\":\"example/a\",\"Test\":\"TestOne\",\"Elapsed\":9}\n" +
		"{\"Action\":\"fail\",\"Package\":\"example/b\",\"Elapsed\":0.5}\n"

	got, err := parseRun(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseRun() error = %v", err)
	}
	if got["example/a"].Status != "pass" || got["example/a"].Elapsed != 1250*time.Millisecond {
		t.Errorf("example/a = %+v, want pass with 1.25s", got["example/a"])
	}
	if got["example/b"].Status != "fail" || got["example/b"].Elapsed != 500*time.Millisecond {
		t.Errorf("example/b = %+v, want fail with 500ms", got["example/b"])
	}
}

func TestAggregate(t *testing.T) {
	runs := []map[string]pkgOutcome{
		{
			"a":         {Status: "pass", Elapsed: time.Second},
			"b-fail":    {Status: "fail", Elapsed: time.Second},
			"c-skip":    {Status: "skip", Elapsed: time.Second},
			"d-missing": {Status: "pass", Elapsed: time.Second},
		},
		{
			"a":      {Status: "pass", Elapsed: 3 * time.Second},
			"b-fail": {Status: "pass", Elapsed: time.Second},
			"c-skip": {Status: "pass", Elapsed: time.Second},
		},
	}

	got := aggregate(runs, false)
	if len(got) != 1 {
		t.Fatalf("len(aggregate) = %d, want 1", len(got))
	}
	if got[0].Name != "a" || got[0].Duration != 2*time.Second {
		t.Errorf("aggregate[0] = %+v, want package a with median 2s", got[0])
	}
}

func TestMedian(t *testing.T) {
	in := []time.Duration{3 * time.Second, time.Second, 2 * time.Second, 4 * time.Second}
	if got, want := median(in), 2500*time.Millisecond; got != want {
		t.Errorf("median(even) = %v, want %v", got, want)
	}
	if got, want := median(in[:3]), 2*time.Second; got != want {
		t.Errorf("median(odd) = %v, want %v", got, want)
	}
	if in[0] != 3*time.Second {
		t.Errorf("median mutated input: first element = %v", in[0])
	}
}
