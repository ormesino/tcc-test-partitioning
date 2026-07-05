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
trade-off between empirical makespan and coordination overhead under a controlled
Go execution model.

The tool is not intended to be a full CI orchestrator. It does not implement a
distributed runner, a multi-language framework, machine-learning prediction, or
production-grade scheduling infrastructure.

### Canonical experimental environment

Only the dedicated Google Cloud Platform VM is part of the final empirical
analysis. Measurements from the former personal Windows notebook are excluded
from final tables, plots, hypotheses, conclusions, and sample counts. They may
remain archived solely as historical traceability.

The subject repositories are frozen at the commits already selected:

| Project | Frozen commit |
| --- | --- |
| cli | `da68cb8f6f597cfc3838cf40f89ecc01f4e53233` |
| goreleaser | `ce96e79b4883bdea39cf2cf5fe33fa63f5df4dd0` |
| grpc-go | `faa34bf170ceef07b9ada9bcd44dc6e16a55d1f4` |
| hugo | `72495f9fba69edadd50a7ecb9ae9fb3d9c46156b` |

The projects will not be updated to newer upstream revisions before the final
collection. Doing so would change tests, dependencies, package populations, and
runtime behavior at the same time as the worker-semantics change, requiring a new
triage and creating an avoidable confounder.

## 2. Scheduling Model and Objective

The primary objective is empirical makespan minimization. In scheduling notation,
the partitioning problem is modeled as P||Cmax: independent jobs are assigned to
a fixed number of identical processors, minimizing the completion time of the
most loaded processor.

In the canonical experiment:

- a Go test package is a job;
- a worker is one external `go test` process;
- every relevant Go process receives `GOMAXPROCS=1`;
- package median duration is the historical processing-time estimate;
- empirical makespan is the wall-clock interval from the first worker start to
  the last worker completion.

`GOMAXPROCS=1` limits simultaneous execution of Go-managed code inside each
process and removes the unrestricted internal goroutine parallelism found by the
worker diagnostic. It does not provide CPU affinity or a physically exclusive
core, so P||Cmax remains a controlled abstraction rather than a claim of hardware
pinning.

Secondary metrics include speedup, efficiency, planned-load dispersion,
partitioning overhead, and the difference between planned and observed behavior.

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

The empirical study uses four open source Go projects: cli, GoReleaser, gRPC-Go,
and Hugo. They were selected from a broader candidate set using build viability,
number of testable packages, pass rate, and duration-distribution diversity.

No new project triage will be performed before the final run. The commits listed
in Section 1 are fixed experimental subjects. This preserves reproducibility and
ensures that `GOMAXPROCS=1` is the only substantive change between the pilot
protocol and the final protocol.

Package counts, CV, max/median, top-k concentration, and pass-only populations
from the inherited-`GOMAXPROCS` collection are pilot descriptors. They must not
be treated as final values. The final table will be regenerated from ten new
validated probes per project under `GOMAXPROCS=1`.

The previous pilot suggested highly dispersed suites but did not contain a
low-dispersion control. Whether this remains true must be reassessed after the
new characterization.

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

Package durations are collected through ten repeated executions of:

```text
GOMAXPROCS=1 go test -json -p 1 -parallel 1 -count=1 ./...
```

The final duration for each package is the median of its ten valid package-level
`Elapsed` values. A package enters the pass-only population only if it is present
and passes in every accepted run.

The choices are deliberate:

- `GOMAXPROCS=1` bounds Go-managed CPU parallelism in every test process;
- `-count=1` disables Go test-result caching;
- `-p 1` serializes package test processes inside the characterization command;
- `-parallel 1` limits tests that explicitly use `t.Parallel`;
- the median reduces sensitivity to occasional noisy observations.

`GOCACHE` remains available between the ten probes because the scheduling weight
uses package-level `Elapsed`, which excludes the preceding build of the test
binary. Cold campaign isolation is a separate wall-clock regime.

Each probe records NDJSON, stderr, and a metadata sidecar. `cmd/validateprobes`
compares terminal events with a package universe obtained on the same GCP
checkout, reports malformed lines, missing terminals, timeouts, and exit-code
inconsistencies, and blocks aggregation on objective incompleteness.

The previous cloud probes passed structural validation but were collected with
inherited `GOMAXPROCS`; they are pilot evidence and cannot supply the final
characterization.

An independent reconciliation with `cmd/auditdurations` classified duration
handling as **A — no error**. Go's `test2json` rounds package terminal `Elapsed`
to one millisecond before JSON serialization. Across 643 pass-only packages,
six stored values had a one-nanosecond floating-point residue and none exceeded
the explicit 1 ns tolerance. Calculation semantics are unchanged; recollection
is required by the `GOMAXPROCS` policy, not by a duration defect.

