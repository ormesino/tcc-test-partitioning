package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"tcc-test-partitioning/internal/executor"
)

func hashFileForTest(path string) string {
	f, _ := os.Open(path)
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func TestValidateBaselineReport(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "data*.json")
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	hash := hashFileForTest(tmpFile.Name())

	valid := executor.BaselineReport{
		Mode: "baseline-seq", Duration: time.Second, PackageCount: 2,
		PackageSource: "data/characterization/example.json", Success: true,
		DataFileSHA256: hash, CacheRegime: "cold",
	}
	if err := validateBaselineReport("baseline.json", valid, 2, tmpFile.Name(), false); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*executor.BaselineReport)
		want string
	}{
		{"wrong mode", func(r *executor.BaselineReport) { r.Mode = "baseline-par" }, "expected 'baseline-seq'"},
		{"failed", func(r *executor.BaselineReport) { r.Success = false }, "success=false"},
		{"wrong population", func(r *executor.BaselineReport) { r.PackageCount = 3 }, "expected 2"},
		{"legacy scope", func(r *executor.BaselineReport) { r.PackageSource = "./..." }, "not pass-only"},
		{"wrong hash", func(r *executor.BaselineReport) { r.DataFileSHA256 = "bad" }, "expected"},
		{"wrong regime", func(r *executor.BaselineReport) { r.CacheRegime = "warm" }, "expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid
			tt.edit(&r)
			if err := validateBaselineReport("baseline.json", r, 2, tmpFile.Name(), false); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
