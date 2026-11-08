# Issues

## CTRL-001 — Runtime lifecycle reports broadcast into agent conversations

**Priority:** High
**Status:** Implemented — 2026-11-05

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

### Implementation

- Pi discovers one controller, addresses status reports explicitly, and filters lifecycle messages from native input without blocking cursor acknowledgments.
- The communication service rejects omitted/empty/duplicate/extra lifecycle recipients before audience expansion or effects.
- Regression coverage uses concurrent workers and two real adapter processes with fake native runtimes. Historical broadcasts cannot create model turns; genuine peer handoffs and accepted completion still work. Human audit visibility is retained without adding human recipients.
- Source-only repair: no live execution was restarted, modified or resubmitted.

### Existing executions

Source changes will not update already-running adapter processes. Assess affected execution traces for extra turns and token usage; disclose any intervention or contamination when reporting an eval. Do not cancel, restart or resubmit an execution automatically as part of this fix.

## CTRL-002 — Persist inspectable tool-failure evidence with per-factory retention

**Priority:** High
**Status:** Open — expanded 2026-11-07
**Ownership:** Controls evidence ingestion, persistence, API, CLI, and retention; native adapters supply observations; Monitor provides inspection.

### Problem

The cumulative tool-failure counter is useful and must remain. It does not explain which calls failed, what arguments were supplied, what the tool returned, or whether an explicit later action resolved the failure. An operator must currently reconstruct that evidence from raw session output.

The control plane must persist the evidence behind each counted failure and make it directly inspectable in Monitor. This is not a request for another counter, browser-only storage, or a general-purpose web log viewer. Evidence must survive process exit, browser closure, and daemon restart for its configured retention period.

Full tool arguments and results can be large and sensitive. Their retention must be an explicit, validated setting in the root `factory.yaml`, pinned with the execution rather than an undocumented UI limit or a daemon-global assumption.

### Tool-failure evidence

During inspection of execution `fc25ef9d-711e-4147-b58b-e1bf28f9d6ff` (GO-001 run `GO-001-20261101T211413927528Z-bee829`), the build session contained nine failed native tool calls:

- Seven calls repeatedly queried `go doc` with language concepts or nonexistent package/symbol names (CTRL-003).
- One test invocation failed because a GET response contained a trailing newline while the create response did not; the test compared their complete response bodies. Resolution was not established by this inspection.
- One scratch-program compilation failed because of missing/unused imports; a subsequent call corrected the imports and succeeded.

The monitor counter is cumulative: each `tool_execution_end` with `isError: true` contributes one failure. A call containing several failing shell commands still counts once. A later successful call does not erase an earlier failure. These are tool-call failures, not necessarily failed workflow attempts or unresolved defects. Inspection must make that distinction clear without inferring recovery from an unrelated later success.

### Current implementation boundary

- [Telemetry events and summaries](internal/telemetry/telemetry.go) maintain cumulative failures and active tools; they are not a complete persisted failure-evidence model.
- [Pi telemetry extraction](adapters/pi/telemetry.mjs) emits `tool_finished` with call ID and failed status; the normalized event does not carry the complete tool result. Start-event command summaries are bounded and are not a substitute for exact stored arguments.
- [Session output capture](internal/sessions/manager.go) retains raw output separately. Preserving that output is valuable, but requiring operators or Monitor to reparse an entire native session is not an inspection API.
- [Factory configuration](internal/factoryconfig/load.go) currently has no tool-failure retention field. The configuration example below is proposed, not supported syntax.
- Native adapters own runner-specific extraction. Controls and Monitor must consume a runner-neutral evidence contract; do not teach the generic controller to parse Pi records.

### Evidence record and storage requirements

Persist an execution-owned failure record with enough information to identify and inspect the actual failed invocation:

| Field group | Required information |
| --- | --- |
| Identity | Stable failure ID; execution; stage/step when applicable; visit; attempt; session; native tool-call ID scoped to that invocation |
| Tool | Tool name, structured arguments or an exact retained representation, command when applicable, observed working directory if supplied by the runtime |
| Timing | Start/end timestamps when known, observation/ingestion time, and duration when derivable; distinguish observation time from native event time |
| Result | Failure signal, error classification/message, exit status or terminating signal when available, structured result, and stdout/stderr or combined output as actually reported |
| Supporting evidence | Stored attachments or bounded result blobs explicitly supplied by the runtime, plus references to surrounding persisted activity where available |
| Integrity and completeness | Digests/byte counts of stored representations, extraction/schema version, redaction indicator, truncation/omission state and reason, original size when known |
| Lifecycle | Retention policy snapshot, expiry eligibility, evidence availability state, deletion timestamp/reason, and explicit recovery links or assessments when supported |

Requirements:

- Write evidence durably in Controls-owned storage with a database index. Decide inline versus blob/file storage based on size; keep large results out of the frequently rewritten execution JSON projection.
- Preserve structured result data and native provenance alongside readable normalized fields. Never invent an exit code, separate stderr stream, working directory, or recovery status that the runner did not report.
- Handle concurrent calls, late/out-of-order completion, split transport records, duplicate native records, delivery replay, abrupt process exit, and missing start events. Correlation must survive restart without attributing evidence to a different attempt.
- Make evidence indexing and the cumulative counter consistent. Prefer one idempotent persistence boundary or an explicitly recoverable linkage so a counted failure cannot silently lose its evidence reference.
- If a runtime reports a failure count without detailed evidence, preserve the count and expose `unavailable` or equivalent with its reason. Do not fabricate inspectable records or imply full coverage.
- Store associated files only when explicitly supplied/authorized by the runtime contract. Do not open arbitrary host paths discovered in error text. A path printed by a tool is not proof that an attachment was captured.
- Sanitize secrets before durable evidence storage and apply equivalent controls to downloads, exports, and raw representations. Preserve the redacted stored representation faithfully; do not promise unredacted originals as a fallback.
- Define capture-size limits, storage-full behavior, and overflow/truncation indicators. Retention bounds time, not burst size; avoid unlimited argument/result ingestion. Do not silently convert storage failure into complete evidence.

### Proposed factory.yaml retention configuration

Design proposal only; review final naming and defaults before implementation:

```yaml
name: golang
retention:
  tool_failure_evidence:
    days: 30
```

This is a fragment of a complete factory configuration. The proposed `days` value is a positive integer representing consecutive 24-hour periods. The suggested default is **30 days**, not a claim about current behavior. Reject zero, negative, fractional, malformed, or out-of-range values; do not overload zero to mean both disabled and forever. Choose and document a finite supported maximum. An unlimited-retention option is not required for the first implementation.

Retention semantics to implement and document:

