# Issues

## CTRL-001 — Runtime lifecycle reports broadcast into agent conversations

**Priority:** High
**Status:** Open

### Defect

The Pi adapter sends `runtime_status` payloads (`busy` / `idle`) through MCP `send_message` without specifying `recipients`. The communication service treats omitted recipients as a broadcast to all thread participants, including peer agents and the operator.

Peer adapters inject incoming messages into their native agent sessions without excluding lifecycle reports. Receiving a peer's status can therefore trigger a model turn, which emits another status report. This creates a possible feedback loop, unnecessary token consumption, misleading activity and operator inbox noise. The broadcast defect is confirmed in code; a sustained feedback loop has not yet been established from execution evidence.

Lifecycle reports describe runtime observations for completion recovery. They are not agent-authored collaboration messages and must not wake peer agents.

### Relevant code

- [Pi lifecycle reporting and incoming delivery](adapters/pi/pi-rpc-session.mjs)
- [Broadcast recipient resolution](internal/comms/messages.go)
- [Runtime status handling and completion recovery](internal/comms/recovery.go)

### Required change

1. Have the adapter explicitly address lifecycle reports to the assigned controller.
2. Enforce controller-only routing for `runtime_status` at the communication-service boundary, including direct service calls. Define an explicit rejection for omitted or additional recipients rather than allowing a broadcast.
3. Defensively exclude lifecycle reports from native model-input delivery, while preserving cursor advancement and acknowledgment.
4. Retain lifecycle state in execution observability without treating it as operator inbox correspondence. Operator access to the audit history must remain available.
5. Update the communication contract, adapter documentation, skill and diagram to reflect enforced routing.

### Acceptance criteria

- Busy/idle reports update the correct invocation's persisted lifecycle state and remain observable.
- Peer agents and the operator are not recipients of lifecycle reports.
- A lifecycle report cannot cause a model turn or another lifecycle report through incoming-message injection.
- An omitted, broadcast or non-controller audience is rejected with a classified, actionable error.
- Defensive filtering does not prevent later genuine messages from being delivered or acknowledged.
- Regression tests include two simultaneously running adapter fixtures and prove bounded message counts with no lifecycle-induced turns.
- Result acceptance, bounded completion recovery and ordinary peer/controller messages continue to work.

### Existing executions

Source changes will not update already-running adapter processes. Assess affected execution traces for extra turns and token usage; disclose any intervention or contamination when reporting an eval. Do not cancel, restart or resubmit an execution automatically as part of this fix.

## CTRL-002 — Log viewer scrolling, rendering and inspection are unreliable

**Priority:** High

**Status:** Open

The affected terminal UI is archived in [Monitor’s `.tui_deprecated`](../monitor/.tui_deprecated/README.md). The replacement web monitor intentionally omits logs; CLI logs and event streams remain supported. Retain these findings as evidence for any future web log viewer.

### Reported problems

- Scrolling loses the log line being read as new output arrives.
- Log lines render inconsistently; navigation does not behave like a conventional terminal log viewer.
- Terminal text cannot be highlighted reliably for copying.
- Raw-message inspection uses a small dialog, leaving too little space for the full message.
- JSON is not pretty-printed, making structured records difficult to read.
- The build tool-failure count has no direct drill-down showing the failed calls, their arguments and diagnostic output.

### Investigation notes

Potential contributors identified in the current code:

- Raw scrolling uses an offset measured backward from the live tail rather than a stable content anchor.
- Partial records can acquire different timestamps when later transport chunks arrive; selection matching depends on those timestamps.
- Selected-agent output is refreshed from a bounded tail, which can replace previously visible content.
- Raw activity rendering operates on transport chunks rather than consistently reconstructed records.
- Inspection dialogs cap their width at 88 columns.
- Application mouse capture can prevent native terminal selection.

Relevant code: [log rendering](../monitor/.tui_deprecated/dashboard/logs.go), [refresh and navigation](../monitor/.tui_deprecated/dashboard/dashboard.go), [inspection dialogs](../monitor/.tui_deprecated/dashboard/actions.go), and [content formatting](../monitor/.tui_deprecated/dashboard/markdown.go).

### Tool-failure evidence

During inspection of execution `fc25ef9d-711e-4147-b58b-e1bf28f9d6ff` (GO-001 run `GO-001-20261101T211413927528Z-bee829`), the build session contained nine failed native tool calls:

- Seven calls repeatedly queried `go doc` with language concepts or nonexistent package/symbol names (CTRL-003).
- One test invocation failed because a GET response contained a trailing newline while the create response did not; the test compared their complete response bodies. Resolution was not established by this inspection.
- One scratch-program compilation failed because of missing/unused imports; a subsequent call corrected the imports and succeeded.

The monitor counter is cumulative: each `tool_execution_end` with `isError: true` contributes one failure. A call containing several failing shell commands still counts once. A later successful call does not erase an earlier failure. These are tool-call failures, not necessarily failed workflow attempts or unresolved defects. Inspection must make that distinction clear without inferring recovery from an unrelated later success.

### Proposed scope for review

