# Project configuration

`factory exec` reads `factory.json` from the current directory, or from the exact path passed with `-c`. The file is a complete workflow definition; it cannot extend another factory. Relative package paths resolve from the configuration file's directory. That directory is the initial working directory for configured commands. A package launcher or script may select another working directory; Controls does not infer one from Git. Factory does not search parent directories. Put `factory.json` in the application project root when agents should edit that project. Factory rejects unknown fields rather than silently ignoring settings it cannot run.

```json
{
  "name": "example-coding",
  "packages": [
    {
      "name": "builder",
      "path": "./agents/builder",
      "entrypoint": "start.sh",
      "runtime": {"name":"pi","version":"=1.0.3"},
      "args": [
        "{prompt}"
      ]
    },
    {
      "name": "reviewer",
      "path": "./agents/reviewer",
      "entrypoint": "start.sh",
      "runtime": {"name":"pi","version":"=1.0.3"},
      "args": [
        "{prompt}"
      ]
    }
  ],
  "workflow": {
    "start": "build",
    "stages": [
      {
        "name": "implementation",
        "loop": {
          "limit": 3,
          "steps": [
            {
              "name": "build",
              "type": "run",
              "title": "Implement the request",
              "instructions": "Implement the request in the project and report the changes and checks performed.",
              "on": {
                "ready_for_review": "review",
                "failed": "$blocked"
              },
              "package": "builder"
            },
            {
              "name": "review",
              "type": "run",
              "title": "Review the implementation",
              "instructions": "Review the work against the request. Return changes_requested with specific findings or completed when it is ready for human acceptance.",
              "on": {
                "changes_requested": "build",
                "completed": "acceptance",
                "failed": "$blocked"
              },
              "package": "reviewer"
            }
          ]
        },
        "human": {
          "required": true,
          "step": "acceptance"
        }
      }
    ],
    "steps": [
      {
        "name": "acceptance",
        "type": "decision",
        "actor": "human",
        "title": "Accept the implementation",
        "on": {
          "approve": "$done",
          "request_changes": "build",
          "reject": "$blocked"
        }
      }
    ]
  }
}
```

`type` is `run` or `decision`. A run step uses a named `package`; a decision step presents its `on` keys as allowed answers. `start`, route targets, and `human.step` refer to step names. Routes may also point to `$done` for completion or `$blocked` for failure. Each `outputs` entry names a required execution-files-relative path. Optional `outputs_on` lists the outcomes that require those files; every entry must be a key in `on`. When omitted or empty, outputs are required for every outcome. Select all outcomes that promise files, including review findings, and omit blocker outcomes that may occur before any file is written. Outcome names have no built-in success or failure meaning. If output validation fails, the error retains the submitted outcome and summary alongside the validation cause. The local runner verifies that the file exists within the execution files directory and records its SHA-256 digest; the agent does not need to declare it again in its result. For a decision with no title, its name becomes the prompt; an optional `artifacts` list adds review targets. The separate web monitor shows these targets and completed-step outputs when reviewing a decision. It previews declared files inside the execution-files directory in a sandboxed document view.

`workflow.stages` is optional. A stage's `loop` contains its run step definitions and a `limit`. The first nested step is the entry for each round. An `on` route back to that step repeats the loop; the limit counts the initial round. For example, `"limit": 3` allows the builder and reviewer to run together at most three times before escalation. A satisfactory review may route onward sooner. At the limit, another back route goes to `human.step` if `human.required` is true; the decision request says the review limit was reached and the event stream records the escalation. Without a required human decision, reaching the limit fails the execution. A human `request_changes` route back to the loop entry starts a fresh bounded set of rounds. Step retries remain separate: they repeat one attempt without starting a new stage round.

`"human": {"required": true, "step": "acceptance"}` requires the named decision step before a loop step can proceed outside the stage. Declare that decision once in `workflow.steps`; it is not part of the loop. Omit `human` if approval is optional. A loop step may only route backward to the loop's first step. A stage is a policy boundary, not a separate agent or an outcome classifier: run steps produce outcomes, their `on` maps select routes, and the stage policy bounds repetition.

The top-level fields are `name`, `packages`, `workflow`, and optional `factory_dir`, `variables`, and `hooks`. `agents` remains accepted as registration shorthand. A package has `name`, `path`, `entrypoint`, optional `args`, `runtime`, and `overrides`. The workflow has `start`, optional `stages`, and `steps` for steps outside loops. A stage has `name`, `loop`, and optional `human` and `hooks`; `loop` has `limit`, `steps`, and optional `mode`. A step has `name`, `type`, optional `package`, `input_bindings`, `title`, `instructions`, `inputs`, `outputs`, `outputs_on`, `artifacts`, `actor`, `hooks`, `budget`, and `on`. Step names must be unique across all nested and top-level steps. Factory assigns separate runtime IDs to executions and step visits; use the runtime step ID shown by `factory EXECUTION-ID` for `factory retry STEP-ID`. The execution-contract document describes future policies such as per-step automatic retry and access grants, but `factory.json` does not accept those fields yet. Manual retry is available after a blocked attempt.

