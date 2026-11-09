# Public execution contracts needed for SWE-bench Lite

These are proposed integrations, not existing APIs. Bench remains a public CLI/MCP
client. Controls stays workflow- and agent-agnostic; benchmark logic belongs in an
integration package. Target: full Lite dataset, autonomous execution.

## CTRL-BENCH-001 — Isolated per-instance execution environment

**Problem:** current host execution does not enforce separation from private test
material, host state or other trials. An issue string does not identify/provision its
source baseline or environment. A worktree is not an isolation boundary.

**Design work:** define a generic environment descriptor and backend interface for
source identity, immutable baseline, image digest, resource/network limits and mounts.
Expose submission and resolved identity through public CLI/MCP. Define how package
runtimes and all participants share one trial environment while separate trials are
isolated. Keep SWE-bench field translation outside the scheduler. Reuse an established
container backend; do not build a new sandbox runtime.

**Requirements:** do not mount raw dataset exports, reference patches, private tests,
host credentials or the Docker socket into workers. Prevent future-commit/reference
lookup according to the registered network policy. Inspect upstream images for leaked
solutions, grader scripts and future history rather than assuming an image is safe.
Retain image/platform/digest/source identity, startup failures and cleanup state.

**Acceptance:** deterministic fixtures verify exact baseline, allowed shared files,
cross-trial denial, private-material denial, network policy, cancellation and cleanup.
Reject unsupported requirements before model work. Document platform/architecture
support, remote-service paths and residual isolation limits. Bench does no provisioning.

## CTRL-BENCH-002 — Public trial submission and bounded lifecycle

**Problem:** pilot submission carries an issue and config, but not a validated,
benchmark-specific instance binding. Client timeouts do not enforce execution budgets.

**Work:** formalize public input/environment binding, submission idempotency and
inspection of the resolved configuration/environment. Evaluate existing APIs first;
extend only missing generic capabilities. Enforce supported per-execution budgets
across all agent/reviewer steps and record the terminal reason. Define handling of
unexpected human input for an autonomous trial without auto-approval.

**Acceptance:** lost submission replies/restarts do not duplicate work; different
inputs cannot reuse an idempotency key; aggregate budget limits apply to the whole
execution; inspection exposes stable identities and missing telemetry explicitly.
No benchmark-aware orchestration or roles in Controls. Document CLI/MCP examples and
contract tests before Bench adopts the interface.

## CTRL-BENCH-003 — Immutable candidate export and grader evidence

**Problem:** Bench cannot retrieve a candidate patch and official grade through a
supported contract. Reading a returned workspace path would violate the boundary.

**Work:** define a generic immutable output/export interface bound to execution,
source baseline and content digest. Specify the export point after agent writes stop,
including failed/budget-exhausted trials where a candidate exists. A configured
integration produces the patch; Controls does not infer Git behavior or score tests.
Allow a separate authorized grading phase/backend to consume the candidate and return
versioned results through a public evidence contract, inaccessible to active workers.

**Requirements:** enforce caller authorization, size/path limits, candidate identity,
append-only grading records and evaluator/environment identity. Grade a fresh baseline,
not the mutated worker container. Separate product grade, workflow state, infrastructure
failure and missing evidence. Report ordinary zero-resolution results without treating
them as control-plane faults. Define retention for patches, grader output and traces.

**Acceptance:** wrong execution/baseline/digest rejected; no stale cached grade reuse;
worker cannot read private tests or official results during its attempt; public clients
can fetch required evidence without filesystem access. Validate with positive/negative
fixtures before connecting the official SWE-bench adapter in Registry.

Dependencies: [Bench plan](../bench/bench_issues.md),
[Registry integration](../registry/bench_issues.md). These issues do not authorize
silently changing existing factories or introducing benchmark endpoints as one-off hacks.
