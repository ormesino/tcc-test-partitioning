package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tcc-test-partitioning/internal/partitioner"
)

// Config drives the benchmark runner. It is loaded from a JSON file
// passed via --config and may be partially overridden by CLI flags
// (see main.go).
//
// The runner executes a matrix of experiments:
//
//	projects × workers × algorithms × repetitions
//
// All combinations are independent; no state is shared across
// (project, alg, workers, rep) tuples beyond the loaded package and
// baseline data (which are immutable inputs).
type Config struct {
	// Mode selects what is measured.
	//   "simulate": partition only (no go test invoked). Deterministic;
	//               repetitions only reveal partitioning-overhead jitter.
	//   "run":      partition + execute via the executor package.
	Mode string `json:"mode"`

	// Workers is the list of worker counts (p) to sweep.
	Workers []int `json:"workers"`

	// Algorithms is the list of algorithm identifiers to run. Each
	// must resolve via resolveAlgorithm. A single entry of "all"
	// expands to every implemented algorithm.
	Algorithms []string `json:"algorithms"`

	// Repetitions is the number of independent reps per
	// (project, algorithm, workers) combination. >=1.
	Repetitions int `json:"repetitions"`

	// TimeoutMinutes is the per-worker `go test` timeout. Used only
	// in "run" mode.
	TimeoutMinutes int `json:"timeout_minutes"`

	// OutputDir is the base directory; the runner creates a
	// timestamped subdirectory inside it for each invocation.
	OutputDir string `json:"output_dir"`

	// Verbose enables `go test -v` in "run" mode.
	Verbose bool `json:"verbose,omitempty"`

	// WarmCache, when true, pre-compiles the selected package set before
	// each project run. This populates Go's build cache so workers measure
	// mostly test execution time rather than compilation.
	WarmCache bool `json:"warm_cache,omitempty"`

	// Projects is the list of subjects to benchmark.
	Projects []ProjectSpec `json:"projects"`
}

// ProjectSpec describes one subject project.
type ProjectSpec struct {
	// Name is a short identifier used in output filenames and
	// aggregated records.
	Name string `json:"name"`

	// DataFile is the path to the PackageInfo JSON (produced by
	// cmd/analyze or cmd/gendata).
	DataFile string `json:"data_file"`

	// BaselineSeqFile is the path to a BaselineReport JSON (produced
	// by `cmd/partitioner --mode baseline-seq --output FILE`). When
	// empty, T1 falls back to sum(Duration) with a stderr warning.
	BaselineSeqFile string `json:"baseline_seq_file,omitempty"`

	// ProjectPath is the Go project root passed to the executor.
	// Required in "run" mode; ignored in "simulate".
	ProjectPath string `json:"project_path,omitempty"`
}

// loadConfig reads and validates a config file.
func loadConfig(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("reading config: %w", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parsing config: %w", err)
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c *Config) validate() error {
	switch c.Mode {
	case "simulate", "run":
	default:
		return fmt.Errorf("config.mode must be \"simulate\" or \"run\" (got %q)", c.Mode)
	}
	if len(c.Workers) == 0 {
		return fmt.Errorf("config.workers must have at least one value")
	}
	for _, w := range c.Workers {
		if w < 1 {
			return fmt.Errorf("config.workers must be >= 1 (got %d)", w)
		}
	}
	if len(c.Algorithms) == 0 {
		return fmt.Errorf("config.algorithms must have at least one entry (use [\"all\"] for the full set)")
	}
	if c.Repetitions < 1 {
		return fmt.Errorf("config.repetitions must be >= 1 (got %d)", c.Repetitions)
	}
	if c.OutputDir == "" {
		return fmt.Errorf("config.output_dir is required")
	}
	if len(c.Projects) == 0 {
		return fmt.Errorf("config.projects must have at least one entry")
	}
	for i, p := range c.Projects {
		if p.Name == "" {
			return fmt.Errorf("config.projects[%d].name is required", i)
		}
		if p.DataFile == "" {
			return fmt.Errorf("config.projects[%d].data_file is required", i)
		}
		if c.Mode == "run" && p.ProjectPath == "" {
			return fmt.Errorf("config.projects[%d].project_path is required in run mode", i)
		}
	}
	return nil
}

// resolveAlgorithms expands the algorithm identifiers from a config
// (e.g. ["lpt", "ffd"] or ["all"]) into concrete Partitioner
// instances. Order is preserved; "all" expands in the canonical
// presentation order used elsewhere in the project.
func resolveAlgorithms(names []string) ([]partitioner.Partitioner, error) {
	// Special-case "all" anywhere in the list.
	for _, n := range names {
		if strings.EqualFold(n, "all") {
			return []partitioner.Partitioner{
				&partitioner.RoundRobin{},
				&partitioner.Quantity{},
				&partitioner.LPT{},
				&partitioner.FFD{},
			}, nil
		}
	}

	out := make([]partitioner.Partitioner, 0, len(names))
	for _, n := range names {
		switch strings.ToLower(n) {
		case "round-robin", "roundrobin", "rr":
			out = append(out, &partitioner.RoundRobin{})
		case "quantity", "qty":
			out = append(out, &partitioner.Quantity{})
		case "lpt":
			out = append(out, &partitioner.LPT{})
		case "ffd", "ffd-weighted":
			out = append(out, &partitioner.FFD{})
		default:
			return nil, fmt.Errorf("unknown algorithm: %q (valid: round-robin, quantity, lpt, ffd, all)", n)
		}
	}
	return out, nil
}
