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

The launchers already changed into the configured source workspace. The generic assignment nevertheless prominently included the original `factory.json` directory, and native injection did not state its actual working directory. That conflicting context is a confirmed contributing defect; code inspection alone cannot establish why the earlier model chose each `cd`.

- Removed the configuration-directory path from the generic source instructions. The native Pi bridge now identifies its actual process working directory after script-owned selection as authoritative for source reads, edits and checks.
- Clarified Registry role instructions and the Go factory's build instruction. Execution-file paths and host artifact collection remain separate and unchanged; Controls gains no Git logic or script-specific workspace discovery.
- Regression tests exercise a script-selected plain directory through real managed MCP completion/artifact collection, native Pi input with distinct source/evidence locations, and the actual Registry coder/reviewer launchers followed by the configured commit hook in a private Git fixture. They check that the original checkout receives no product edits.
- This is path/context correctness, not filesystem enforcement. An unconstrained host agent can still leave its working directory; sandboxing is separate.
- A fresh live-agent eval is still required to assess model adherence. No live execution or historical eval was repaired, restarted, regraded or resubmitted. Prior directory-mismatched evidence remains unsuitable for a clean comparison until its source provenance is established.


## CTRL-005 — Version-aware runner adapters must apply per-agent factory overrides

**Priority:** High
**Status:** Open — CTRL-006 prerequisite implemented
**Prerequisite:** [CTRL-006](#ctrl-006--formalize-the-agent-runtime-adapter-boundary) owns the generic runtime boundary, preparation contract and conformance fixture. This issue implements Pi-specific override/version support against that contract.
**Ownership:** Controls owns runner-specific translation and compatibility; Registry owns agent bundles and native defaults.

### Problem

A team must be able to reuse a Registry agent while selecting its own provider, model, API endpoint, credential environment variable and supported model options in `factory.json`. Editing each downloaded agent bundle is fragile, duplicates configuration and makes updates difficult. Global provider defaults are insufficient: the architect, builder and reviewer may deliberately use different providers, models and credentials in the same execution.

Passing arbitrary JSON to a package does not implement this behavior. Someone must know how a particular runner stores configuration, selects its model, resolves credentials and launches a session. For Pi, that knowledge includes `settings.json`, `models.json`, command-line options and any relevant runner-version differences. It must not be independently reimplemented in every Registry role's launcher or left for the model to interpret.

### Current implementation and gap

- [Package configuration](docs/packages.md) already supports a per-registration `with` object. Controls pins those values and passes them through the private `FACTORY_PACKAGE_CONFIG` file. It does not currently merge them into native runner configuration or expand credential references.
- [Factory configuration loading](internal/factoryconfig/load.go) treats packages generically. The `agents` form is accepted shorthand for the same registration mechanism.
- [Runtime adapters](adapters/README.md) already belong to Controls. The [Pi integration](adapters/pi/pi-rpc-session.mjs) handles native RPC sessions, communication and telemetry.
- Registry agents contain their own Pi defaults and launchers. For example, [coder_golang](../registry/agents/coder_golang/start.sh) copies its bundled Pi profile into a private directory for a standalone invocation and invokes the managed adapter when Factory supplies communication settings. That does not establish a validated provider/model override contract.
- There is currently no Go configuration-preparation package under `adapters/pi` establishing the version-aware resolution proposed here.

**Do not document factory-level Pi overrides as working until the launcher and adapter path demonstrably applies them.**

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

### Proposed factory configuration

Use the existing package-level `with` mechanism for overrides rather than introducing an unrelated root-level provider defaults block. Example of the intended **proposed behavior**, not a currently implemented configuration contract:

```json
{
  "packages": [
    {
      "name": "builder",
      "path": "../registry/agents/coder_golang",
      "entrypoint": "start.sh",
      "with": {
        "provider": "corporate-gateway",
        "model": "coding-model",
        "base_url": "https://ai.example.com/v1",
        "api": "openai-completions",
        "api_key_env": "CORPORATE_AI_KEY",
        "thinking": "medium"
      }
    },
    {
      "name": "reviewer",
      "path": "../registry/agents/code_reviewer_golang",
      "entrypoint": "start.sh",
      "with": {
        "provider": "review-provider",
        "model": "review-model",
        "api_key_env": "REVIEW_AI_KEY"
      }
    }
  ]
}
```

`api` identifies the native API protocol, not the provider's display name. The example protocol spelling must be checked against the supported Pi version before implementation. Provider endpoint and credential references must be resolved coherently with the selected model's native definition.

**Adapter selection is defined by CTRL-006 and must be explicit and documented.** Do not infer Pi from package names, roles, file names or the existence of `models.json`. CTRL-006 must settle where the package declares its runner and compatibility requirements and how the launcher requests adapter preparation. A package-level runtime descriptor or an explicit package entrypoint-to-adapter contract are possible approaches; no additional JSON keys are approved by this example. Factory overrides must not silently change the agent's runner.

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

Packages may use `with` for other purposes. Strict validation belongs to the selected package/adapter schema; a generic package must not be forced into a Pi or LLM schema. Separate adapter-owned settings from any package-specific settings explicitly if both share the object.

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
6. Update [package configuration](docs/packages.md), [factory configuration](docs/factory-json.md), [runtime adapters](adapters/README.md), [Pi adapter](adapters/pi/README.md), [factory.example.json](factory.example.json), and relevant Registry agent setup/launch documentation. Clearly distinguish working support from future runners.

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
