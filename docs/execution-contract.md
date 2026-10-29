# Execution contract

This document defines the **target** Factory Workflows execution contract. The [Go package](../execution/) expresses the core records and operations. The [Hatchet integration](../hatchetengine/) implements durable routing and waits, and the local controller exposes CLI execution, inspection, decisions, retries, and cancellation. Use the [factory.json reference](factory-json.md) for fields accepted today. Versioned admission, recovery, authority, artifact verification, and sandbox requirements below are design targets where the current implementation falls short.

## Terms

An **execution** is one run of a workflow for a brief and project. One controller service may manage many executions concurrently. Every command names an execution; there is no implicit “current execution.” This first contract allows one active workflow position per execution; separate executions may progress independently.

A **step** is an executable unit: a run step invokes an agent or deterministic package, and a decision step waits for an authorized answer. A **stage** groups run steps and owns their bounded repeat policy and optional human approval boundary. For example, `build` and `code_review` are distinct steps in an `implementation` stage. Routes still determine which step follows an outcome.

A **visit** begins each time routing enters a step. A review-to-build loop creates a new build visit. A **try** is an attempt within one visit after an automatic or authorized manual retry. Both counters start at one. Each attempt records the configuration revision it used.

## Workflow definition

A workflow has one start step. Each step has a unique `name` in `factory.json`, a kind, and explicit outcome routes. Routes refer to step names or a terminal outcome. The controller assigns separate runtime IDs to executions and step visits. It may take a route only for an outcome accepted from the active attempt. An outcome with no route is invalid: the controller rejects it and surfaces the configuration problem rather than inferring success.

A run step references a named package whose snapshot is pinned by digest in the same configuration revision. Its declared inputs and outputs name the handoff contract. Optional `outputs_on` selects which outcomes require those output files; without it, all outcomes require them. This is workflow policy, independent of outcome spelling or the agent runner. A blocker can be reported without promised success artifacts when the workflow excludes that outcome from output requirements. The package may run an AI agent, a script, or a classifier. A decision step names the required authority, prompt, and available outcomes. To ask a person only when a criterion is met, route through a classifier or deterministic run step: one outcome reaches the decision step and another skips it. The controller does not invent a classification or decide on a person's behalf.

For a small coding workflow, `build` routes `ready_for_review` to `review`. The review step routes `changes_requested` back to `build` and `completed` to `acceptance`. The `implementation` stage's `loop` contains the build and review step definitions and sets the maximum number of rounds. The stage may require the `acceptance` human decision before proceeding. If the last allowed review still requests changes, routing goes to `acceptance` with a visible limit warning and the review findings; the human can request changes to begin a fresh bounded series or reject. An early satisfactory review goes to acceptance immediately. This policy works for any run packages and outcome names; the controller does not interpret review prose.

The configuration revision contains the workflow, selected package snapshots, effective non-secret runner settings, and requested access profiles. The controller must preserve the exact revision used for each attempt. Secret *references* may be pinned; secret values are resolved only at launch and are not stored in events or execution records. A digest is meaningful only if the package bytes remain retrievable. The package format and distribution mechanism are separate from this contract.

## Execution and completion

Today, the workflow engine owns the durable workflow position and route evaluation. The Factory control plane accepts commands and maintains a queryable PostgreSQL projection through progress events. Atomicity between the engine and that projection, authenticated authority, version checks, and idempotent command IDs remain design targets; the current implementation must not be treated as providing them.

A run step is complete **only** when its active worker submits a valid structured `step_result` message through the thread MCP interface and the controller accepts it. A process exit, an idle pane, a chat message saying “done,” or a loop completion token is not a result. The result states an outcome, summary, and actor; the local runner attaches verified required file artifacts from the project. The engine checks that each required output has a URI and digest. Other runner implementations must provide equivalent artifact evidence; general evidence requirements remain a design target. A valid submission can route to review, back to earlier work, or to failure; it does not itself approve the execution.

A decision step becomes an attention item when reached. An authorized actor answers one of its configured outcomes with a reason. The decision and its target artifact versions are recorded before routing continues. A message from a conversational agent is not proof of human authority. The host must authenticate the actor and enforce any explicitly delegated decision scope.

The execution is complete only after a route reaches `$complete`. `$fail`, cancellation, and a blocked step are distinct terminal or paused conditions. Reports must retain every visit, try, result, failure, decision, and configuration revision, including superseded work.

`CancelExecution` prevents new dispatch and records the actor and reason. If a worker is live, the controller requests its termination and records whether the runner confirmed it stopped; cancellation of the record alone does not imply the process ended.

## Retry and recovery

