package metrics

import (
	"math"
	"testing"
	"time"

	"tcc-test-partitioning/internal/model"
)

// mkResult builds a synthetic PartitionResult with the given
// per-worker loads (in milliseconds). Used by table-driven tests
// to avoid coupling to the partitioner implementations.
func mkResult(loadsMs ...int) model.PartitionResult {
	parts := make([]model.Partition, len(loadsMs))
	var maxLoad time.Duration
	for i, ms := range loadsMs {
		d := time.Duration(ms) * time.Millisecond
		parts[i] = model.Partition{
			WorkerID: i,
			Load:     d,
		}
		if d > maxLoad {
			maxLoad = d
		}
	}
	return model.PartitionResult{
		Algorithm:  "test",
		Workers:    len(loadsMs),
		Partitions: parts,
		Makespan:   maxLoad,
	}
}

func TestMakespan_EmptyResult(t *testing.T) {
	r := model.PartitionResult{}
	if got := Makespan(r); got != 0 {
		t.Errorf("Makespan(empty) = %v, want 0", got)
	}
}

func TestMakespan_PicksMaximumLoad(t *testing.T) {
	r := mkResult(100, 500, 250, 750, 300)
	if got, want := Makespan(r), 750*time.Millisecond; got != want {
		t.Errorf("Makespan = %v, want %v", got, want)
	}
}

func TestLoadVariance_PerfectBalance(t *testing.T) {
	// All workers carry the same load → variance must be exactly 0.
	r := mkResult(500, 500, 500, 500)
	if got := LoadVariance(r); got != 0 {
		t.Errorf("variance with perfect balance = %v, want 0", got)
	}
}

func TestLoadVariance_KnownValue(t *testing.T) {
	// Loads in seconds: {1, 2, 3, 4} → mean = 2.5
	// population variance = mean((1-2.5)^2 + (2-2.5)^2 + (3-2.5)^2 + (4-2.5)^2)
	//                     = mean(2.25 + 0.25 + 0.25 + 2.25) = 5.0 / 4 = 1.25
	r := mkResult(1000, 2000, 3000, 4000)
	got := LoadVariance(r)
	want := 1.25
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("variance = %v, want %v", got, want)
	}
}

func TestLoadStdDev_IsSqrtOfVariance(t *testing.T) {
	r := mkResult(1000, 2000, 3000, 4000)
	if got, want := LoadStdDev(r), math.Sqrt(1.25); math.Abs(got-want) > 1e-9 {
		t.Errorf("stddev = %v, want %v", got, want)
	}
}

func TestCompute_SpeedupAndEfficiency(t *testing.T) {
	// 4 workers, balanced at 250ms each → makespan = 250ms.
	// Sequential T1 = 1000ms → Speedup = 4, Efficiency = 1.
	r := mkResult(250, 250, 250, 250)
	rep := Compute(r, time.Second)

	if got, want := rep.Speedup, 4.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("Speedup = %v, want %v", got, want)
	}
	if got, want := rep.Efficiency, 1.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("Efficiency = %v, want %v", got, want)
	}
}

func TestCompute_ZeroT1ProducesZeroSpeedup(t *testing.T) {
	// When T1 is unknown / unmeasured, Speedup and Efficiency must
	// default to 0 (not NaN, not Inf) to be safe for JSON output.
	r := mkResult(100, 200, 300)
	rep := Compute(r, 0)

	if rep.Speedup != 0 {
		t.Errorf("Speedup with T1=0 = %v, want 0", rep.Speedup)
	}
	if rep.Efficiency != 0 {
		t.Errorf("Efficiency with T1=0 = %v, want 0", rep.Efficiency)
	}
}

func TestTotalDuration_SumsAllPackages(t *testing.T) {
	r := model.PartitionResult{
		Partitions: []model.Partition{
			{Packages: []model.PackageInfo{
				{Duration: 100 * time.Millisecond},
				{Duration: 200 * time.Millisecond},
			}},
			{Packages: []model.PackageInfo{
				{Duration: 300 * time.Millisecond},
			}},
		},
	}
	if got, want := TotalDuration(r), 600*time.Millisecond; got != want {
		t.Errorf("TotalDuration = %v, want %v", got, want)
	}
}
