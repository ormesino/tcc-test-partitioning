# tcc-test-partitioning

Empirical evaluation of test-suite partitioning algorithms for Go projects.
The tool compares four static partitioning strategies, Round-Robin, Quantity,
LPT, and FFD-Multifit, against Go-native baselines under the P||Cmax scheduling
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
[DECISIONS.md](DECISIONS.md). The private working-document index and document
hierarchy are in [docs/README.md](docs/README.md).

## Repository Layout

```text
cmd/
  analyze/      Aggregates N go test -json runs into PackageInfo data.
  auditdurations/ Independently reconciles probes, medians and suite metrics.
  benchmark/    Experimental driver: projects x workers x algorithms x reps.
  demo/         Demonstrates all algorithms on synthetic datasets.
  gendata/      Exports deterministic synthetic fixtures as JSON.
  partitioner/  Main CLI: simulate, run, baseline-seq, baseline-par.
  preflight/    Verifies effective child-process GOMAXPROCS.
  validateprobes/ Retroactive PASS/WARN/FAIL integrity check for probes.
  workerdiag/   Completed non-canonical diagnostic that motivated GOMAXPROCS=1.
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
  triage.ps1                     Historical project-selection utility; not part of final collection.
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
The final protocol requires every worker to receive `GOMAXPROCS=1` together with
`-p 1 -parallel 1`. This decision was made after `cmd/workerdiag` found a material
effect from inherited internal Go parallelism. The current code must not be used
for final collection unless `go run ./cmd/preflight` succeeds. The executor now
injects and verifies the value for every measured child process.

## Data Collection Workflow

### 1. Clone or place subject repositories

The selected projects are listed in `repos/repos.txt`. Local clones are expected
under `repos/<name>`, for example `repos/cli` or `repos/grpc-go`.

### 2. Characterize a project

```powershell
pwsh scripts/collect.ps1 -ProjectPath repos/cli -ProjectName cli -Runs 10
```

This runs `GOMAXPROCS=1 go test -json -p 1 -parallel 1 -count=1` repeatedly and stores
`run_NN.json`, `run_NN.err`, and `run_NN.meta.json` under
`data/probe/<project>/`. Before aggregation, `cmd/validateprobes` checks the
expected package universe, terminal-event completeness, malformed NDJSON, exit
metadata, and timeout indicators, producing `validation.json`. Only then is the
pass-only dataset written to `data/characterization/<project>.json`.

The build cache is intentionally retained across the ten characterization runs.
`-count=1` disables test-result caching, while package-level `Elapsed` excludes
the preceding test-binary build. Cold campaign isolation is a separate regime.

To validate an existing set without recollecting it:

```powershell
$probes = Get-ChildItem data/probe/cli -Filter run_*.json |
  Where-Object Name -Match '^run_\d{2}\.json$' |
  Sort-Object Name | Select-Object -ExpandProperty FullName
go run ./cmd/validateprobes `
  -project-path repos/cli `
  -expected-runs 10 `
  -output data/probe/cli/validation.json `
  $probes
```

`PASS` means the expected runs and terminal package events are complete. `WARN`
allows aggregation but requires reviewing the listed caveats, which is expected
for historical probes without sidecars. `FAIL` blocks aggregation and identifies
the runs that need correction or recollection.

To reconcile durations independently after validation:

```powershell
go run ./cmd/auditdurations `
  -characterization data/characterization/cli.json `
  $probes
```

The auditor reports source quantization, pass-only medians, suite dispersion,
concentration and every characterization difference with a 1 ns tolerance.

### Current experimental state

The former GCP probes passed the retroactive structural validator, so no silent
truncation or aggregation defect was found. `cmd/workerdiag`, however, showed a
material timing effect when `GOMAXPROCS` was fixed at one. The final methodology
therefore requires a complete timing-dependent recollection under
`GOMAXPROCS=1`.

All previous cloud characterizations, baselines, and campaigns are classified as
`pilot/pre-gomaxprocs1`. The local Windows dataset is outside the final study.

The selected repositories remain frozen at their existing commits. Do not pull
new upstream revisions or run a new triage: that would change the subject systems
at the same time as the worker semantics.

Implementation preparation is complete. Before the final run:

1. run the preflight and confirm clean, frozen source identities;
2. recollect ten probes per project and regenerate the four characterizations;
3. audit and freeze the characterizations;
4. recollect all 32 baselines;
5. run eight campaigns with five repetitions and counterbalanced order.

### 3. Collect pass-only baselines

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/collect_passonly_baselines.ps1 -TimeoutMinutes 60
```

The baseline commands use the same package list as the characterization file.
This keeps `T1` and `Tp` comparable when computing speedup. Cold baselines use a
fresh isolated `GOCACHE`; reports are first staged and validated, and an existing
canonical report is backed up before replacement.

### 4. Run benchmark campaigns

```powershell
go run ./cmd/benchmark --config benchmarks/campaign_cli_warm.json --repetitions 5 --environment-label gcp-primary
```

Future final campaigns use five logical repetitions and a cyclic counterbalanced
algorithm order. A real benchmark retries a failed logical repetition up to three total attempts
by default (`max_attempts`). Every failure is logged. Failed attempts are not
aggregated; if the third attempt also fails, the repetition remains marked as
failed and the command returns an error after preserving all reports.

A benchmark run writes:

- `config.json`: resolved configuration copy;
- `environment.json`: captured environment metadata;
- `native_baselines.csv`: sequential and Go-native parallel references used by the run;
- `results.json`: full structured report;
- `raw.csv`: one row per repetition;
- `aggregate.csv`: summary by project, algorithm, and worker count.

For the complete set of final campaigns:

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1 `
  -TimeoutMinutes 90 -Repetitions 5 -EnvironmentLabel gcp-primary
```

### Targeted worker-semantics diagnostic

This diagnostic has been completed and is intentionally separate from canonical campaigns:

```powershell
go run ./cmd/workerdiag `
  --project-path repos/cli `
  --data-file data/characterization/cli.json `
  --workers 1,2,4,8 `
  --repetitions 3 `
  --top-packages 8
```

It alternates inherited `GOMAXPROCS` and `GOMAXPROCS=1`, validates the child
setting with a self-check, and writes JSON, CSV, and a textual interpretation.
It demonstrated that inherited internal Go parallelism materially affected execution, especially for gRPC-Go. The canonical protocol now requires `GOMAXPROCS=1`; diagnostic values remain non-canonical.

## Selected Subject Projects

| Project | Frozen commit | Final characterization |
| --- | --- | --- |
| cli/cli | `da68cb8f6f597cfc3838cf40f89ecc01f4e53233` | pending under `GOMAXPROCS=1` |
| goreleaser/goreleaser | `ce96e79b4883bdea39cf2cf5fe33fa63f5df4dd0` | pending under `GOMAXPROCS=1` |
| grpc/grpc-go | `faa34bf170ceef07b9ada9bcd44dc6e16a55d1f4` | pending under `GOMAXPROCS=1` |
| gohugoio/hugo | `72495f9fba69edadd50a7ecb9ae9fb3d9c46156b` | pending under `GOMAXPROCS=1` |

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
