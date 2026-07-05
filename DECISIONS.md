# Project Decisions

This document summarizes the main technical and methodological decisions behind
`tcc-test-partitioning`. It is intended for repository readers who need the
rationale without reading the full thesis or the private working notes.

> **Current-state rule:** later accepted ADRs and current artifacts override
> older tables, ranges, terminology, and hypotheses. Superseded sections remain
> only as historical records and must not be combined with current values. An
> automated agent must identify the status of a decision before using it as
> project context.

## 1. Research Scope

The project evaluates static partitioning strategies for Go test suites. The
central question is which package-level partitioning strategy offers the best
trade-off between makespan and coordination overhead across Go projects with
different test-duration distributions.

The tool is not intended to be a full CI orchestrator. It does not implement a
distributed runner, a multi-language test framework, machine-learning prediction,
or production-grade scheduling infrastructure. The implementation stays focused
on collecting data, applying scheduling heuristics, running controlled host
experiments, and exporting results for analysis.

### Experimental environment hierarchy

The dedicated Google Cloud Platform VM is the primary and current experimental
environment. Results collected on the personal Windows notebook are retained as
a historical and comparative dataset. They are useful for studying the effect
of moving from a shared, day-to-day machine to a dedicated VM, including effects
visible in probes, characterization, baselines, and campaigns.

On the notebook, the experiment was launched from a single PowerShell window and
the computer was left untouched until completion, but background services and
installed programs were not disabled. Therefore, local and cloud results must be
analyzed separately. Absolute times must not be pooled as a homogeneous sample;
comparisons should use within-environment baselines, normalized metrics, ranking
consistency, and differences in variability.

The current application reference is commit `5829f07d`. The older hash recorded
in `env/environment-local.txt` predates documentation-only changes and does not,
by itself, invalidate measurements from either environment.

## 2. Scheduling Model and Objective

The primary objective is makespan minimization. In scheduling notation, the
problem is treated as P||Cmax: a set of independent jobs must be assigned to a
fixed number of identical processors, and the objective is to minimize the load
of the most loaded processor.

In this project:

- a Go test package is a job;
- a worker is an external `go test` executor; its equivalence to one logical processor is an approximation under active diagnostic;
- package duration is the processing time;
- the makespan is the maximum partition load.

Makespan is the primary metric because it corresponds directly to CI feedback
time: the suite is only complete when the slowest partition finishes.

Secondary metrics include speedup, efficiency, load standard deviation, and
partitioning overhead.

## 3. Package-Level Granularity

Partitioning is performed at the Go package level.

This was chosen because a package is the natural execution unit of `go test`:
Go builds one test binary per package and already schedules packages via the
`-p` flag. Package-level partitioning also matches how CI systems commonly split
work, for example through job matrices or test shards.

Finer granularities such as individual test functions were intentionally avoided.
They would require parsing or instrumenting Go tests, interfere with shared
package setup/teardown, and blur the boundary between the external scheduler and
Go's own test runtime.

The trade-off is that package-level partitioning is coarser and may leave fewer
opportunities for perfect balancing, especially when a project has a small
number of very slow packages.

## 4. Algorithms Under Comparison

The project compares four static partitioning algorithms:

1. **Round-Robin**
   Assigns packages cyclically to workers without considering duration. It is a
   simple duration-agnostic baseline.

2. **Quantity**
   Splits the ordered package list into contiguous blocks with approximately the
   same number of packages. It balances package count, not load.

3. **LPT (Longest Processing Time first)**
   Sorts packages by duration descending and greedily assigns each package to
   the currently least-loaded worker. This is a classical scheduling heuristic
   for P||Cmax and is expected to perform well when durations vary widely.

4. **FFD-Multifit**
   Sorts packages by duration descending and searches for a feasible makespan
   capacity using First-Fit Decreasing. This variant operates over the same
   median durations as LPT but employs a bounded capacity search (up to 40
   iterations) to pack exactly P bins, testing whether a tighter packing
   improves results on highly dispersed suites with dominant packages.

Approaches based on regression or machine learning were excluded because they
would require a larger historical dataset and would expand the thesis scope
beyond the intended algorithmic comparison.

## 5. FFD-Multifit Packing Strategy

The multifit algorithm uses a binary search over the possible capacity $C$:

```text
lower_bound = max(max_duration, sum(durations)/p)
upper_bound = sum(durations)
```

