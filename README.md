# tcc-test-partitioning

Empirical evaluation of test-suite partitioning algorithms for Go projects.
The tool compares four static partitioning strategies, Round-Robin, Quantity,
LPT, and FFD-Weighted, against Go-native baselines under the P||Cmax scheduling
model.

The project is part of an undergraduate Computer Science thesis. The
implementation intentionally uses only the Go standard library.

## What This Repository Contains

This repository contains a small research toolchain for:

- collecting package-level Go test durations;
- building pass-only characterization datasets;
- simulating partitioning strategies from historical durations;
- executing partitioned `go test` workloads locally;
- collecting sequential and Go-native parallel baselines;
- running full benchmark campaigns and exporting JSON/CSV results.

For the consolidated research and design rationale, see
[DECISIONS.md](DECISIONS.md).

## Repository Layout

```text
cmd/
  analyze/      Aggregates N go test -json runs into PackageInfo data.
  benchmark/    Experimental driver: projects x workers x algorithms x reps.
  demo/         Demonstrates all algorithms on synthetic datasets.
  gendata/      Exports deterministic synthetic fixtures as JSON.
  partitioner/  Main CLI: simulate, run, baseline-seq, baseline-par.
data/
  synthetic/         Deterministic synthetic fixtures.
  characterization/  Final pass-only package datasets for the selected projects.
  baseline/          Final pass-only baseline measurements.
internal/
  model/        Domain types: PackageInfo, Partition, PartitionResult.
  partitioner/  Implementations of the four partitioning algorithms.
  executor/     Parallel execution of go test using goroutines and channels.
  metrics/      Makespan, speedup, efficiency, and load-balance metrics.
repos/
  repos.txt     List of analyzed GitHub projects; clones live in repos/<name>.
scripts/
  collect.ps1                    Collects repeated go test -json runs.
  collect_passonly_baselines.ps1 Collects comparable pass-only baselines.
  recharacterize_all.ps1         Rebuilds characterization data for all subjects.
  run_all_campaigns.ps1          Runs the final cold and warm campaigns.
  generate_charts.py             Generates plots from benchmark results.
  triage.ps1                     Performs candidate-project triage.
benchmarks/
  example-config.json  Synthetic benchmark example.
  campaign_*.json      Final campaign configs for the selected projects.
```

Generated runtime outputs such as logs, reports, raw probes, benchmark results,
and cloned external repositories are ignored by Git.

## Requirements

- Go 1.22 or newer.
- PowerShell 7 or newer for the collection scripts.
- Python with `pandas` and `matplotlib` only for chart generation.
- GNU Make is optional and only provides convenience targets.

## Quick Validation

The fastest way to validate the tool does not require cloning external
projects or running real test suites.

```powershell
go run ./cmd/gendata -profile all
go run ./cmd/demo --output-json reports/demo.json
go run ./cmd/benchmark --config benchmarks/example-config.json
```

This generates deterministic synthetic datasets, runs all four algorithms, and
writes structured JSON/CSV reports.

## Main CLI

### Simulate from a characterization file

```powershell
go run ./cmd/partitioner --mode simulate `
  --algorithm all `
  --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq-passonly.json `
  --output-json reports/cli-simulate-w4.json
```

`simulate` does not execute `go test`; it computes planned schedules and
metrics from previously collected durations.

### Run a partitioned execution

```powershell
go run ./cmd/partitioner --mode run --warm-cache `
  --algorithm ffd `
  --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq-warm-passonly.json `
  --project-path repos/cli `
  --output-json reports/cli-ffd-w4-warm.json
```

`run` partitions the package list and executes one `go test` process per worker.
Each worker is restricted to `-p 1 -parallel 1` to preserve the scheduling model
and avoid local over-parallelism.

## Data Collection Workflow

### 1. Clone or place subject repositories

The selected projects are listed in `repos/repos.txt`. Local clones are expected
under `repos/<name>`, for example `repos/cli` or `repos/grpc-go`.

### 2. Characterize a project

```powershell
pwsh scripts/collect.ps1 -ProjectPath repos/cli -ProjectName cli -Runs 10
```

This runs `go test -json -p 1 -parallel 1 -count=1` repeatedly, stores raw probe
files under `data/probe/<project>/`, and writes the aggregated pass-only dataset
to `data/characterization/<project>.json`.

### 3. Collect pass-only baselines

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/collect_passonly_baselines.ps1 -TimeoutMinutes 60
```

The baseline commands use the same package list as the characterization file.
This keeps `T1` and `Tp` comparable when computing speedup.

### 4. Run benchmark campaigns

```powershell
go run ./cmd/benchmark --config benchmarks/campaign_cli_warm.json
```

A benchmark run writes:

- `config.json`: resolved configuration copy;
- `results.json`: full structured report;
- `raw.csv`: one row per repetition;
- `aggregate.csv`: summary by project, algorithm, and worker count.

For the complete set of final campaigns:

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1 -TimeoutMinutes 90
```

## Selected Subject Projects

| Project | Pass-only packages | Characterization file |
| --- | ---: | --- |
| cli/cli | 233 | `data/characterization/cli.json` |
| goreleaser/goreleaser | 116 | `data/characterization/goreleaser.json` |
| grpc/grpc-go | 137 | `data/characterization/grpc-go.json` |
| gohugoio/hugo | 142 | `data/characterization/hugo.json` |

Only packages that pass under the characterization regime are included in the
final experiments.

## Testing

```powershell
go test ./cmd/... ./internal/... ./data/synthetic
go vet ./...
```

Avoid `go test ./...` as a blanket test command if external repositories are
cloned under `repos/`. The scoped test command above validates this tool only.

## JSON Conventions

All generated JSON follows the same conventions:

- field names use `snake_case`;
- `time.Duration` values are serialized as nanoseconds with `_ns` suffixes;
- `time.Time` values use RFC3339 formatting;
- optional fields use `omitempty` where appropriate.