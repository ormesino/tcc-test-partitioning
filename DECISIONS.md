# Project Decisions

This document summarizes the main technical and methodological decisions behind
`tcc-test-partitioning`. It is intended for repository readers who need the
rationale without reading the full thesis or the private working notes.

## 1. Research Scope

The project evaluates static partitioning strategies for Go test suites. The
central question is which package-level partitioning strategy offers the best
trade-off between makespan and coordination overhead across Go projects with
different test-duration distributions.

The tool is not intended to be a full CI orchestrator. It does not implement a
distributed runner, a multi-language test framework, machine-learning prediction,
or production-grade scheduling infrastructure. The implementation stays focused
on collecting data, applying scheduling heuristics, running controlled local
experiments, and exporting results for analysis.

## 2. Scheduling Model and Objective

The primary objective is makespan minimization. In scheduling notation, the
problem is treated as P||Cmax: a set of independent jobs must be assigned to a
fixed number of identical processors, and the objective is to minimize the load
of the most loaded processor.

In this project:

- a Go test package is a job;
- a worker is a processor;
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

4. **FFD-Weighted**
   Sorts packages by a duration-and-variability weight and greedily assigns each
   package to the worker with the smallest accumulated weight. This variant is
   used to test whether incorporating variability improves results on unstable
   or heavy-tailed suites.

Approaches based on regression or machine learning were excluded because they
would require a larger historical dataset and would expand the thesis scope
beyond the intended algorithmic comparison.

## 5. FFD-Weighted Cost Function

The weighted algorithm uses:

```text
weight_i = median_duration_i * (1 + cv_i)
```

where `cv_i` is the coefficient of variation for package `i` across repeated
measurements.

This makes FFD-Weighted meaningfully different from LPT. LPT only considers the
median duration; FFD-Weighted also penalizes packages whose execution time is
less predictable. The intuition is that high-duration, high-variance packages
are more dangerous for the final makespan and should be placed earlier and more
carefully.

The formula is a design choice, not a theoretical optimum. It is intentionally
simple, reproducible, and derived only from values already collected during
characterization.

## 6. Subject Project Selection

The empirical study uses four open source Go projects:

| Project | Pass-only packages | Characterization file |
| --- | ---: | --- |
| cli/cli | 233 | `data/characterization/cli.json` |
| goreleaser/goreleaser | 116 | `data/characterization/goreleaser.json` |
| grpc/grpc-go | 137 | `data/characterization/grpc-go.json` |
| gohugoio/hugo | 142 | `data/characterization/hugo.json` |

The projects were selected from a broader candidate set using build viability,
number of testable packages, pass rate, and duration-distribution diversity.
The final set provides medium-to-large Go suites with non-trivial duration
variance, including heavy-tailed profiles.

A recognized limitation is that the selected projects do not include a clearly
low-variance suite. The conclusions therefore apply primarily to suites in the
observed range of package counts and variability.

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

The final duration for each package is the median across 10 runs. The coefficient
of variation is also computed from those runs.

The choices are deliberate:

- `-count=1` disables Go test result caching;
- `-p 1` measures packages sequentially;
- `-parallel 1` avoids intra-package parallelism during characterization;
- the median is robust to occasional noisy runs;
- 10 runs provide a practical balance between stability and collection cost.

## 9. Worker Execution Regime

Partitioned runs execute one `go test` process per worker. Each worker uses:

```text
go test -p 1 -parallel 1 -count=1 <assigned packages>
```

This avoids a local explosion of parallelism. Without this restriction, each
worker could run multiple packages and intra-package parallel tests at the same
time, making the experiment harder to interpret and potentially exhausting local
resources.

The restriction also keeps the empirical execution closer to the P||Cmax model:
a worker's elapsed time should approximate the sum of the packages assigned to
it.

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
those reports are used as `T1` inputs.

## 11. Cold and Warm Cache Campaigns

The final methodology distinguishes two regimes.

**Cold runs** include the normal cost of building and executing test binaries.
This reflects a less controlled environment where compilation contributes to the
observed wall-clock time.

**Warm runs** pre-compile the selected test binaries before measurement using a
no-test command that populates Go's build cache. The partitioned execution then
focuses more directly on test execution time rather than repeated compilation.

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

Each tuple is treated independently. The driver writes:

- `config.json`, a copy of the resolved configuration;
- `results.json`, the full structured report;
- `raw.csv`, one row per repetition;
- `aggregate.csv`, summary statistics by project, algorithm, and worker count.

This avoids manual execution drift and makes the campaign auditable.

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
- the selected projects do not cover low-variance suites;
- package-level partitioning cannot split a single very slow package;
- local goroutines simulate distributed workers but are not a real cluster;
- warm-cache behavior approximates, but does not fully reproduce, CI caching;
- the static algorithms rely on historical durations that may become stale.

These limitations are acceptable for the thesis goal: comparing classical
partitioning heuristics under a controlled and reproducible Go test workload.