package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tcc-test-partitioning/internal/executor"
	"tcc-test-partitioning/internal/model"
	"tcc-test-partitioning/internal/partitioner"
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
		DataFileSHA256: hash, CacheRegime: "warm",
	}
	if err := validateBaselineReport("baseline.json", valid, 2, tmpFile.Name(), true); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}

	invalid := valid
	invalid.Success = false
	if err := validateBaselineReport("baseline.json", invalid, 2, tmpFile.Name(), true); err == nil || !strings.Contains(err.Error(), "success=false") {
		t.Fatalf("error = %v, want success=false", err)
	}
	invalid = valid
	invalid.PackageCount = 1
	if err := validateBaselineReport("baseline.json", invalid, 2, tmpFile.Name(), true); err == nil || !strings.Contains(err.Error(), "expected 2") {
		t.Fatalf("error = %v, want population mismatch", err)
	}
}

func TestConfigValidateRunRequiresBaseline(t *testing.T) {
	cfg := Config{Mode: "run", Workers: []int{2}, Algorithms: []string{"lpt"}, Repetitions: 1, OutputDir: "out", Projects: []ProjectSpec{{Name: "p", DataFile: "data.json", ProjectPath: "project"}}}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "baseline_seq_file") {
		t.Fatalf("validate error = %v, want missing baseline_seq_file", err)
	}
}

func TestConfigValidateRunRequiresParallelBaselines(t *testing.T) {
	cfg := Config{
		Mode: "run", Workers: []int{2, 4}, Algorithms: []string{"lpt"},
		Repetitions: 1, OutputDir: "out",
		Projects: []ProjectSpec{{Name: "p", DataFile: "data.json", ProjectPath: "project", BaselineSeqFile: "seq.json", BaselineParFiles: map[string]string{"2": "p2.json"}}},
	}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), `baseline_par_files["4"]`) {
		t.Fatalf("validate error = %v, want missing p=4 native baseline", err)
	}
}

func TestRunOneSeparatesPlannedAndMeasuredT1(t *testing.T) {
	packages := []model.PackageInfo{{Name: "a", Duration: 4 * time.Second}, {Name: "b", Duration: 6 * time.Second}}
	rec := runOne(Config{Mode: "simulate"}, ProjectSpec{Name: "p"}, packages, 10*time.Second, 100*time.Second, &partitioner.LPT{}, 2, 1, 3)
	if rec.PlannedMakespanNS != int64(6*time.Second) {
		t.Fatalf("planned makespan = %d, want %d", rec.PlannedMakespanNS, 6*time.Second)
	}
	if rec.PlannedSpeedup != 10.0/6.0 {
		t.Fatalf("planned speedup = %v, want %v", rec.PlannedSpeedup, 10.0/6.0)
	}
}

func TestMakeRunDirRejectsCollision(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	first, err := makeRunDir(base, now)
	if err != nil {
		t.Fatalf("first makeRunDir: %v", err)
	}
	if first != filepath.Join(base, "20260628-120000") {
		t.Fatalf("run dir = %q", first)
	}
	if _, err := makeRunDir(base, now); err == nil {
		t.Fatal("second makeRunDir unexpectedly reused an existing directory")
	}
}