This makes FFD-Multifit meaningfully different from LPT. LPT greedily assigns
jobs to the least loaded worker; FFD-Multifit tests an explicit capacity and
only assigns a package if the worker's total load remains within that capacity.
The intuition is that high-duration packages are more dangerous for the final
makespan and should be placed using a stricter bin capacity.

The binary search is bounded to 40 iterations and the resolution is purely
based on the median durations collected during characterization.

## 6. Subject Project Selection

The empirical study uses four open source Go projects:

| Project | Pass-only packages | Suite CV | Max/median | Characterization file |
| --- | ---: | ---: | ---: | --- |
| cli/cli | 236 | 4.797488 | 262.521739 | `data/characterization/cli.json` |
| goreleaser/goreleaser | 121 | 5.467722 | 3346.285714 | `data/characterization/goreleaser.json` |
| grpc/grpc-go | 144 | 2.949269 | 1041.848485 | `data/characterization/grpc-go.json` |
| gohugoio/hugo | 142 | 6.078856 | 1362.039474 | `data/characterization/hugo.json` |

The projects were selected from a broader candidate set using build viability,
number of testable packages, pass rate, and duration-distribution diversity.
The final set provides medium-to-large Go suites with non-trivial duration
variance, including markedly dispersed profiles.

The current characterization contains four highly dispersed suites, with
different degrees of concentration in their largest packages. This supports
comparisons under imbalance but does not provide a low-dispersion control
subject. The observed package-count range is 121 to 236. Suite CV and
max/median are descriptive and do not, by themselves, establish a heavy-tailed
distribution.

## 7. Pass-Only Experimental Scope

Packages that fail or skip during characterization are excluded from the final
experimental population.

This keeps the experiment focused on stable test packages with measurable
durations. Treating skipped packages as zero-duration jobs or mixing failing
packages into the schedule would distort both the partitioning inputs and the
runtime measurements.

The consequence is that the measured suite is not always the full upstream test
suite. The repository records this explicitly through pass-only characterization
files and pass-only baseline reports.

## 8. Characterization Regime

Package durations are collected with repeated executions of:

```text
go test -json -p 1 -parallel 1 -count=1
```

The final duration for each package is the median across 10 runs. Suite-level
dispersion is descriptive and is not stored in `PackageInfo`.

The choices are deliberate:

- `-count=1` disables Go test result caching;
- `-p 1` serializes package test processes inside the command;
- `-parallel 1` limits tests that explicitly use `t.Parallel`, but does not cap ordinary goroutines or all CPU use inside the tested code;
- the median is robust to occasional noisy runs;
- 10 runs provide a practical balance between stability and collection cost.

`GOCACHE` is intentionally retained between characterization runs. The
aggregator uses package-level `Elapsed` events, whose interval starts after the
test binary has been built. Clearing the build cache would add collection cost
without improving the package-duration estimate. This differs from a cold
campaign, which measures the complete `go test` command with an isolated cache.

Every new probe run also records a `.meta.json` sidecar with command, timestamps,
exit code, and timeout indication. `cmd/validateprobes` compares each NDJSON file
with the package universe returned by `go list`, detects malformed lines and
missing terminal events, and emits a clear PASS/WARN/FAIL report before aggregation.
Historical probes without sidecars receive a warning rather than automatic rejection.

## 9. Worker Execution Regime

Partitioned runs execute one `go test` process per worker. Each worker uses:

```text
go test -p 1 -parallel 1 -count=1 <assigned packages>
```

This avoids package-level and `t.Parallel`-level over-parallelism. It does not,
however, force the complete test binary to one CPU: ordinary goroutines and
concurrent code inside the subject project may still use the inherited
`GOMAXPROCS`. Therefore, the worker is currently interpreted as a sequential
package executor rather than a guaranteed single-core processor.

A targeted, non-canonical diagnostic (`cmd/workerdiag`) compares inherited
`GOMAXPROCS` with `GOMAXPROCS=1` on selected dominant packages. No canonical
characterization, baseline, or campaign is changed until that diagnostic is
interpreted.

## 10. Baselines

The project uses two Go-native baselines.

### Sequential baseline

```text
go test -p 1 -parallel 1 -count=1 <pass-only packages>
```

This measures `T1`, the sequential reference used for empirical speedup.

### Native parallel baseline

```text
go test -p P -parallel 1 -count=1 <pass-only packages>
```

This measures Go's package-level parallelism at the same worker counts used by
the partitioning algorithms.

Both baselines are pass-only: they use exactly the packages present in the
characterization file. This prevents comparing a partitioned run over one package
population against a baseline over another.

