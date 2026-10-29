# Project configuration

`factory exec` reads `factory.json` from the current directory, or from the exact path passed with `-c`. The file is a complete workflow definition; it cannot extend another factory. Relative agent paths resolve from the configuration file's directory. That directory is also the project working directory for every step. Factory does not search parent directories. Put `factory.json` in the application project root when agents should edit that project. Factory rejects unknown fields rather than silently ignoring settings it cannot run.

```json
{
  "name": "example-coding",
  "agents": [
    {
      "name": "builder",
      "path": "./agents/builder",
      "entrypoint": "start.sh",
      "args": ["{prompt}"]
    },
    {
      "name": "reviewer",
      "path": "./agents/reviewer",
      "entrypoint": "start.sh",
      "args": ["{prompt}"]
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
              "type": "agent",
              "agent": "builder",
              "title": "Implement the request",
              "instructions": "Implement the request in the project and report the changes and checks performed.",
              "on": {
                "ready_for_review": "review",
                "failed": "$blocked"
              }
            },
            {
              "name": "review",
              "type": "agent",
              "agent": "reviewer",
              "title": "Review the implementation",
              "instructions": "Review the work against the request. Return changes_requested with specific findings or completed when it is ready for human acceptance.",
              "on": {
                "changes_requested": "build",
                "completed": "acceptance",
                "failed": "$blocked"
              }
            }
          ]
        },
        "human": {"required": true, "step": "acceptance"}
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

`type` is `agent` or `decision`. An agent step uses a named package; a decision step presents its `on` keys as allowed answers. `start`, route targets, and `human.step` refer to step names. Routes may also point to `$done` for completion or `$blocked` for failure. Each `outputs` entry names a required project-relative file. Optional `outputs_on` lists the outcomes that require those files; every entry must be a key in `on`. When omitted or empty, outputs are required for every outcome. Select all outcomes that promise files, including review findings, and omit blocker outcomes that may occur before any file is written. Outcome names have no built-in success or failure meaning. If output validation fails, the error retains the submitted outcome and summary alongside the validation cause. The local runner verifies that the file exists within the project and records its SHA-256 digest; the agent does not need to declare it again in its result. For a decision with no title, its name becomes the prompt; an optional `artifacts` list adds review targets. The TUI shows these targets and the completed steps' outputs when you answer a decision, and can open them in the default application.

`workflow.stages` is optional. A stage's `loop` contains its run step definitions and a `limit`. The first nested step is the entry for each round. An `on` route back to that step repeats the loop; the limit counts the initial round. For example, `"limit": 3` allows the builder and reviewer to run together at most three times before escalation. A satisfactory review may route onward sooner. At the limit, another back route goes to `human.step` if `human.required` is true; the decision request says the review limit was reached and the event stream records the escalation. Without a required human decision, reaching the limit fails the execution. A human `request_changes` route back to the loop entry starts a fresh bounded set of rounds. Step retries remain separate: they repeat one attempt without starting a new stage round.

`"human": {"required": true, "step": "acceptance"}` requires the named decision step before a loop step can proceed outside the stage. Declare that decision once in `workflow.steps`; it is not part of the loop. Omit `human` if approval is optional. A loop step may only route backward to the loop's first step. A stage is a policy boundary, not a separate agent or an outcome classifier: run steps produce outcomes, their `on` maps select routes, and the stage policy bounds repetition.

The accepted top-level fields are `name`, `agents`, and `workflow`. An agent has `name`, `path`, `entrypoint`, and optional `args`. The workflow has `start`, optional `stages`, and `steps` for steps outside loops. A stage has `name`, `loop`, and optional `human`; `loop` has `limit` and `steps`. A step has `name`, `type`, optional `agent`, `title`, `instructions`, `inputs`, `outputs`, `outputs_on`, `artifacts`, `actor`, and `on`. Step names must be unique across all nested and top-level steps. Factory assigns separate runtime IDs to executions and step visits; use the runtime step ID shown by `factory EXECUTION-ID` for `factory retry STEP-ID`. The execution-contract document describes future policies such as per-step automatic retry and access grants, but `factory.json` does not accept those fields yet. Manual retry is available after a blocked attempt.

An agent entrypoint starts in the project root. Its `args` are literal arguments, except `{prompt}` is replaced with the assignment. It receives `FACTORY_PROMPT_PATH`, `FACTORY_EXECUTION_ID`, `FACTORY_STEP_ID`, a scoped MCP endpoint/credential (`FACTORY_MCP_URL`, `FACTORY_MCP_TOKEN`), `FACTORY_THREAD_ID`, `FACTORY_PARTICIPANT_ID`, and `FACTORY_COMMS_SKILL`. The runtime configures these automatically; credentials must never enter prompts or logs. The prompt identifies prior results and artifacts. The agent sends a `step_result` through MCP `send_message` with a configured outcome and factual summary, and waits for `effect.state: accepted`. Factory fills actor/time and verifies applicable output files. Process exit or idle output does not mean completion. See [communication tools and payloads](../comms.md).

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
