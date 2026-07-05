package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditReconcilesMillisecondQuantization(t *testing.T) {
	d := t.TempDir()
	runs := make([]string, 10)
	for i := range runs {
		runs[i] = filepath.Join(d, "run"+string(rune('a'+i))+".json")
		body := "{\"Action\":\"pass\",\"Package\":\"p\",\"Elapsed\":0.0019999999999999996}\n{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"T\",\"Elapsed\":9}\n"
		if err := os.WriteFile(runs[i], []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	characterization := filepath.Join(d, "char.json")
	if err := os.WriteFile(characterization, []byte("[{\"name\":\"p\",\"duration_ns\":2000000}]"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := audit(runs, characterization, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Divergences != 0 || r.PassOnlyPackages != 1 || r.IndividualTestTerminalEvents != 10 {
		t.Fatalf("report=%+v", r)
	}
}

func TestAuditRejectsMalformedProbe(t *testing.T) {
	d := t.TempDir()
	probe := filepath.Join(d, "run.json")
	os.WriteFile(probe, []byte("not-json\n"), 0o644)
	char := filepath.Join(d, "char.json")
	os.WriteFile(char, []byte("[]"), 0o644)
	if _, err := audit([]string{probe}, char, 1); err == nil {
		t.Fatal("expected malformed probe error")
	}
}

func TestAuditRejectsPopulationMismatch(t *testing.T) {
	d := t.TempDir()
	probe := filepath.Join(d, "run.json")
	os.WriteFile(probe, []byte("{\"Action\":\"pass\",\"Package\":\"p\",\"Elapsed\":0.01}\n"), 0o644)
	char := filepath.Join(d, "char.json")
	os.WriteFile(char, []byte("[]"), 0o644)
	if _, err := audit([]string{probe}, char, 1); err == nil {
		t.Fatal("expected population mismatch")
	}
}