Baseline reports include duration, package count, package source, success state,
and error text when a run fails. The CLIs reject failed baseline reports when
those reports are used as `T1` inputs. Cold baselines execute with a fresh,
isolated `GOCACHE`; warm baselines inherit the cache populated by their
successful warm-up. Reports are staged and published without directly
truncating an existing valid artifact.

## 11. Cold and Warm Cache Campaigns

The final methodology distinguishes two regimes.

**Cold runs** include the normal cost of building and executing test binaries.
Each partitioned worker receives its own initially empty `GOCACHE`, matching the
independent-runner interpretation of ADR-017. Cache-directory creation and
cleanup are outside the measured makespan; compilation performed by `go test`
remains inside it.

**Warm runs** pre-warm reusable build-cache artifacts for the selected packages
using `-run=^$`. This reduces reusable compilation work, but it does not assert
that every later command performs no build, link, initialization, or setup work.

Both regimes are useful. Cold runs show the practical behavior of the complete
local command. Warm runs better isolate the scheduling question.

## 12. Speedup Interpretation

The project separates two notions of speedup.

**Theoretical speedup** uses:

```text
T1 = sum(characterized package durations)
Tp = planned makespan from simulation
```

This is useful for validating the scheduling algorithms against the mathematical
model.

**Empirical speedup** uses:

```text
T1 = measured sequential baseline
Tp = measured partitioned execution makespan
```

This reflects actual wall-clock behavior. It may include effects not represented
in the pure P||Cmax model, such as compilation behavior and operating-system
noise. The distinction is important because mixing a measured `T1` with a
simulated `Tp` would compare different quantities.

## 13. Benchmark Driver

The `cmd/benchmark` binary exists to run the full experimental matrix from a
JSON configuration file. It sweeps:

```text
projects x workers x algorithms x repetitions
```

Future final campaigns use five logical repetitions per combination. Algorithm
order is deterministically counterbalanced by cyclic rotation: over each complete
block of four repetitions, every algorithm occupies every sequence position once.
Historical three-repetition campaigns remain readable and must be labeled as such.
In run mode, one logical repetition may be attempted up to three times when execution fails. Failed attempts are logged but
are not statistical samples; only a successful attempt is retained. If the third
attempt also fails, the logical repetition is preserved as failed, excluded from
aggregates, and the campaign exits unsuccessfully after writing diagnostics.

The driver writes:

- `config.json`, a copy of the resolved configuration;
- `environment.json`, including environment label, effective `GOMAXPROCS`, CPU model, OS/kernel, Go cache paths, memory and source identities;
- `native_baselines.csv`, the sequential and Go-native parallel references used by the campaign;
- `results.json`, the full structured report;
- `raw.csv`, one row per repetition;
- `aggregate.csv`, summary statistics by project, algorithm, and worker count.

The raw records also preserve `sequence_position`. The timeout path waits for the
child process to terminate before cleanup or retry, preventing overlap with a
previous timed-out attempt. This avoids manual execution drift and makes the
campaign auditable.

## 14. Output Format

All structured JSON files use the same conventions:

- `snake_case` field names;
- `_ns` suffixes for nanosecond duration fields;
- RFC3339 timestamps;
- optional fields omitted when not applicable.

The convention is designed for easy downstream use in Python, spreadsheets, and
plotting tools.

## 15. Implementation Structure

The internal Go packages are intentionally small:

- `internal/model` defines shared domain types;
- `internal/partitioner` contains the strategy interface and algorithms;
- `internal/executor` runs `go test` and records execution results;
- `internal/metrics` computes makespan, speedup, efficiency, and load balance.

The `Partitioner` interface has only two methods: `Name()` and `Partition()`.
This keeps the algorithms interchangeable and easy to test.

The executor uses goroutines, channels, and `sync.WaitGroup`, following Go's CSP
style. No external Go dependencies are required.

## 16. Known Limitations

The study is intentionally scoped. The most important limitations are:

- results are based on four Go projects, not a broad benchmark corpus;
- the current characterization has no low-dispersion control project (suite CV below 0.5);
- package-level partitioning cannot split a single very slow package;
- local goroutines simulate distributed workers but are not a real cluster;
- primary results come from one dedicated GCP VM, while the notebook dataset is comparative and subject to uncontrolled background activity;
- warm-cache behavior approximates, but does not fully reproduce, CI caching;
- the static algorithms rely on historical durations that may become stale.

These limitations are acceptable for the thesis goal: comparing classical
partitioning heuristics under a controlled and reproducible Go test workload.
