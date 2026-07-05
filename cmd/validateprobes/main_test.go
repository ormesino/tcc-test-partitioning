package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProbeFixture(t *testing.T, dir, name, body, meta string) string {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestValidateCompleteRunWithSkipReturnsWarning(t *testing.T) {
	dir := t.TempDir()
	path := writeProbeFixture(t, dir, "run_01",
		"{\"Action\":\"pass\",\"Package\":\"example/a\",\"Elapsed\":1}\n"+
			"{\"Action\":\"skip\",\"Package\":\"example/b\",\"Elapsed\":0}\n",
		"{\"exit_code\":0,\"timed_out\":false}")

	report := validate(dir, "./...", 1, []string{"example/a", "example/b"}, []string{path})
	if report.Status != "WARN" {
		t.Fatalf("status=%s, want WARN: %+v", report.Status, report)
	}
	if report.CompleteRuns != 1 || report.TotalSkipStatuses != 1 || report.TotalMissing != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestValidateMalformedAndMissingRunFails(t *testing.T) {
	dir := t.TempDir()
	path := writeProbeFixture(t, dir, "run_01",
		"not json\n{\"Action\":\"pass\",\"Package\":\"example/a\",\"Elapsed\":1}\n", "")

	report := validate(dir, "./...", 1, []string{"example/a", "example/b"}, []string{path})
	if report.Status != "FAIL" {
		t.Fatalf("status=%s, want FAIL", report.Status)
	}
	joined := strings.Join(report.FatalFindings, " ")
	if !strings.Contains(joined, "malformed") || !strings.Contains(joined, "missing terminal") {
		t.Fatalf("fatal findings=%v", report.FatalFindings)
	}
}