- Preserve the selected record/line while paused, including across new output and chunk completion; distinguish following from paused state.
- Provide consistent line/page/start/end navigation and intentional wrapping.
- Reconstruct records before presentation while preserving original output for inspection and copying.
- Use available terminal space for full-message inspection, with access to all content.
- Pretty-print JSON/JSONL without changing numbers or underlying evidence.
- Define how mouse-wheel navigation and native terminal text selection coexist; support complete-message copying.
- Make monitor retention limits explicit and distinguish them from persisted logs.
- Let users inspect/filter failed calls from the failure count, correlating call ID, timestamp, tool name, arguments and complete result. Preserve access to the original record and distinguish cumulative failures from workflow status.

### Acceptance criteria

- New output and split-record completion do not move a paused reader to another record.
- Long, multiline and Unicode records fit terminal bounds without hiding their final lines.
- Complete messages can be inspected and copied, including content outside the visible viewport.
- JSON is readable and preserves large integers; original content remains available.
- Mouse, keyboard and follow controls are documented and verified at representative terminal sizes.
- Regression tests cover live appends, partial records, retention boundaries, resizing, navigation and inspection.
- Failure inspection includes every counted failed call exactly once, without duplicating equivalent native message records. Multiple shell failures within one tool call count once; subsequent success does not remove the historical failure.

Review the interaction design before implementation. No fix is authorized by this issue.

## CTRL-003 — Repeated invalid documentation queries waste build-agent work

**Priority:** Medium
**Status:** Open
**Primary ownership:** Registry Go agent instructions and skills; Controls supplies execution evidence.

### Observed behavior

In the GO-001 build session identified in CTRL-002, seven failed shell calls repeatedly used `go doc` as a language-concept search engine. Queries included `goroutine`, `spawn`, `go func`, `chan`, `channel`, and nonexistent packages such as `go/spawn` and `internal/goroutine`. The agent continued guessing after receiving explicit missing-symbol/package errors. One query also produced lengthy Go command documentation rather than evidence relevant to the implementation.

The existing Registry `go-planning-evidence` skill recommends targeted `GOTOOLCHAIN=local go doc package.Symbol` lookups for consequential library claims. It does not require these invalid queries for Go language constructs. The observed research strategy failed; the reason the model persisted has not been established.

### Proposed change

- Distinguish language-specification questions from package/API documentation lookups in the Go agent guidance.
- Require a change of research strategy after repeated equivalent lookup failures rather than more guesses at package names.
- Keep discovery scoped to an actual implementation question and the selected dependency/toolchain version. State consequential uncertainty when evidence cannot be obtained.
- Use recorded tool calls to evaluate whether the revised guidance reduces repeated failed research. Keep language-specific policy in Registry, not the generic control plane.

### Acceptance criteria

- Go guidance explains when to consult the language specification versus `go doc package.Symbol`.
- A representative agent eval demonstrates that repeated semantic lookup failures lead to a different evidence source or an explicit uncertainty, with the relevant calls retained for review.
- Reporting distinguishes these avoidable research failures from legitimate failing tests and ordinary corrected experiments.

## CTRL-004 — Build-agent commands leave the configured execution workspace

**Priority:** High
**Status:** Open

### Observed defect

The pinned configuration for execution `fc25ef9d-711e-4147-b58b-e1bf28f9d6ff` selects a worktree through `variables.workspace`, but multiple build-agent shell calls explicitly change directory to the original run project instead:

```text
Original project:
  runs/GO-001-20261101T211413927528Z-bee829/project

Configured workspace:
  runs/GO-001-20261101T211413927528Z-bee829/project/.factory/worktrees/
  fc25ef9d-711e-4147-b58b-e1bf28f9d6ff/
  runs/GO-001-20261101T211413927528Z-bee829/project
```

Both paths are relative to the eval repository; the configured workspace is shown across lines for readability. The affected shell calls explicitly used `cd` into the original project. Their inspections and checks therefore did not run against the selected worktree. The extent of any resulting divergence between edits, checks, commits and evaluated output remains to be assessed; do not assume every operation used the wrong directory.

### Investigation required

Trace the source-directory context supplied by Controls, the Registry launch scripts and the agent instructions. Controls exposes the factory configuration directory, while Registry scripts select the working source directory. Determine why the original path remained an attractive execution target after workspace selection. The observed directory mismatch is confirmed; its precise prompting/launcher cause is not yet established.

### Required outcome

- Establish an unambiguous source-workspace contract across launch, agent instructions, checks and commit hooks. Distinguish source files from execution documents under `files_dir`.
- Preserve script-owned workspace selection. Controls must remain generic: no Git-specific workspace discovery, branch logic or worktree management.
- Verify that edits, checks, commits and eval grading refer to the same selected source workspace; detect and disclose existing contaminated evidence.
- Document that path guidance is not filesystem isolation. Any enforcement or sandbox proposal must be explicit rather than implied by a launcher changing directory.

### Acceptance criteria

- Regression coverage uses different configuration and source directories, including a script-selected workspace, and verifies consistent path propagation to the runner and artifact handling.
- A follow-up eval records the actual working directories of checks and source operations, with no unintended writes to the original seed project.
- Execution documents remain in the configured execution-files directory; product grading reads the selected source workspace.
- Non-Git factories remain supported without special-case control-plane behavior.

Investigation and design review must precede implementation. No execution intervention is authorized by this issue.