A step's retry policy names retryable infrastructure failure kinds and, separately, submitted outcomes that may be retried. `max_attempts` includes the first try; zero means one try. `delay_seconds` applies between automatic tries. Retry uses the **same visit and configuration revision**, increments `try`, and retains the prior attempt in history. If policy does not permit another automatic try, the execution becomes blocked or follows an explicit failure route. A review result that routes back to build is a **new visit**, not a retry.

`RetryStep` is an explicit, authorized recovery command for an inactive attempt. It retains that attempt's configuration, even if the automatic retry limit was exhausted, and records who requested it and why. It cannot be used to duplicate a still-running worker. A lost worker must first be established by the runner's supervision mechanism; an expired heartbeat alone is insufficient proof that it stopped.

`ContinueFrom` is distinct from retry. The caller selects a step and supplies a complete new configuration. After validating and pinning that revision, the controller starts a **new visit** at the selected step. Previous work remains in history, but results downstream of that point are marked superseded and cannot satisfy the new path. The controller must stop or reconcile any affected live worker and close stale attention items before dispatching the new visit. It must not silently upgrade packages or settings mid-attempt.

## Handoffs and visibility

The controller gives a run step its declared inputs plus the relevant immutable results and artifacts from prior steps. A reviewer can see the coder's submitted result and source snapshot; it does not need to know how the coder was launched. Review findings become a result routed to a new coder visit. In a concurrent loop, active peers exchange work and findings through messages. Each member submits its own final result; the round barrier reduces routes only after all members finish. Messages cannot substitute for a required result or decision.

An artifact may be a document, report, source snapshot, or diff. The contract requires a retrievable URI and digest; it does not require Git or a particular artifact store.

External clients—including agents, a CLI, and a monitor—use the same controller operations. `GetExecution` returns the current execution and history; `ListAttention` returns decisions or input requests requiring action; `ListEvents` returns an ordered stream after a sequence number. Clients may observe and propose actions, but the controller authorizes and validates each action. Every visible step should answer: who owns it, which visit and try is active, what it received, what it must produce, what failed, and what action is available next.

## Access and process boundaries

The control plane depends on the Go `execution.Engine` interface for starting, deciding, retrying, and cancelling executions. The Hatchet adapter implements that port and owns durable scheduling, scoped events, and replay. The adapter depends on `execution.Runner` for one package invocation; the host command runner implements that port using a session supervisor. Neither the controller nor the runner imports Hatchet. The runner receives a pinned `execution.Work` and returns `StepResult`, so an agent-specific launcher or a sandbox runner can be substituted without changing controller policy. The application composition root wires these implementations together.

Each package snapshot requests an access profile: readable and writable paths, network destinations, tools, MCPs, and secret references. Empty lists mean no grant. A runner must reject a step it cannot enforce; a prompt telling an agent to avoid a path is not isolation. The controller should record the requested profile and the runner's actual admission decision. The first implementation may support one sandbox runtime and a deliberately limited profile; it should never report isolation it did not enforce.

The controller supervises each launched worker instance. An execution-scoped worker identity cannot submit for another execution or an old attempt. A single active controller owner or lease prevents two controller processes from dispatching the same execution. Losing the lease stops new transitions until ownership is reconciled. These rules do not prevent multiple executions from running at once.

## Initial vertical slice

The current slice connects the CLI, local controller, Hatchet execution loop, command package runner, and session supervisor. Automated integration covers human decisions/replay, concurrent MCP peer exchange across rounds, and an interrupted round followed by retry. A provider-backed coding/review eval remains a separate validation step. `ContinueFrom` with changed configuration, an enforceable sandbox, authenticated authority, and crash reconciliation remain to be built.

Structured agent communication uses MCP and A2A content parts as defined in the communication contract. Package distribution, enforced sandboxing, and remote artifact storage remain open. Hatchet owns durable workflow scheduling. Factory's PostgreSQL records provide the queryable execution, decision, session, and audit view; they derive from accepted transitions rather than deciding workflow position independently.

## Communication and concurrent loops

See the [communication contract](../comms.md) for the eight MCP tools, A2A content types, typed payloads, unread cursors, publication, and current limits. The runtime injects a scoped connection and shared skill; no completion file is used.

`loop.mode` is `sequential` by default or `concurrent`. Concurrent members start together, stay available between turns, and share a thread. A failed process interrupts the round and stops peers; manual retry restarts that round's members with new attempts. The loop limit counts rounds, not messages. The containing stage retains human decision requirements. `factory EXECUTION-ID messages` and the monitor expose recent conversations; the execution event stream retains complete activity.
