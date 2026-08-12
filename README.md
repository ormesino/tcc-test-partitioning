# tcc-test-partitioning

Empirical evaluation of static test-suite partitioning strategies for Go
projects. This repository contains the implementation, the accepted experimental
artifacts, and consolidated tables produced for an undergraduate Computer
Science thesis.

The README is organized as a guided entry point to the artifact:

1. project purpose and scope;
2. repository structure;
3. role of each component in the experimental workflow;
4. brief methodology and result summary.

For the complete theoretical foundation, experimental design, threats to
validity, and discussion of the results, consult the thesis submitted with this
artifact.

## 1. Project Overview

The project compares four static partitioning strategies:

- **Round-Robin**, which distributes packages cyclically;
- **Quantity**, which balances the number of packages per worker;
- **LPT (Longest Processing Time first)**, which greedily assigns the longest
  remaining package to the least-loaded worker;
- **FFD-Multifit**, which searches for a tighter capacity-based packing.

The strategies are evaluated against sequential and Go-native parallel
baselines under the `P||Cmax` scheduling model. The primary objective is to
reduce empirical makespan, the elapsed time until the slowest worker finishes,
while keeping the cost of constructing the partitions negligible.

In this repository, **partitioning overhead** means only the time spent building
the partitions. It does not include process coordination, compilation, or test
execution. The implementation uses only the Go standard library.

The published artifact includes the source code, synthetic fixtures, validated
characterizations, pass-only baselines, accepted campaign reports, probe
metadata and audits, and consolidated result tables. Raw probe NDJSON and stderr
files, subject-repository clones, and transient execution outputs are not part
of the public artifact set.

## 2. Repository Structure

```text
cmd/
  analyze/          Aggregates repeated go test -json probes.
  auditdurations/   Reconciles probes, medians, and suite metrics.
  benchmark/        Runs projects x workers x algorithms x repetitions.
  demo/             Demonstrates all algorithms on synthetic data.
  gendata/          Generates deterministic synthetic fixtures.
  partitioner/      Main CLI for simulation, execution, and baselines.
  preflight/        Checks effective child-process GOMAXPROCS.
  validateprobes/   Audits probe integrity with PASS/WARN/FAIL status.
  workerdiag/       Diagnoses worker runtime semantics.
data/
  synthetic/        Deterministic fixtures for local validation.
  probe/            Probe metadata, validation reports, and duration audits.
  characterization/ Final pass-only package-duration datasets.
  baseline/         Final pass-only sequential and native measurements.
internal/
  model/            Domain types and result structures.
  partitioner/      Implementations of the four algorithms.
  executor/         Parallel go test execution.
  metrics/          Makespan, speedup, efficiency, and balance metrics.
repos/
  repos.txt         Selected projects and revisions; local clones go here.
scripts/
  collect.ps1                    Collects repeated package-level probes.
  collect_passonly_baselines.ps1 Collects comparable baselines.
  recharacterize_all.ps1         Rebuilds all characterization datasets.
  run_all_campaigns.ps1          Runs the complete campaign matrix.
  triage.ps1                     Supports the original project-selection stage.
benchmarks/
  example-config.json Synthetic example for local validation.
  campaign_*.json     Final campaign configurations.
  results/            Primary artifacts from the eight accepted campaigns.
results/
  *.csv               Consolidated views derived from campaign artifacts.
  SHA256SUMS.txt      Integrity manifest for the consolidated tables.
```

The two result directories serve different purposes:

- `benchmarks/results/` preserves the primary output of each accepted campaign,
  including resolved configuration, environment, raw repetitions, and
  aggregates;
- `results/` provides compact cross-campaign views used to inspect the complete
  experiment without reopening every campaign directory.

## 3. Components and Experimental Workflow

The components form a traceable pipeline from raw timing observations to the
final analysis:

| Stage | Main components | Input | Output |
| --- | --- | --- | --- |
| Environment check | `cmd/preflight`, `cmd/workerdiag` | Go runtime and a subject project | confirmation of worker semantics |
| Probe collection | `scripts/collect.ps1` | subject repository | repeated `go test -json` observations |
| Validation | `cmd/validateprobes`, `cmd/auditdurations` | probes and metadata | integrity report and duration audit |
| Characterization | `cmd/analyze`, `scripts/recharacterize_all.ps1` | accepted probes | pass-only package medians |
| Baselines | `cmd/partitioner`, `collect_passonly_baselines.ps1` | characterization and subject project | sequential and Go-native references |
| Simulation and execution | `cmd/partitioner` | characterization, baseline, and worker count | planned or empirical partition result |
| Campaigns | `cmd/benchmark`, `run_all_campaigns.ps1` | campaign configurations | raw and aggregate campaign reports |
| Final analysis | `benchmarks/results/`, `results/` | accepted reports | auditable artifacts and consolidated tables |

### Requirements

- Go 1.22 or newer;
- PowerShell 7 or newer for the collection scripts;
- GNU Make, optionally, for convenience targets.

### Quick validation with synthetic data

This path exercises the tool without cloning external projects or running their
test suites:

```powershell
go run ./cmd/gendata -profile all
go run ./cmd/demo --output-json reports/demo.json
go run ./cmd/benchmark --config benchmarks/example-config.json
```

### Reproducing the experimental flow

The selected repositories and frozen revisions are listed in `repos/repos.txt`.
Place local clones under `repos/<name>` and verify the execution environment
before collecting measurements:

```powershell
go run ./cmd/preflight
pwsh scripts/collect.ps1 -ProjectPath repos/cli -ProjectName cli -Runs 10
```