func TestFinalConfigsReferenceValidNativeBaselines(t *testing.T) {
	if os.Getenv("TCC_VALIDATE_CAMPAIGN_ARTIFACTS") != "1" {
		t.Skip("set TCC_VALIDATE_CAMPAIGN_ARTIFACTS=1 to validate campaign artifacts")
	}
	root := filepath.Join("..", "..")
	configs, err := filepath.Glob(filepath.Join(root, "benchmarks", "campaign_*.json"))
	if err != nil || len(configs) != 8 {
		t.Fatalf("final configs = %d, err=%v; want 8", len(configs), err)
	}
	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg, err := loadConfig(path)
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			project := cfg.Projects[0]
			project.DataFile = filepath.Join(root, filepath.FromSlash(project.DataFile))
			project.BaselineSeqFile = filepath.Join(root, filepath.FromSlash(project.BaselineSeqFile))
			for workers, baseline := range project.BaselineParFiles {
				project.BaselineParFiles[workers] = filepath.Join(root, filepath.FromSlash(baseline))
			}
			packages, err := loadPackages(project.DataFile)
			if err != nil {
				t.Fatalf("loadPackages: %v", err)
			}
			seq, _ := resolveT1(packages, project.BaselineSeqFile, project.DataFile, cfg.WarmCache)
			if records, err := loadNativeBaselines(cfg, project, packages, seq); err != nil || len(records) != 4 {
				t.Fatalf("native baselines = %d, err=%v; want 4", len(records), err)
			}
		})
	}
}

func TestConfigValidateDefaultsMaxAttemptsToThree(t *testing.T) {
	cfg := Config{
		Mode: "simulate", Workers: []int{2}, Algorithms: []string{"lpt"},
		Repetitions: 1, OutputDir: "out", Projects: []ProjectSpec{{Name: "p", DataFile: "data.json"}},
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", cfg.MaxAttempts)
	}
}

func TestRunWithRetriesSucceedsOnThirdAttempt(t *testing.T) {
	runs := 0
	var failedAttempts []int
	rec := runWithRetries(3, func() rawRecord {
		runs++
		if runs < 3 {
			return rawRecord{ExecError: "transient failure"}
		}
		return rawRecord{}
	}, func(attempt int, _ string, willRetry bool) {
		failedAttempts = append(failedAttempts, attempt)
		if !willRetry {
			t.Errorf("attempt %d should be retried", attempt)
		}
	})

	if runs != 3 || rec.Attempts != 3 || rec.ExecError != "" {
		t.Fatalf("runs=%d rec=%+v, want success on third attempt", runs, rec)
	}
	if len(failedAttempts) != 2 || failedAttempts[0] != 1 || failedAttempts[1] != 2 {
		t.Fatalf("failed attempts = %v, want [1 2]", failedAttempts)
	}
}

func TestRunWithRetriesReturnsFinalFailure(t *testing.T) {
	runs := 0
	finalCallbackMarked := false
	rec := runWithRetries(3, func() rawRecord {
		runs++
		return rawRecord{ExecError: "persistent failure"}
	}, func(attempt int, _ string, willRetry bool) {
		if attempt == 3 && !willRetry {
			finalCallbackMarked = true
		}
	})

	if runs != 3 || rec.Attempts != 3 || rec.ExecError == "" || !finalCallbackMarked {
		t.Fatalf("runs=%d rec=%+v finalCallback=%v", runs, rec, finalCallbackMarked)
	}
}

func TestAlgorithmOrderForRepCounterbalancesCompleteBlock(t *testing.T) {
	algorithms, err := resolveAlgorithms([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	positions := make(map[string]map[int]int)
	for rep := 1; rep <= len(algorithms); rep++ {
		for pos, alg := range algorithmOrderForRep(algorithms, rep) {
			if positions[alg.Name()] == nil {
				positions[alg.Name()] = make(map[int]int)
			}
			positions[alg.Name()][pos]++
		}
	}
	for name, counts := range positions {
		for pos := range algorithms {
			if counts[pos] != 1 {
				t.Fatalf("algorithm %s appeared %d times at position %d; want 1", name, counts[pos], pos)
			}
		}
	}
}

func TestLoadPackagesRejectsEmptyNamesAndDuplicates(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"empty-population", `[]`, "empty package population"},
		{"empty-name", `[{"name":"","duration_ns":1}]`, "empty name"},
		{"duplicate", `[{"name":"a","duration_ns":1},{"name":"a","duration_ns":2}]`, "duplicate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "packages.json")
			if err := os.WriteFile(path, []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadPackages(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want substring %q", err, tc.want)
			}
		})
	}
}