## 9. Worker Execution Regime

Partitioned runs execute one `go test` process per worker. Every worker uses:

```text
GOMAXPROCS=1 go test -p 1 -parallel 1 -count=1 <assigned packages>
```

The worker diagnostic showed a material difference between inherited
`GOMAXPROCS` and `GOMAXPROCS=1`, particularly for gRPC-Go. The final protocol
therefore fixes the value at one rather than redefining a worker as a potentially
multi-CPU executor.

The setting must be injected by the tool for every child process, not left as an
undocumented shell convention. The child value is self-checked and recorded in
`environment.json`. Warm-up commands, retries, baselines, and probes follow the
same rule.

This decision requires a complete timing-dependent reset: new probes,
characterizations, baselines, and campaigns. Earlier cloud results remain valid
only as pilot data used to diagnose and refine the protocol.

## 10. Baselines

The project uses two Go-native baselines over the exact final pass-only
population.

### Sequential baseline

```text
GOMAXPROCS=1 go test -p 1 -parallel 1 -count=1 <pass-only packages>
```

This measures `T1`, the sequential reference used for empirical speedup.

### Native parallel baseline

```text
GOMAXPROCS=1 go test -p P -parallel 1 -count=1 <pass-only packages>
```

This measures Go's package-level parallelism for P in {2, 4, 8}. Each generated
test process inherits the same one-CPU Go-runtime limit, while `-p P` controls
how many package test processes may run concurrently.

Both baselines must be recollected after the new characterization. Baselines
from the inherited-`GOMAXPROCS` pilot are not compatible with final speedup or
efficiency calculations.

Reports include duration, package count, source hash, success state, cache
regime, and error text. Cold baselines use a fresh isolated `GOCACHE`; warm
baselines use the successful warm-up state. Reports are staged and validated
before replacing any canonical artifact.

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

## 16. Pilot Validation and Canonical Reset

`cmd/validateprobes` found the prior GCP probes structurally complete: there was
no evidence of malformed NDJSON, truncated runs, duplicate terminal events, or
silent package loss. The prior characterization logic is therefore not rejected
as an implementation error.

`cmd/workerdiag`, however, found material runtime differences when internal Go
parallelism was restricted. The project has consequently chosen technical-model
fidelity over preserving the previous timing artifacts.

The next collection is the only canonical dataset and consists of:

- 40 probes: four projects times ten runs;
- four validated pass-only characterizations;
- 32 baselines: four projects times two cache regimes times one sequential and
  three native-parallel references;
- eight campaigns: four projects times cold/warm, each with 60 logical samples
  (three worker counts times four algorithms times five repetitions).

No new triage or upstream update is part of this reset. Previous GCP artifacts
are retained under a `pilot/pre-gomaxprocs1` classification and must not be mixed
with final results.

## 17. Hypotheses and Exploratory Questions

The final hypotheses are frozen before the new canonical collection. They are
pilot-informed and this provenance must be stated explicitly; they must not be
changed again after final results are observed.

- **H1:** for the same project, cache regime, and worker count, LPT tends to
  achieve lower empirical makespan than Round-Robin and Quantity.
- **H2:** the relative improvement of LPT over Round-Robin and Quantity tends to
  be greater in warm-cache than in cold-cache executions.
- **H3:** partitioning overhead remains below 1% of empirical makespan for every
  evaluated algorithm and configuration.

FFD-Multifit remains a full algorithm under comparison, but the relation between
its planned and empirical performance is treated as an exploratory research
question rather than a post-hoc directional hypothesis. The relation between
suite concentration metrics and algorithm gains is also exploratory because the
study contains only four projects.

Five repetitions support descriptive comparison of medians, dispersion, relative
effects, sign consistency, and outliers. They do not justify strong claims of
population-level statistical inference.

## 18. Known Limitations

The study is intentionally scoped. The most important limitations are:

- results are based on four frozen Go projects, not a broad benchmark corpus;
- all final measurements come from one dedicated GCP VM;
- `GOMAXPROCS=1` bounds Go-runtime CPU parallelism but does not pin each process
  to an exclusive physical or logical CPU;
- package-level partitioning cannot split a single very slow package;
- the final characterization may still lack a low-dispersion control project;
- cold runs include compilation and other costs not represented by package
  `Elapsed` weights;
- warm-cache behavior approximates, but does not fully reproduce, CI caching;
- static historical durations may become stale;
- five repetitions prioritize practical descriptive evidence over strong
  inferential claims.

These limitations are acceptable for comparing classical partitioning heuristics
under one controlled, reproducible Go workload protocol.