1. **Per execution, frozen at submission.** Resolve defaults and pin the policy in the execution configuration. Changing a project's `factory.yaml` affects future executions; it must not silently shorten an existing execution's evidence retention.
2. **Protect active work.** Start the proposed retention clock when the execution reaches a terminal state, not when one tool call fails. Running, blocked, waiting-for-human, and otherwise resumable executions retain their evidence. Define how explicit future continuation/reopening affects eligibility before enabling it.
3. **Preserve measurements.** Expiring large argument/result/attachment payloads does not decrement historical failure counts or erase failure identity and timing from retained execution history. Keep a lightweight tombstone with availability and expiry information until the parent history itself is removed. The UI may show, for example, nine historical failures with three payloads expired.
4. **Use authoritative timestamps.** Compute eligibility from persisted terminal/retention timestamps, with documented UTC calculations. A pending review or supported explicit reference that requires evidence must not be broken without a defined retention rule.
5. **Control-plane cleanup.** Run bounded, resumable cleanup independently of an open browser. Catch up after daemon downtime; coordinate concurrent cleanup workers safely and delete only owned evidence. Expired payloads must not reappear through replay/backfill. Expose cleanup backlog/errors rather than claiming deletion at the eligibility instant.
6. **Account for duplicate storage.** If the same content remains in raw native logs, activity rows, report snapshots, caches, exports, or copied attachments, expiring one evidence blob does not delete those copies. Inventory those paths and decide whether this policy governs each owned copy, requires compatible retention, or explicitly excludes it. Do not market an evidence TTL as a complete erasure guarantee while identical payloads remain indefinitely in owned raw logs. User-downloaded exports are outside automatic control-plane cleanup and must be identified as such.
7. **Separate automatic expiry from explicit deletion.** Define interactions with execution deletion and `factory prune --all`. An explicitly authorized parent-data deletion can remove evidence sooner; ordinary `factory prune` must not expire retained failure payloads merely because they are files. Foreign source files, worktrees, agent bundles, and external services are never evidence-retention cleanup targets.
8. **Apply availability consistently.** An expired/purged record remains distinguishable from evidence that was never captured, is partially captured, or is temporarily unreadable. API, Monitor, CLI, and reports must use the same availability states.

### Monitor and CLI inspection

- Make the cumulative failure count on execution and step details an actionable link/button to the corresponding failure list. Keep the existing count and its cumulative meaning.
- Provide filtering by stage/step, visit, attempt, session/agent, tool, and time, with stable ordering and pagination. Keep concurrent calls distinct.
- Selecting a failure opens a large, scrollable details view with identity/timestamps, arguments, structured results, error/exit information, and retained attachments. Long content must not be confined to a tiny dialog.
- Support readable JSON with preserved numeric precision, selectable/copyable text, and safe downloads of retained evidence. Show full retained content or explicitly indicate truncation; do not silently hide the tail.
- Link to surrounding CLI activity/logs using stable IDs/cursors when available. Do not require building a general web logs page or loading the entire native transcript into the browser.
- Display expiry policy/deadline, availability/completeness, redaction, truncation, and capture gaps. For expired evidence, retain the metadata row and explain why the payload cannot be opened.
- Expose the same evidence through an inspection API and CLI path so external agents and reports do not need a browser or their own Pi log parser. Propose minimal CLI/API additions for review rather than proliferating commands.
- Never equate a historical tool failure with a failed workflow. Recovery/resolution is separate attributed information, and an unrelated later successful call is insufficient proof.

### Acceptance criteria

- Every detailed counted failure has a stable inspectable record exactly once, including concurrent calls and repeated native deliveries. Equivalent records are deduplicated within the correct invocation, not globally by a reusable native call ID.
- A tool call containing several failing shell commands counts once. A later success does not erase the historical failure or its evidence.
- Persisted evidence survives agent/process exit, daemon restart, and browser closure. Monitor and CLI agree on counts, identities, contents, and availability.
- Operators can drill down from execution/step counts to the exact failed call and read/copy the complete retained arguments/result. Missing streams, exit codes, or attachments are explicitly marked unknown/unavailable.
- Automated fixtures cover long/multiline/Unicode content, large JSON integers, concurrent and out-of-order calls, missing start records, split records, duplicate delivery, abrupt exit, and a failure followed by correlated recovery.
- Redaction tests cover credentials in arguments, URLs, headers, stdout/stderr, nested structured data, and attachments. Original secret values must not leak through alternate evidence endpoints or exports covered by the policy.
- Retention configuration is validated and pinned. Tests use a controlled clock for expiry boundaries, policy defaults, configuration changes between executions, blocked/human-wait protection, and service downtime.
- Expiry removes eligible owned payloads/attachments without lowering historical counts; a tombstone remains while parent history remains. Repeated/concurrent cleanup is idempotent and does not resurrect expired data.
- Duplicate owned storage has documented retention semantics, tested for the selected scope. Reports distinguish historical counts from currently available detailed evidence.
- Cleanup failures, partial capture, quota/size limits, and expired versus never-captured evidence are observable. Tests prove no source workspace or unrelated files are removed.
- No paid model calls are needed for the ingestion, inspection, and retention regression tests. A separate authorized eval validates the operator experience.

### Historical TUI findings

The original report concerned scrolling losing the selected line, chunk-based rendering, small inspection dialogs, unreadable JSON, and mouse capture interfering with selection. The affected implementation is archived in [Monitor’s `.tui_deprecated`](../monitor/.tui_deprecated/README.md). Retain these as design evidence; repairing that UI is not the current scope. New viewers should preserve paused selection and distinguish stored evidence from the visible tail.

### Related work and non-goals

- CTRL-009: database outages and bounded persistence behavior also apply to evidence ingestion.
- CTRL-010: paginated summaries and incremental activity should be reused rather than adding large failure payloads to every execution-list response.
- CTRL-013: behavioral reports must preserve measurement provenance and distinguish missing from expired evidence.
- CTRL-014: artifact-path failures provide a concrete inspection example; evidence retention must not automatically move or repair misplaced product artifacts.
- Do not add a new agent, model-based failure classifier, general artifact-lifecycle framework, or general-purpose web log viewer to deliver the first useful version.
- This issue defines required persistence and UX outcomes plus a proposed retention shape. It does not authorize code changes, data deletion, live workflow intervention, or repair/regrading of prior evals.

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
**Status:** Implemented — 2026-11-05; live model-compliance eval pending

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

### Investigation and implementation

The launchers already changed into the configured source workspace. The generic assignment nevertheless prominently included the original `factory.yaml` directory, and native injection did not state its actual working directory. That conflicting context is a confirmed contributing defect; code inspection alone cannot establish why the earlier model chose each `cd`.

- Removed the configuration-directory path from the generic source instructions. The native Pi bridge now identifies its actual process working directory after script-owned selection as authoritative for source reads, edits and checks.
- Clarified Registry role instructions and the Go factory's build instruction. Execution-file paths and host artifact collection remain separate and unchanged; Controls gains no Git logic or script-specific workspace discovery.
- Regression tests exercise a script-selected plain directory through real managed MCP completion/artifact collection, native Pi input with distinct source/evidence locations, and the actual Registry coder/reviewer launchers followed by the configured commit hook in a private Git fixture. They check that the original checkout receives no product edits.
- This is path/context correctness, not filesystem enforcement. An unconstrained host agent can still leave its working directory; sandboxing is separate.
- A fresh live-agent eval is still required to assess model adherence. No live execution or historical eval was repaired, restarted, regraded or resubmitted. Prior directory-mismatched evidence remains unsuitable for a clean comparison until its source provenance is established.


## CTRL-005 — Version-aware runner adapters must apply per-agent factory overrides