Probe collection runs
`GOMAXPROCS=1 go test -json -p 1 -parallel 1 -count=1` ten times. It retains the
build cache across characterization runs while disabling the Go test-result
cache. `cmd/validateprobes` then verifies the package universe, terminal events,
NDJSON structure, exit metadata, and timeout indicators before aggregation.

The validation statuses mean:

- `PASS`: the expected runs and terminal package events are complete;
- `WARN`: aggregation is allowed, but the reported caveats require review; this
  is expected when failing or skipped packages are intentionally excluded by
  the pass-only policy;
- `FAIL`: aggregation is blocked until the affected probes are corrected or
  recollected.

Baselines use the same pass-only package universe as the characterization so
that `T1` and `Tp` remain comparable:

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/collect_passonly_baselines.ps1 `
  -TimeoutMinutes 60
```

A single accepted campaign configuration can be executed with:

```powershell
go run ./cmd/benchmark `
  --config benchmarks/campaign_cli_warm.json `
  --repetitions 5 `
  --environment-label gcp-primary
```

The complete matrix can be run with:

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1 `
  -TimeoutMinutes 90 `
  -Repetitions 5 `
  -EnvironmentLabel gcp-primary
```

Each campaign directory contains:

- `config.json`, the resolved configuration;
- `environment.json`, the captured execution environment;
- `native_baselines.csv`, the sequential and native parallel references;
- `results.json`, the complete structured report;
- `raw.csv`, one row per logical repetition;
- `aggregate.csv`, summaries by project, algorithm, and worker count.

Campaigns use a cyclic counterbalanced algorithm order and retry a failed
logical repetition up to three total attempts by default. Failed attempts are
preserved but excluded from aggregation. If the final attempt also fails, the
repetition remains failed and the command exits with an error.

### Direct CLI examples

Simulation computes a planned schedule from historical durations without
executing `go test`:

```powershell
go run ./cmd/partitioner --mode simulate `
  --algorithm all `
  --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq-passonly.json `
  --output-json reports/cli-simulate-w4.json
```

Execution partitions the package list and starts one `go test` process per
worker:

```powershell
go run ./cmd/partitioner --mode run --warm-cache `
  --algorithm ffd `
  --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq-warm-passonly.json `
  --project-path repos/cli `
  --output-json reports/cli-ffd-w4-warm.json
```

Every measured child process receives and verifies `GOMAXPROCS=1`, together
with `-p 1 -parallel 1`.

## 4. Thesis Methodology and Results

The final study used four Go projects at frozen revisions:

| Project | Frozen commit | Pass-only packages |
| --- | --- | ---: |
| cli/cli | `da68cb8f6f597cfc3838cf40f89ecc01f4e53233` | 236 |
| goreleaser/goreleaser | `ce96e79b4883bdea39cf2cf5fe33fa63f5df4dd0` | 121 |
| grpc/grpc-go | `faa34bf170ceef07b9ada9bcd44dc6e16a55d1f4` | 144 |
| gohugoio/hugo | `72495f9fba69edadd50a7ecb9ae9fb3d9c46156b` | 142 |

Only packages that passed all ten accepted characterization probes were
included. The canonical dataset comprises:

- 40 validated probes and 4 pass-only characterizations;
- 32 pass-only baselines;
- 8 cold- and warm-cache campaigns;
- 480 logical executions and 96 aggregate rows;
- no final logical errors and no attempts beyond the first.

All final campaigns were collected in the `gcp-primary` environment on
Linux/amd64 with an AMD EPYC 7B13 VM and effective `GOMAXPROCS=1`. Each campaign
used five logical repetitions, worker counts of 2, 4, and 8, and a
counterbalanced algorithm order.

The four hypotheses were defined before acceptance of the final dataset and
treated as directional hypotheses:

- **H1 — partially supported:** LPT achieved lower empirical makespan than
  Round-Robin in 16/24 comparable cells and lower than Quantity in 17/24. It
  beat both simultaneously in 14/24; the median improvements were 6.49% and
  4.17%, respectively.
- **H2 — supported mainly under warm cache:** the relative LPT advantage was
  greater under warm cache in 10/12 comparisons with Round-Robin and 8/12 with
  Quantity.
- **H3 — strongly supported:** partition construction remained well below 1%
  of empirical makespan; the maximum observed share was 0.000720%.
- **H4 — not supported:** FFD-Multifit's planned advantage over LPT was at most
  0.029%, and it did not achieve a lower empirical makespan in any comparable
  cell. Its median empirical difference relative to LPT was -40.13%.

The difference between planned and empirical behavior is consistent with costs
that historical package durations do not model, such as process startup,
compilation, I/O, cache effects, and the number of packages assigned to each
partition. This is a descriptive interpretation, not a causal claim. Pearson
correlations between planned and observed makespan are likewise reported
without significance tests or causal attribution.

This summary is intentionally brief. The thesis should be used as the canonical
source for the research questions, theoretical background, complete protocol,
statistical treatment, limitations, threats to validity, and detailed result
discussion.

## Technical Reference

### Testing

```powershell
go test ./cmd/... ./internal/... ./data/synthetic
go vet ./...
```

Avoid `go test ./...` when external repositories are cloned under `repos/`, as
the blanket command may include those projects. The scoped command validates
this tool only.

### JSON conventions

- field names use `snake_case`;
- `time.Duration` values are serialized as nanoseconds with `_ns` suffixes;
- `time.Time` values use RFC3339 formatting;
- optional fields use `omitempty` where appropriate.

The experimental dataset and thesis analysis are complete. This repository is
the archival presentation of the accepted study; no additional characterization,
baseline collection, campaign execution, project update, or hypothesis
reformulation is planned.