A package entrypoint starts beside factory.json. Its `args` support the explicit [execution and variable references](execution-files.md#references); `{prompt}` is separately replaced with the assignment. It receives `FACTORY_PROMPT_PATH`, `FACTORY_EXECUTION_ID`, `FACTORY_STEP_ID`, a scoped MCP endpoint/credential (`FACTORY_MCP_URL`, `FACTORY_MCP_TOKEN`), `FACTORY_THREAD_ID`, `FACTORY_PARTICIPANT_ID`, and `FACTORY_COMMS_SKILL`. The runtime configures these automatically; credentials must never enter prompts or logs. The prompt identifies prior results and artifacts. The agent sends a `step_result` through MCP `send_message` with a configured outcome and factual summary, and waits for `effect.state: accepted`. Factory fills actor/time and verifies applicable output files. Process exit or idle output does not mean completion. See [communication tools and payloads](communications.md).

The local command runner launches packages as host processes, so it cannot enforce file, network, tool, MCP, or secret grants. It rejects nonempty access profiles. The controller currently does not parse access profiles from `factory.json`, so current packages inherit the host user's permissions. Use only trusted packages until an isolated runner exists.

## Loop execution mode

`loop.mode` is `sequential` (the default) or `concurrent`. Both modes own the same `steps`, `limit`, and containing stage's human policy.

In a concurrent loop, every member starts before the engine waits for results. Active sessions exchange work and feedback through their shared thread; an idle model turn does not end an assignment. Each member has its own step visit, attempt, output, and accepted result. A round completes only after all members return results. A configured `$blocked` route wins; otherwise any route back to the loop's first step repeats the round; otherwise every exit must name the same target. Conflicting exits and routes into a non-entry member are rejected. Internal routes in concurrent mode may target only the loop entry. These are scheduling rules, not built-in outcome names.

`limit` includes the first round. Messages and individual step retries do not increment it. Reaching the limit escalates to the configured human decision, or fails when no human decision is configured. A new round starts new member invocations; it does not require restarting a process per feedback message. Required human approval remains separate from agent agreement.

Example concurrent member routes:

```json
{
  "loop": {
    "mode": "concurrent",
    "limit": 3,
    "steps": [
      {"name":"build","type":"agent","agent":"builder","on":{"ready":"acceptance","revise":"build","failed":"$blocked"}},
      {"name":"review","type":"agent","agent":"reviewer","on":{"ready":"acceptance","revise":"build","failed":"$blocked"}}
    ]
  }
}
```

This is a stage fragment; declare the agents, entry point, instructions, and `acceptance` decision in the surrounding configuration. The Go factory example stays sequential unless you explicitly adapt its routes and instructions to concurrent collaboration.

## Resource visibility and budgets

Executable steps can define an optional `budget` with token, elapsed-time, and idle-time limits and an explicit warning, pause, or stop policy. See [live resources and step budgets](resources.md) for accounting semantics, configuration, recovery, and the runner telemetry protocol.

## Lifecycle hooks

Optional `hooks.before` and `hooks.after` objects run trusted utilities at execution, stage, or step boundaries. See the [hook contract](hooks.md) for configuration, JSON stdin, ordering, failure recovery, and the Registry Git example. Hook operations are separate from agent completion and do not require agent-specific integration.

## Generic packages and structured handoff

See [packages and explicit inputs](packages.md) for `overrides`, named JSON outputs, `input_bindings`, runtime files, and the original Jev request/response handoff. Existing `type: "agent"` / `agent` references remain accepted shorthand. Decision-model evaluations use ordinary run steps; no `conditional` type or new routing token is introduced.

## Execution storage and declared variables

`factory_dir` defaults to `.factory` beside factory.json. Each execution receives its own persistent `executions/<id>/files/` directory for documents and evidence. It is separate from project source and from service-private runtime state. Root `variables` declares strings available through explicit references in package paths/arguments and lifecycle commands. Read [execution files and variable semantics](execution-files.md) for resolution, examples, security boundaries and retention.

The root example includes optional Registry Git hooks and assumes its placeholder builder/reviewer entrypoints accept `--workspace DIRECTORY`. Adapt those arguments to the actual bundle interface; remove the Git hooks and workspace argument for a non-Git factory.

## Native agent runtimes

Package/agent registrations accept optional `runtime` with `name` (adapter selection), `version` (semantic-version constraint) and `executable` (installed native program). For example: `"runtime": {"name":"pi","version":"=1.0.3"}`. Executable paths containing a separator resolve from the factory file; bare names resolve on the service PATH. The requirement is pinned; the detected version and resolved executable are recorded per launched session. Native runtime identity is separate from `runner: "command"`, the local host mechanism. Unknown adapters, incompatible versions and unsupported concurrent capabilities fail before package launch. Absent `runtime` does not infer Pi from package files. See [Adding an agent runtime](runtime-adapters.md) for supported capabilities, validation phases and integration instructions.

Use package-level `overrides` for supported runtime/package settings; unknown fields are rejected. Each runtime/package owns its schema. See [Pi override configuration](pi-overrides.md) and [generic package inputs](packages.md).