**Priority:** High
**Status:** Implemented
**Prerequisite:** [CTRL-006](#ctrl-006--formalize-the-agent-runtime-adapter-boundary) owns the generic runtime boundary, preparation contract and conformance fixture. This issue implements Pi-specific override/version support against that contract.
**Ownership:** Controls owns runner-specific translation and compatibility; Registry owns agent bundles and native defaults.

### Problem

A team must be able to reuse a Registry agent while selecting its own provider, model, API endpoint, credential environment variable and supported model options in `factory.yaml`. Editing each downloaded agent bundle is fragile, duplicates configuration and makes updates difficult. Global provider defaults are insufficient: the architect, builder and reviewer may deliberately use different providers, models and credentials in the same execution.

Passing arbitrary JSON to a package does not implement this behavior. Someone must know how a particular runner stores configuration, selects its model, resolves credentials and launches a session. For Pi, that knowledge includes `settings.json`, `models.json`, command-line options and any relevant runner-version differences. It must not be independently reimplemented in every Registry role's launcher or left for the model to interpret.

### Current implementation and gap

- [Package configuration](docs/packages.md) already supports a per-registration `overrides` object. Controls pins those values and passes them through the private `FACTORY_PACKAGE_CONFIG` file. The explicit Pi adapter translates supported fields into a private native profile; generic packages retain their own schemas.
- [Factory configuration loading](internal/factoryconfig/load.go) treats packages generically. The `agents` form is accepted shorthand for the same registration mechanism.
- [Runtime adapters](adapters/README.md) already belong to Controls. The [Pi integration](adapters/pi/pi-rpc-session.mjs) handles native RPC sessions, communication and telemetry.
- Registry agents contain their own Pi defaults and launchers. For example, [coder_golang](../registry/agents/coder_golang/start.sh) copies its bundled Pi profile into a private directory for a standalone invocation and invokes the managed adapter when Factory supplies communication settings. That does not establish a validated provider/model override contract.
- [Pi preparation](adapters/pi/prepare.go) owns native translation, supported-version validation, offline preflight and safe resolution metadata. The RPC bridge verifies actual launched selection before its first assignment.

Supported configuration, precedence and limitations are documented in [Pi overrides](docs/pi-overrides.md).

### Architectural boundary

Provide an adapter per **runner**, not per agent role. Architect, coder and reviewer bundles running Pi share the Pi adapter. A Claude Code integration would have its own adapter. Deterministic script/classifier packages remain valid without an LLM runner adapter.

| Layer | Owns | Must not own |
|---|---|---|
| Factory definition | Package selection and explicit per-package overrides | Native Pi file construction or plaintext credentials |
| Registry agent bundle | Native default profile, role instructions, skills, MCPs and tools | A separate copy of Pi override/version translation |
| Runner adapter in Controls | Supported override schema, runner-version compatibility, native configuration preparation and launch integration | Workflow routing, domain decisions or edits to the published bundle |
| Generic execution/control plane | Scheduling, invocation isolation, persistence and the adapter-neutral execution contract | Pi JSON fields, provider-specific defaults or runner-specific configuration branching |

Add Go configuration preparation under `adapters/pi`, with clear responsibilities such as `config.go`, `version.go` and `launch.go` where warranted. Keep the adapter-neutral invocation interface outside the Pi implementation; application composition connects implementations. Generic controller, engine and execution packages must not import Pi-specific configuration code.

The existing JavaScript Pi session integration may remain where native Pi APIs make it appropriate. This issue does not require rewriting it in Go. Define one owner for each preparation task, including MCP injection, so Go preparation and the session adapter cannot apply competing profile mutations.

### Factory configuration

Use the existing package-level `overrides` mechanism for overrides rather than introducing an unrelated root-level provider defaults block. Implemented configuration fragment:

```yaml
packages:
  - name: builder
    path: ../registry/agents/coder_golang
    entrypoint: start.sh
    runtime:
      name: pi
      version: =1.0.3
    overrides:
      provider: corporate-gateway
      model: coding-model
      base_url: https://ai.example.com/v1
      api: openai-completions
      api_key_env: CORPORATE_AI_KEY
      thinking: medium
      model_config:
        reasoning: true
        context_window: 128000
        max_tokens: 8192
  - name: reviewer
    path: ../registry/agents/code_reviewer_golang
    entrypoint: start.sh
    runtime:
      name: pi
      version: =1.0.3
    overrides:
      api_key_env: REVIEW_AI_KEY
```

`api` identifies the native API protocol, not the provider's display name. The supported protocol spellings and native mappings are documented in [Pi overrides](docs/pi-overrides.md). Provider endpoint and credential references must be resolved coherently with the selected model's native definition.

Adapter selection is explicit through `runtime.name`; do not infer it from roles, paths or native files. Pi initially supports only tested version 1.0.3.

### Resolution semantics

1. Begin with the selected agent package's native defaults. With no overrides, preserve its existing provider, model, tools, MCPs, skills, extensions and instructions.
2. Apply only explicitly supplied override fields. Distinguish absent values from invalid empty values; do not use an empty string to accidentally select an ambient user default.
3. Translate supported fields deliberately. Do not perform an unrestricted recursive JSON merge or forward unknown options to Pi and hope they take effect.
4. Validate the final provider/model combination. A provider switch must not retain the former provider's API key reference, endpoint, protocol or incompatible model definition. A model-only change within a provider must resolve a known model definition or receive the additional metadata required by the supported Pi version.
5. Respect model capabilities, context/output limits and supported thinking options. Do not invent metadata or silently discard an unsupported option. Document which additional settings are required for a custom model.
6. Define the interaction with explicit native command-line flags. Factory overrides must not be undone by bundled launch arguments. Reject conflicting values or apply one documented precedence consistently; do not rely on accidental argument order.
7. Prepare a private native profile for each invocation under its existing invocation runtime directory. Never modify checked-in agent files, another execution's profile or the user's global Pi configuration.
8. Apply overrides before credential/model preflight and before starting a model session. Preflight and launch must inspect the same resolved profile and executable.
9. Unsupported settings, malformed values, missing credentials and incompatible provider/model combinations produce concise, classified errors naming the relevant configuration field. No fallback to bundled OpenRouter settings or a globally configured provider after an explicit override fails.

Packages may use `overrides` for other purposes. Strict validation belongs to the selected package/adapter schema; a generic package must not be forced into a Pi or LLM schema. Separate adapter-owned settings from any package-specific settings explicitly if both share the object.

### Version compatibility

- Support Pi **1.x only initially**, within an explicitly documented and tested version range. Do not implement support for Pi below 1.0.
- Detect the actual executable's version using its supported version interface. Resolve the executable once and use that exact executable for detection, preflight and launch; do not rediscover a potentially different executable through `PATH` later.
- Distinguish the package's declared runner requirement, the adapter's supported range and the installed runner version. An invocation is allowed only when all applicable constraints agree.
- Do not assume every 1.x minor is compatible solely because the major is unchanged. Establish support using documented compatibility and fixture/integration coverage.
- Reject unsupported versions, missing executables and unparseable version responses before model invocation. Do not install or upgrade Pi automatically.
- Future Pi 2.x support requires an explicit compatibility decision. Introduce separate translation logic only when actual configuration/protocol differences require it; do not duplicate implementations speculatively.
- Record the runner version, adapter revision/version and resolved non-secret configuration for each attempt. Record the executable identity sufficiently to diagnose different installations.
- Keep requested settings in the pinned execution configuration. If a retry uses a different adapter/runner version, detect and disclose the difference and follow an explicit compatibility policy. Do not silently treat it as an identical invocation. This issue does not introduce a new resume-with-configuration mechanism.

### Credentials and environment references

- `api_key_env` is an environment-variable **name**, such as `CORPORATE_AI_KEY`, not the key value and not a shell expression.
- Resolve the credential through the execution service's environment at launch. A variable available only in the submitting terminal may be unavailable to an already-running service; document that boundary and report a missing variable by name.
- Translate the reference into the supported runner's native credential mechanism. For example, Pi may require an environment reference in `models.json`; confirm the exact syntax for supported versions rather than assuming it.
- Keep actual secret values out of authored/resolved factory configuration, prompts, messages, telemetry, errors, reports and configuration digests. Audit records may retain the variable name. Validate that overridden credentials cannot fall back to unrelated ambient authentication.
- Do not implement this through shell interpolation or commands stored in credential fields. If general environment-name remapping is needed, specify a constrained mapping separately; do not make this issue an unrestricted environment/secret injection feature.
- Private profile/configuration files require restrictive permissions and the existing invocation retention/cleanup policy. Do not serialize a full service environment into them.
- The host runner still inherits trusted service privileges. Selecting a credential reference or using a private profile is not credential isolation or sandboxing; do not claim otherwise.

### Implementation scope and integration

1. Consume CTRL-006’s adapter-selection and preparation contract; define the Pi-specific override schema, custom-provider/model requirements and precedence. Do not independently introduce another runtime descriptor or preparation interface.
2. Implement Pi configuration/version preparation behind that contract. Keep native Pi configuration knowledge out of generic execution and scheduling packages.
3. Implement version detection, compatibility validation and deterministic native profile generation in the Pi adapter.
4. Wire the managed launch path and preflight to use the generated profile. Consolidate the relevant duplicated Registry launcher behavior rather than introducing a second translation implementation. Preserve standalone agent operation and existing workspace selection.
5. Persist safe requested/resolved settings and compatibility metadata so users can inspect which provider/model was actually used. Keep resource telemetry's observed provider/model available to diagnose divergence from requested settings.
6. Update [package configuration](docs/packages.md), [factory configuration](docs/factory-yaml.md), [runtime adapters](adapters/README.md), [Pi adapter](adapters/pi/README.md), [factory.example.yaml](factory.example.yaml), and relevant Registry agent setup/launch documentation. Clearly distinguish working support from future runners.

### Required contributor guide: adding another agent runner

This issue is incomplete without robust contributor documentation explaining how to add support for another runner, such as Claude Code, without copying Pi-specific assumptions or modifying workflow semantics. Distinguish **adding a role/package for an already supported runner** from **implementing a new runtime adapter**. The former should not require writing Go integration code.

Extend the generic contributor guide and non-Pi fixture established by CTRL-006 with the implemented Pi configuration/version behavior. Do not create a competing guide or fixture. Provide the guide under `docs/`, linked from the root README, adapter index and package documentation. Use the implemented adapter-neutral interfaces as the authority; link to actual types and runnable examples rather than inventing a parallel prose-only contract. Keep runner-specific details in each adapter's own README.

The guide must cover:

1. **Boundaries and repository layout.** Show where Go configuration/version preparation, any native session bridge, shared SDK usage, tests and Registry bundles belong. Explain application-level adapter registration/selection and dependency direction. State what can be reused and what must be implemented for a new runner; no role-specific adapter copies or dynamic plugin ecosystem are required.
2. **Minimal end-to-end integration.** Walk from a small agent bundle and factory registration through adapter selection, version preflight, private configuration preparation, process launch, message delivery and accepted completion. Include a runnable fake-runner reference or fixture that works without provider credentials or paid model calls. Demonstrate both native defaults and a per-agent override. Label any illustrative configuration that is not executable.
3. **Configuration translation.** Document the preparation interface and its inputs/outputs, the package's declared runner/version requirements, native config sources, supported override schema, custom-provider/model metadata, validation and precedence. Show how to handle native CLI flags and unsupported settings without silently ignoring them. Native configuration files are runner-specific; do not require every runner to have Pi-style profile files.
4. **Invocation context and files.** Explain prompt and bound-input delivery, identity, working directory, execution-files versus source-workspace paths, private runtime directories, profile permissions, cleanup and credential references. Explain the execution-service environment boundary and how to avoid shell expansion, global config mutation and secret disclosure.
5. **Native session lifecycle.** Describe process startup/readiness, interactive input, operator steering, idle turns, termination, cancellation and cleanup. Define how native events map to the shared contract. Distinguish an idle model turn, process exit and an accepted step result. State required behavior on authentication/provider failure, malformed native output, unsupported features and unexpected process exit.
6. **MCP communication and completion.** Show endpoint/token provisioning, participant/controller discovery, assignment and communication-skill loading, incoming message delivery and acknowledgments, and `step_result` submission with acceptance handling. Explain how deterministic packages use the same protocol without an LLM skill. Document lifecycle-report routing and filtering against the implemented contract; do not reproduce CTRL-001's broadcast defect.
7. **Sequential and concurrent capabilities.** Explain how a runner receives messages while idle and remains available until its assignment is complete. Document capability validation and clear errors when a runner cannot support the requested interaction mode. Do not claim concurrent collaboration support solely because multiple processes can be launched. Explain how to avoid competing polling/delivery loops and repeated outgoing completion attempts.
8. **Telemetry and observability.** Specify the existing versioned resource-event contract, native output retention, provider/model attribution, usage categories, tool failures and progress events. State which observations are mandatory, optional or unavailable and how unsupported measurements appear. Do not fabricate token usage, prices, idle signals or successful delivery.
9. **Versioning and operational diagnostics.** Explain how to establish and test a compatibility range, detect the exact launched executable, evolve native configuration translation, record adapter/runner versions and handle retries after upgrades. Include concise troubleshooting examples for missing executables/credentials, invalid profiles, rejected results, unavailable MCP connections and incompatible versions.
10. **Verification and maintenance.** Provide exact commands for configuration fixtures, fake-process integration tests, concurrent invocation isolation, cancellation, credential redaction and protocol conformance. Explain how to add a new compatibility fixture and a real-runner smoke check, what each proves and what still requires an eval. List the documentation and examples that must change when introducing a runner or native breaking change.

Include a compact capability matrix for existing adapters, with unsupported and unverified behavior clearly identified, and a contributor checklist tied to the guide and tests. The matrix describes implementation support; it must not become a second independently maintained workflow configuration language.

### Acceptance criteria and verification

- A factory can run two Pi-based agents with different provider/model/API-key environment references without editing either package or using global defaults.
- The same agent package can participate in concurrent executions with different overrides; generated profiles, MCP credentials and configuration do not cross executions or attempts.
- With overrides omitted, the agent's native configuration and instruction/skill/tool/MCP selections remain intact. Adding a new provider preserves unrelated native configuration.
- Pi below 1.0, unsupported majors, versions outside the tested range, malformed version output and conflicting compatibility requirements fail before any provider request.
- Tests verify native `settings.json` and `models.json` content, credential-reference syntax, provider/model pairing, unchanged source profiles and precedence against conflicting launch arguments. Cover unknown options, missing credentials, unsupported thinking options, custom-model metadata and malformed native profiles.
- A fake native runner inspects the actual profile and arguments used by the managed launch path. Demonstrate that supplied overrides take effect rather than merely appearing in the pinned JSON or passing a unit-level merge test.
- Concurrent integration tests prove profile isolation and preserve the existing MCP delivery, accepted-result completion and resource-reporting contracts.
- Diagnostics and persisted metadata include relevant non-secret values and versions. Seed recognizable fake credentials in tests and assert they do not appear in logs, prompts, persisted records or reports.
- At least one documented supported Pi version is exercised through the actual startup/configuration path; compatibility fixtures represent every materially different supported configuration format. Do not require paid model calls for deterministic configuration tests.
- Generic scripts/classifiers still launch without Pi-specific configuration or dependencies. Registry roles do not each implement their own provider/model override translation.
- The contributor guide is linked from the root README, adapter index and package documentation, and distinguishes new roles from new runners. Its examples and commands match the implemented interfaces.
- A contributor can follow the guide to register and execute the fake runner, apply an override, exchange scoped messages and obtain an accepted result without modifying controller scheduling or Pi code. Required fixtures run in the normal verification suite.
- Every supported adapter documents its compatibility range, native configuration mapping and capability limitations. Documentation does not describe unavailable telemetry, automatic delivery or concurrent collaboration as supported.

### Non-goals

No workflow redesign, role-specific adapter packages, global provider defaults, automatic Pi upgrades, Pi <1 support, speculative Pi 2 support, new secret manager, container runtime, or unrestricted native JSON patching. No changes to active executions are authorized by recording this issue.


### Implementation record

- Replaced package `with` with `overrides` throughout active Controls/Registry configuration, examples and documentation. Unknown authored fields are rejected; no compatibility alias.
- Added a finite Pi schema and invocation-private native translation, credential-reference precedence, offline native RPC preflight and actual-session verification before assignment. No global provider defaults or changes to bundle files.
- Added safe native resolution metadata and same-visit retry fencing for changed runner/adapter versions. Pi 1.0.3 remains the sole supported release.
- Added schema, malformed/unsupported configuration, private credential/profile, concurrent preparation, prepared-process launch, actual-session conflict and retry regression tests. Existing non-Pi conformance coverage remains independent of Pi.
- Updated [factory.example.yaml](factory.example.yaml), [Pi configuration guide](docs/pi-overrides.md), contributor documentation, Registry docs and published architecture references.
- Native smoke verification uses installed Pi 1.0.3 in offline mode and asserts zero provider requests. No paid model calls, live eval, production service restart or commits.
- Verification passed: `go test ./...`, `go vet ./...`, race tests for adapters/command runner/sessions, Controls npm tests (12 passed), Registry tests (30 passed; optional installed-skill-loader test skipped). The separate installed-Pi override smoke check passed.

## CTRL-006 — Formalize the agent runtime adapter boundary

**Priority:** High
**Status:** Implemented — 2026-11-04
**Scope:** Define and establish the generic runtime boundary; preserve existing workflow behavior.

### Problem and evidence before implementation

The control plane has a generic execution interface, but native runner integration is not yet a complete, independently verified contract. Adding provider/model override logic now risks distributing more native-runner knowledge between Controls, Registry shell scripts and the Pi session bridge.

Source evidence at issue creation:

- [execution.Runner and Work](execution/runtime.go) expose `Run(context.Context, Work) (StepResult, error)`. This separates package execution from scheduling but does not define native configuration preparation or capability validation.
- [PackageSnapshot](execution/workflow.go) stores a `Runner` string and opaque settings. The [configuration loader](internal/factoryconfig/load.go) currently records every package as `Runner: "command"`, identifying the host execution mechanism rather than the native agent runner.
- [Command runner](internal/commandrunner/runner.go) provisions MCP identity, writes invocation files and invokes a package entrypoint through [session supervision](internal/sessions/manager.go).
- Registry entrypoints prepare native defaults and select the [Pi RPC adapter](adapters/pi/pi-rpc-session.mjs); that adapter injects communication configuration and starts Pi. Preparation ownership is therefore distributed across conventions rather than one explicit contract.
- Generic MCP result acceptance and versioned telemetry already exist. Existing fake-Pi tests cover native Pi behavior, but do not independently demonstrate that the full integration boundary supports another runner.

This is a boundary-design and implementation task, not a request to replace the working execution architecture.

### Required decisions and deliverables

#### 1. Establish vocabulary and ownership

Distinguish:

| Concept | Responsibility |
|---|---|
| Agent package | Work instructions, skills, tools and native default configuration |
| Native runner | The agent program, such as Pi or another agent CLI |
| Host execution mechanism | Currently supervised local command execution; distinct from the native runner |
| Runtime adapter | Translation between a native runner and Controls’ invocation, messaging and observability contracts |
| Control plane / workflow engine | Execution state, scheduling, routing, authorization and accepted results |

Record the current launch sequence and the intended sequence. Assign one owner to version detection, configuration validation/preparation, profile creation, credential references, MCP injection, instruction/skill loading, process launch and cleanup. Eliminate conflicting preparation responsibilities rather than adding another layer of wrappers.

Do not introduce adapters per role. Pi-based architect, builder and reviewer packages share the same runtime adapter. Deterministic packages must remain runnable without an LLM or Pi dependency.

#### 2. Define explicit adapter selection and identity

Define how a package declares its native runner and compatibility requirements, how Controls selects the adapter, and how that identity differs from host execution mode. Do not infer runner identity from role names, directory layout or the presence of native configuration files.

Specify any necessary changes to factory/package schema, pinned configuration and recorded session metadata. Include concrete configuration examples, validation errors and dependency direction. Keep selection static and explicit; no dynamic plugin discovery or new package ecosystem.

#### 3. Establish a small preparation contract

Define typed, adapter-neutral preparation inputs and outputs consistent with the existing `execution.Runner` and supervised-session interfaces. Cover package defaults, opaque adapter settings, invocation identity/directories, working-directory semantics, prompt/input paths, credential references, compatibility checks, launch details, safe resolved metadata and cleanup ownership.

Decide which validation can occur when the factory is loaded and which requires the execution host immediately before launch. Keep secrets out of pinned definitions and audit records. Native file names such as Pi’s `settings.json` and `models.json` belong inside their adapter, not in the generic interface.

Explain why each interface method and field is needed. Do not add an abstraction merely to mirror the current Pi implementation. The preparation contract must accommodate a runner configured by arguments or another native mechanism rather than requiring profile files.

#### 4. Define session behavior and capabilities

Separate required package behavior from optional agent capabilities. Specify readiness, operator input, idle-session delivery, lifecycle observations, accepted completion, native failure, cancellation and process cleanup. Distinguish process exit, idle turns and a durably accepted result.

Define how capabilities are declared and checked before launch, including the requirements for concurrent collaboration. A runner that can launch a process but cannot deliver messages to an idle session must not be advertised as supporting that interaction mode. Define actionable failures and truthful representations of unavailable telemetry; do not fabricate usage or infer idle from quiet output.

Preserve the existing MCP and telemetry protocols where possible. Incoming delivery ownership must be unambiguous, and lifecycle reports must not become peer conversation input. CTRL-001 separately owns correction of the current broadcast defect; document the intended controller-only behavior without silently treating it as already fixed.

#### 5. Demonstrate the boundary independently of Pi

Implement a minimal non-Pi fake runner and reusable conformance tests. The fixture must not emulate Pi RPC events or depend on Pi profile files. Exercise the actual managed invocation path, including preparation, scoped MCP messages, accepted completion, native failure, cancellation and concurrent invocation isolation where advertised.

Verify that unsupported capabilities are rejected before launch. Retain existing Pi integration tests; the independent fixture supplements rather than replaces them. No paid provider calls are needed.

#### 6. Document how to add support for another runner

Create the generic contributor guide requested by CTRL-005, linked from the root README, adapter index and package documentation. Cover the implemented interfaces, explicit selection, directory layout, invocation lifecycle, communication and telemetry responsibilities, credential handling, compatibility, test commands and a runnable fake-runner walkthrough. Clearly distinguish adding a role for an existing runner from implementing a new runtime adapter.

Include an implementation capability matrix and contributor checklist. CTRL-005 will extend this same guide with actual Pi override and version behavior; avoid parallel documentation that drifts from the interface.

### Acceptance criteria

- Vocabulary, ownership, launch flow and selection semantics are documented with links to actual implemented types and examples.
- Native runner identity is distinguishable from generic command execution in configuration resolution and operational records.
- Generic execution, controller and workflow-engine packages contain no native Pi configuration interpretation or runner-specific conditional branches. Composition selects implementations explicitly.
- Preparation has one owner per responsibility. Registry launchers do not duplicate Controls’ adapter translation; existing standalone agent operation remains supported.
- A non-Pi fixture passes conformance tests through the managed execution path without changes to scheduling or Pi code. Capability limitations are enforced and observable.
- Existing accepted-result completion, peer/operator communication, process supervision, telemetry and invocation isolation remain covered. Workspace-selection behavior is preserved; CTRL-004’s separate defect is not implicitly fixed or hidden by this task.
- The contributor guide is sufficient to reproduce the fake-runner integration without reverse-engineering Pi scripts, and documents unsupported behavior accurately.
- CTRL-005 can implement native Pi overrides and compatibility checks against this boundary without inventing another adapter-selection or preparation abstraction.

### Non-goals and related work

No Pi provider/model override implementation (CTRL-005), workflow redesign, scheduler replacement, sandbox/container runtime, dynamic plugins, new secret manager or production integration for a second external agent. Do not expand this into a general agent framework. Related defects CTRL-001 and CTRL-004 retain their own scope. No active execution intervention or commits are authorized by recording this issue.


### Implementation record

- Added the [adapter preparation port](adapters/adapter.go), [native identity/capability types](execution/native.go), explicit `runtime` selection and pinned compatibility requirements.
- Application composition registers native adapters; command execution/scheduling remain independent of Pi configuration. Sessions persist host mechanism and native identity separately.
- Added Go Pi profile preparation, exact executable/version checks, and Registry managed-profile reuse. Initial tested compatibility is Pi 1.0.3. Provider/model overrides remain CTRL-005; lifecycle broadcast and source-workspace defects remain CTRL-001/CTRL-004.
- Added a [non-Pi MCP reference worker](adapters/conformance/worker/main.go) and [managed conformance tests](internal/commandrunner/conformance_test.go).
- Added [Adding an agent runtime](docs/runtime-adapters.md), capability/ownership matrices, contributor checklist and updated factory/Registry examples.
- Verification: full Go suite, `go vet ./...`, targeted race tests, Controls npm adapter tests and Registry tests. Registry's optional installed-Pi skill-discovery test remains skipped by default. No paid model calls, production service restart, eval intervention or commits.

## CTRL-007 — Make control commands recoverable across dispatch and persistence failures

**Priority:** High — first reliability workstream
**Status:** Open

### Problem and evidence

The workflow engine and Controls projection are separate durability boundaries. [Execution submission](internal/control/controller.go) creates a local execution, dispatches it, then records the engine run ID. Decision handling sends the decision before persisting the answered request. A crash, timeout, or failed write between these operations can leave an accepted command looking unaccepted, or local and engine state inconsistent. An uncertain dispatch response does not establish that the engine rejected the operation.

The CLI checks a returned failed execution, but the HTTP submission path can return a created response containing that failed record. Define consistent command semantics across CLI, MCP, and Monitor rather than relying on each caller to infer success differently.

### Required change

- Define durable command identities, status, and request fingerprints for submission, decisions, retry, and cancellation. Reject conflicting reuse of an identity.
- Persist command intent before dispatch; use an outbox or equivalent recoverable dispatch mechanism. Reuse the same identity for reconciliation and transport retries.
- Keep engine-specific deduplication and lookup behind the engine interface. Where deduplication cannot be guaranteed, expose an unresolved state rather than redispatching blindly.
- Define accepted, dispatched, confirmed, rejected, and unresolved behavior and public error semantics. A caller must be able to inspect the original command after losing its response.
- Avoid claims of atomicity or exactly-once side effects across independent systems.

### Acceptance criteria

- Failure injection covers crashes before/after dispatch, lost responses, and failed projection updates for each command type.
- Repeating one command cannot create duplicate executions or contradictory decisions; concurrent conflicting decisions have a deterministic result.
- Restart reconciliation resolves confirmed outcomes or explicitly retains uncertainty with actionable evidence.
- CLI, MCP, and Monitor expose the same command identity and outcome semantics.

### Scope

Prioritize this with CTRL-008 and CTRL-009. Do not redesign workflow syntax or move orchestration policy into agents.

## CTRL-008 — Reconcile executions and owned processes after service interruption

**Priority:** High — first reliability workstream
**Status:** Open

### Problem and evidence

[Supervisor startup](internal/sessions/manager.go) marks previously running sessions interrupted; that alone does not reconcile live host processes, engine position, accepted MCP results, pending messages, or hook effects. The local resource journal enables verified recovery shutdown but is not a complete execution recovery protocol. Agents are started before their ownership record is written, leaving a crash window between spawn and registration.

### Required change

- Specify recovery invariants for each execution, step visit, attempt, session, communication participant, hook invocation, and managed dependency.
- Close or explicitly account for the spawn/registration window, for example with a launcher registration handshake that prevents agent work until ownership is durable.
- Reconcile persisted state against verified process ownership, accepted results, and engine state before permitting replacement work. Never adopt a process from a PID alone.
- Define whether interrupted work is blocked, retried, or already completed. Preserve accepted results, evidence, and stable invocation identities.
- Handle hook side effects through their existing idempotency contract; reconciliation must not repeat an unverified external effect.

### Acceptance criteria

- Deterministic tests interrupt launch, result acceptance, decision delivery, hook execution, and shutdown at defined boundaries.
- Restart cannot silently dispatch duplicate work or accept stale results from superseded attempts.
- Surviving processes are reconciled or explicitly reported as unresolved; unrecoverable PTYs are not presented as reattached sessions.
- Repeated reconciliation converges without changing previously accepted outcomes or deleting source workspaces.
- Keep separate ownership for engine durability and host-process recovery; this is not a scheduler replacement.

## CTRL-009 — Bound output persistence and session finalization during database outages

**Priority:** High — first reliability workstream
**Status:** Open

### Problem and evidence

[Session output capture](internal/sessions/manager.go) retries failed database writes indefinitely using a background context. Process-exit handling waits for the capture goroutine even after closing the PTY. A persistent outage can therefore prevent finalization. Temporary backpressure is useful, but indefinite waiting is not a recovery policy.

### Required change

- Define bounded outage behavior, cancellation, and an observable degraded state for output persistence and session finalization.
- Evaluate a bounded local spool versus explicit termination after a configured outage limit. Document capacity, retention, replay ordering, deduplication, and disk-full behavior if spooling is selected.
- Preserve evidence when possible; explicitly report gaps rather than silently dropping output or claiming complete capture.
- Keep shutdown bounded even when the database remains unavailable. Reconcile pending final state when persistence returns.

### Acceptance criteria

- Tests cover temporary outages, sustained outages, cancellation, process exit during an outage, database recovery, and exhausted spool capacity if applicable.
- Shutdown/finalization cannot wait indefinitely for output persistence.
- Replayed output retains order without duplication, and incomplete evidence is visible in inspection/reporting.
- Resource use stays bounded; backpressure and termination behavior are documented.

## CTRL-010 — Make execution inspection lightweight, paginated, and incremental

**Priority:** Medium
**Status:** Open
**Ownership:** Controls read API/storage; Monitor consumption.

### Problem and evidence

Execution JSON records include pinned configuration, step history, requests, and a growing events array. [Projection updates](internal/control/store.go) serialize the complete record, and listing returns full execution records. Monitor polls execution data repeatedly. This creates increasing read, serialization, transfer, and write costs as history grows; large-scale performance has not yet been benchmarked.

### Required change

- Add a lightweight execution-summary query and stable cursor pagination for execution lists.
- Expose incremental activity/events using durable cursors; let Monitor request changes rather than repeatedly transfer complete histories.
- Separate large append-only histories from frequently updated projection state where measurement justifies it. Reuse existing durable activity storage where appropriate; avoid a wholesale database rewrite.
- Keep ordering, snapshot consistency, missing/expired cursor behavior, and reconnect semantics explicit.

### Acceptance criteria

- Benchmarks cover increasing execution counts and long execution histories, recording payload sizes and query/update latency.
- List responses exclude pinned packages and full event histories unless explicitly requested.
- Pagination and incremental refresh do not omit or duplicate records under concurrent updates.
- CLI, Monitor, reports, and existing full inspection retain access to complete evidence.

## CTRL-011 — Provide one pre-execution configuration and setup check

**Priority:** High — after the first reliability workstream
**Status:** Open

### Problem

Users currently encounter setup problems across configuration loading, package validation, native preflight, and actual execution. Much of the validation machinery exists, but there is no single clear inspection operation that explains whether a configured factory is ready and what is missing before creating execution state.

### Required change

- Propose one CLI check operation and a reusable application/API boundary for agent-driven use; avoid parallel validation implementations.
- Report configuration errors, unresolved paths/variables, unsupported adapters/versions/capabilities, invalid overrides, missing entrypoints/hooks/executables, and absent credential environment names.
- Separate static/offline validation from native probes and explicitly requested connectivity checks. Label checks not performed and limits of readiness claims.
- Reuse existing loader, adapter Validator, and preflight contracts. Keep runner-specific checks in adapters.
- Redact secrets. Do not implicitly call paid models, create an execution/worktree, run lifecycle hooks, install tools, or start the daemon.

### Acceptance criteria

- One invocation produces actionable per-check results and a reliable exit status, with structured output for external agents.
- Tests prove no workflow or model request is started by the default check.
- Unsupported configuration and missing prerequisites are distinguishable from transient connection failures.
- Passing static validation is not described as proof of provider availability or successful agent execution.

## CTRL-012 — Package installation independently of adjacent source checkouts

**Priority:** High — after the first reliability workstream
**Status:** Open
**Ownership:** Controls distribution and Registry integration.

### Problem and evidence

The Controls installer installs the Go binary but not adapter/SDK npm dependencies. [Registry dependencies](../registry/package.json) point to sibling Controls source directories through local file references. Setup therefore depends on checkout layout and additional manual installation, which is fragile on a new or corporate machine.

### Required change

- Define a versioned installation bundle containing the binary and compatible Controls-owned adapter/SDK assets, with explicit locations and compatibility metadata.
- Keep user agent bundles and external tools separate; expose their prerequisites through the setup check in CTRL-011.
- Remove the requirement for adjacent source repositories at runtime. Preserve source-based contributor development as a separate documented path.
- Support reproducible dependency locks and a documented offline/corporate installation path, including CA/proxy requirements where network access remains necessary.
- Define upgrade and rollback behavior without silently restarting services or modifying active execution configuration.

### Acceptance criteria

- A clean-machine test installs and runs a fixture workflow without a Controls source checkout or sibling-repository layout.
- Missing/incompatible runtime assets produce an actionable setup failure before execution.
- Packaged asset versions/digests are inspectable and credentials are never bundled.
- Installation is repeatable, paths with spaces are covered, and upgrade/rollback behavior is tested.
- Do not introduce a remote package registry, plugin ecosystem, or agent-tool installer merely to solve asset distribution.

## CTRL-013 — Establish repeatable behavioral and recovery release gates

**Priority:** High
**Status:** Open
**Ownership:** Controls verification and the separate eval repository.

### Problem

Unit and fixture integration tests establish important contracts, but they do not demonstrate reliable provider-backed workflow behavior. A successful operator-assisted eval is also not evidence of unattended recovery. Recent adapter, workspace, messaging, and lifecycle changes need repeatable evaluation with comparable evidence.

### Required change

- Use the existing unchanged Go scenario as the behavioral benchmark, with pinned factory configuration, package digests, runtime versions, and an identified baseline.
- Record completion, acceptance/quality evidence, interventions, elapsed time, tokens/usage coverage, tool failures, retries, and recovery behavior. Keep missing measurements explicit.
- Separate unattended runs from operator-assisted runs and intended human approvals from recovery interventions. Preserve contaminated/failed runs with their provenance.
- Treat scenario changes as a new scenario revision/cohort; distinguish comparable scenario results from broader factory-level aggregates.
- Add deterministic crash/failure tests from CTRL-007–CTRL-009 alongside the behavioral eval. They cover different claims and must not substitute for one another.
- Define release criteria, tolerances, repetition policy, and authorized provider spend. Model-backed gates must be explicitly triggered rather than silently adding paid calls to ordinary unit tests.

### Acceptance criteria

- A documented command/script runs the existing benchmark and produces immutable evidence plus a comparable report.
- Reports distinguish configuration changes, scenario changes, normal human decisions, recovery interventions, and incomplete usage.
- A candidate release cannot be labelled verified solely because one assisted run completed.
- Recovery gate failures identify the violated invariant and reproducible interruption boundary.
- Follow the eval repository's black-box execution rules: no agent/operator implementation work or unrecorded repairs to improve a result.

## CTRL-014 — Bind artifact writes to execution storage instead of model-generated absolute paths

**Priority:** High
**Status:** Open — recorded 2026-11-07
**Ownership:** Controls artifact/storage boundary and agent-facing contract; Registry rendering scripts and skills integrate with that contract.

### Problem

An agent can receive the correct execution-files directory yet write its required outputs elsewhere by reproducing an absolute path incorrectly in a tool call. Current completion validation catches missing files after the work, but the writing operation does not enforce the assigned destination. Correct path selection is therefore a model instruction rather than a deterministic invariant.

This caused a failed architect attempt in GO-001. The failure was not an unavailable model, a document-content rejection, or a JSON completion-schema error. The documents existed, but at an unintended sibling run path that Controls correctly refused to collect.

### Recorded execution and evidence

- Run: `GO-001-20261107T160953628967Z-8271ed`.
- Execution: `a3211129-2aaf-4799-bdbb-a0c295f0b47c`.
- Step: `specification`; stage: `design`; visit: 1; attempt: 1.
- Session: `b04ef253-38f6-43aa-b697-dc78bb5369cd`.
- Runtime-reported provider/model: OpenRouter, `deepseek/deepseek-v4-flash`.
- Assigned invocation prompt: `~/.factory/runtime/executions/a3211129-2aaf-4799-bdbb-a0c295f0b47c/d0416b43-e167-56ad-8128-2e20f270afc7/try-1/prompt.md`.
- Frozen configuration: [run project configuration](../evals/runs/GO-001-20261107T160953628967Z-8271ed/project/factory.json).
- Evidence can be inspected with `factory b04ef253-38f6-43aa-b697-dc78bb5369cd logs`. These are read operations, not instructions to retry or repair the run.

The prompt supplied the correct directory and explicit paths for both required outputs. The authoritative run component was:

```text
GO-001-20261107T160953628967Z-8271ed
```

The architect generated document-rendering commands using:

```text
GO-001-20261107T160953628968Z-8271ed
```

The final timestamp digit changed from **7** to **8**. That one-character substitution identified a different filesystem directory, not another revision of the same execution storage.

The expected execution-files root was:

```text
/Users/j/pragma/evals/runs/GO-001-20261107T160953628967Z-8271ed/project/.factory/executions/a3211129-2aaf-4799-bdbb-a0c295f0b47c/files
```

The rendered `spec.html` and `plan.html` were written under the equivalent path containing `...628968Z...`. Filesystem inspection found both documents there. The same incorrect run component also appeared in some agent-generated source-workspace commands; those commands returned missing-directory errors. No finding of filesystem isolation, a sandbox mount discrepancy, or collector path corruption was established.

### Failure sequence

1. Factory supplied the correct execution directory, required filenames, and full output paths in the initial assignment.
2. The architect produced document bodies and invoked Registry's renderer with model-generated absolute `--output` paths containing the changed digit.
3. The command sequence wrote both rendered documents outside the assigned execution-files root.
4. The architect submitted completion. Artifact validation attempted to read the configured root and returned:

   ```text
   artifact_validation_failed:
   required output "spec.html": lstat ...628967Z.../files/spec.html:
   no such file or directory
   ```

5. Controls sent two bounded corrective reminders. The tool error contained the expected path, but the attempt did not relocate or regenerate the documents at that path.
6. Completion recovery exhausted its configured reminder limit. Controls interrupted the attempt; session exit code 143 reflects termination after the rejected handoff, not the initiating defect.
7. A later attempt wrote documents under the correct run directory. That does not retroactively repair the failed attempt's evidence or establish a successful final execution.

First-attempt observations, not a controlled comparison:

| Measurement | Recorded value |
| --- | --- |
| Started, America/Chicago | 2026-11-07 10:09:57 |
| Stopped, America/Chicago | 2026-11-07 10:14:58 |
| Approximate elapsed time | 5 minutes 1 second |
| Reported input tokens | 66,985 |
| Reported output tokens | 32,320 |
| Reported cache-read tokens | 2,282,240 |
| Native tool-call failures | 11 |

These totals describe the whole attempt, not solely the path error. Reported zero/unavailable pricing must not be interpreted as zero spend. File sizes and modification times observed during investigation are evidence of writes, not an assessment of document quality.

### Root cause and limits of the finding

**Observed mechanism:** the agent's command arguments changed a digit in a supplied path. The initial assignment and collector used the correct path. There is no established explanation for why the model generated that particular substitution; do not attribute it to sampling settings, context exhaustion, provider faults, or a specific model defect without further evidence.

**Engineering weakness:** a required artifact's destination is selected by unconstrained model-generated text. The host writing path accepts that text instead of resolving a relative artifact name against a trusted, execution-bound storage capability. Detection occurs only when publication or completion tries to collect the configured files.

**Recovery weakness:** reminders ask the same agent to inspect and repair the handoff, but still rely on it to identify and correct the same path-selection mistake. More reminders or a longer timeout would not establish correct destination binding.

The renderer accepts a caller-supplied `--output` path. Its current implementation calls `writeFileSync`; it is not an execution-scoped storage API. Do not describe the renderer as creating parent directories automatically or claim that relative paths alone enforce containment.

### Relevant boundaries

- [Required-output collection](internal/artifacts/host.go): collects against `work.ArtifactDirectory()`.
- [Artifact collection and validation](internal/artifacts/collect.go): inspect the actual implementation when designing containment changes.
- [Invocation preparation and assignment prompt](internal/commandrunner/runner.go): currently supplies execution paths to agent tools.
- [Communication and completion recovery](docs/communications.md): accepted results remain contingent on verified required outputs.
- [Registry document renderer](../registry/skills/author-spec/scripts/document.mjs): owns spec/plan presentation, not workflow storage authority.
- [Execution files and variables](docs/execution-files.md): execution evidence is distinct from project source and optional script-selected worktrees.

### Required design outcome

Provide a generic, execution-scoped artifact-writing boundary so an agent identifies **what file to write**, while trusted runtime/storage code determines **where it belongs**.

A conceptual request, for interface discussion rather than an approved new tool/schema, is:

```json
{
  "path": "spec.html",
  "content": "<rendered document bytes>"
}
```

The authenticated invocation binds the destination execution. A caller must not supply an arbitrary host root or gain access to another execution by changing an execution ID in the payload. Define a bounded content/transfer representation appropriate for actual artifact sizes; do not require large files to be copied through additional model responses merely to use the boundary.

### Proposed scope for design and implementation

1. **Trusted destination binding.** Resolve relative artifact names under the invocation's assigned files root. Enforce containment in host/storage code. Cover absolute paths, parent traversal, symlink traversal, and cross-execution access; a string-prefix check is insufficient.
2. **Generic artifact interface.** Support arbitrary declared execution artifacts and safe nested paths. Keep `spec.html` and `plan.html` as workflow/Registry configuration, not special cases in Controls. Do not add separate `write_spec` or `write_plan` operations.
3. **Transport and ownership.** Decide how the boundary is exposed through the existing MCP/runtime contract or a deterministic helper backed by that authority. Keep agent-to-control-plane communication aligned with MCP and preserve runner independence. Review the protocol before fixing new tool names or message kinds.
4. **Deterministic renderer integration.** Registry's renderer should use the bound artifact destination through a supported helper/API. Separate rendering from storage, preserve standalone rendering use, and avoid duplicating storage authority inside every skill.
5. **Artifact receipts and revisions.** Return a verified relative name, byte count, digest, and revision/write identity as appropriate. Define atomic replacement and retry/deduplication semantics. Distinguish a successful write from publication, review approval, and accepted step completion.
6. **Concurrent access.** Specify ownership or expected-revision checks so one agent cannot silently overwrite a peer's accepted revision. Publication and final collection must refer to consistent bytes, not an unverified path that can change between checks.
7. **Actionable failure handling.** Distinguish missing output, invalid path, permissions, conflicting revision, and unavailable storage. Corrective feedback should identify the relative artifact and the supported writing operation; it should not require copying the full host path again.
8. **Keep final validation.** A write receipt alone must not advance a step or prove document quality. Preserve configured-output validation, publication consistency, and explicit MCP result acceptance.
9. **Document enforcement limits.** The current host runner is not sandboxed. A scoped writer can enforce its own operations, but cannot prevent an agent with unrestricted shell/filesystem tools from writing elsewhere. State that distinction and evaluate narrowly how managed Registry rendering avoids that bypass; do not claim complete filesystem confinement.

### Acceptance criteria

- An agent can produce a required artifact without constructing or reproducing an absolute execution path.
- Deterministic tests include nearly identical execution IDs and run names differing by one digit. A request cannot redirect a managed write into the other execution or create an unintended sibling run directory.
- Absolute paths, `..` escapes, symlink escapes, and cross-execution references fail before writing outside the assigned storage root.
- Valid nested filenames work; legitimate writes cannot corrupt project source, a worktree, or another invocation's artifacts.
- Failed writes leave no partially accepted artifact. Replayed write requests and concurrent edits have documented, tested behavior.
- Rendering through the managed path produces the same document presentation and metadata as standalone rendering, without model-generated destination roots.
- Tests cover missing files, permission errors, interrupted writes, storage errors, stale revisions, and publication/collection consistency.
- A rejected completion produces a targeted correction using relative artifact identity and the supported repair operation. No unrelated model work or unbounded recovery loop is introduced.
- A regression fixture reproduces this failure without paid model calls. A subsequent authorized behavioral eval records whether the managed renderer avoids the observed path-selection failure; no historical run is silently repaired or regraded.

### Non-goals and related work

- Do not move source-workspace selection into the controller or add Git-specific control-plane logic. Source selection remains separate from execution evidence storage (CTRL-004).
- Do not weaken artifact validation, search sibling directories for matching filenames, accept any `spec.html` found on disk, or automatically copy guessed files into an execution. Such behavior could accept stale or another execution's work.
- Do not fix this solely with more prompt text, longer paths repeated more often, higher retry limits, or a renamed run timestamp.
- Do not add spec/plan-specific semantics, a new workflow DSL, container runtime, or agent framework as part of this issue. Filesystem isolation remains a separate capability.
- Coordinate evidence/drill-down with CTRL-002, restart/write recovery with CTRL-008, and regression evaluation with CTRL-013.
- This issue records a proposal. It authorizes no code changes, live messages, retries, filesystem repair, or changes to recorded eval evidence.
