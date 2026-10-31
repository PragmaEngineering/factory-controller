# Factory Workflows

Factory runs configured agent workflows. A workflow has runnable-package or decision steps with explicit outcomes and routes. Stages can bound review loops and require human approval before work proceeds. [Hatchet](https://docs.hatchet.run/v1/durable-execution) currently schedules durable executions; Factory's control plane records contexts, step attempts, sessions, decisions, and events for the CLI. The control plane uses a Go workflow-engine interface, and the scheduler uses a separate package-runner interface. This keeps Hatchet and agent launchers replaceable without changing Factory's policy or CLI.

To operate a configured workflow from Codex, Claude Code, or another agent, use the [Factory operator skill](skills/factory-operator/SKILL.md). See [installation and invocation](docs/agent-operator.md) for manually starting runs, following progress, messaging agents, and handling decisions through your agent session.

The CLI is the primary interface. Run `./install.sh` once from this checkout to install the CLI and terminal monitor as `factory` in a directory on your `PATH` (by default `~/.local/bin`). The installer checks `PATH` before building. Run `factory` for command help. On the first command, Factory starts its local service and an embedded Hatchet engine automatically. No Hatchet account, server setup, or client token is required for local use. First use downloads and initializes the local database before the UI opens. Startup reports its wait and supports Ctrl-C; later commands reuse the running service. Factory stores execution and session state in Factory-owned PostgreSQL tables alongside Hatchet's tables; all terminals and projects connect to one local service through `~/.factory/runtime/factory.sock`. The inherited `FACTORY_RUNTIME_DIR` variable does not select another service; contexts organize executions within the shared service. Factory does not use Hatchet Cloud. A separately deployed, self-hosted Hatchet server remains an operator-managed option; see the [engine guide](docs/hatchet-engine.md).

`./install.sh` builds and installs only the Go binary. Managed Pi agents and Node.js classifiers also require the [adapter/SDK dependencies](adapters/README.md#setup) and [Registry package setup](../registry/README.md#controls-dependencies). Installing the binary does not install npm dependencies, Pi, or agent tools.

From a project directory containing `factory.json`:

For a complete first run with the Go agents and both human approvals, follow the [short tutorial](tutorial.md).

Start from [factory.example.json](factory.example.json) if you need a small coding workflow. Copy it to the project as `factory.json` and set the two agent paths and entrypoints to your installed packages.

```sh
factory exec "Add a health endpoint"
factory exec "Implement the attached proposal" -f proposal.md -f acceptance.txt
factory exec -f request.md                  # a file alone is enough
cat request.md | factory exec -f -           # stdin
factory exec "Another task" -c /path/to/project/factory.json

factory ls
factory monitor                             # live terminal view of executions and agents
factory report EXECUTION-ID > report.html    # execution review + last 90 days of factory history
factory get-context
factory set-context CONTEXT-ID               # * means all contexts
factory inbox
factory EXECUTION-ID                          # full status and step IDs
factory SESSION-ID logs -f
factory EXECUTION-ID stream -f             # workflow, agents, and operator activity
factory EXECUTION-ID attach                # attach to the active agent; step/session ID also works
factory SESSION-ID messages
factory SESSION-ID send "Please inspect the failing check"
factory REQUEST-ID                            # prompt and allowed outcomes
factory answer REQUEST-ID approve --reason "Reviewed"
factory retry STEP-ID --reason "Provider recovered"
factory cancel EXECUTION-ID
```

`factory exec` reads **only** `./factory.json` unless `-c/--config` names another file. The file's directory is the project working directory, including when `-c` is used. Each invocation creates a unique execution. When the selected context is `*`, it also creates a new short context ID; selecting a context groups later executions under it. `--context ID` overrides the selection for one `exec`, `ls`, or `inbox` command. `factory ls` shows execution, context, current step, last activity, and any required action. `factory EXECUTION-ID` shows the pinned workflow, stage labels, pending and visited steps, attempts, and errors. `factory ID -o json` and `factory ls -o json` expose the records to other tools. `stream`, `logs`, and `events` accept `-f/--follow`.

`factory monitor` has three pages. **Home (`1`)** shows executions, the full-height workflow, and the operator inbox. **Messages (`2`)** shows a traffic display above an inbox index (operator, then each configured agent), the selected inbox, and an inline message prompt. Use ↑/↓ in the index to change recipient, `m` or Enter to focus the prompt, and Enter to send; Ctrl-J inserts a newline. Messages are addressed to that recipient's running session; delivery does not prove execution. Drafts are retained separately per execution/recipient and on send failure. Operator requests are answered through the inbox, rather than sent to an agent. **Logs (`3`)** presents execution activity as readable tool, result, response, and session entries. Arrows select entries; Enter opens the full content. `v` switches between readable entries and retained raw output; unknown runner records remain visible. Expanded messages and assistant text render Markdown, with `v` available to inspect original content. Traffic lanes show observed senders, recipients, counts, and relative delivery volume within the retained inbox window. A bright packet marks traffic from the last five seconds; the display does not invent activity. `n` starts an execution; `a` opens actions; `i` focuses the inbox. Enter reads a full message or answers a decision. Inside an item dialog, listed files support `o` to open and `c` to copy their locations. In the main view, `c` selects a context. Decisions require confirmation before submission.

Rendered previews using fictional fixture data: [Home](docs/images/monitor-home.png), [Messages](docs/images/monitor-messages.png), and [Logs](docs/images/monitor-logs.png).

The workflow groups connected step cards inside stage frames, labels return routes, and distinguishes decision steps with double borders. Decision labels use the configured authority; they do not assume an approval process. It shows elapsed time, the current step, and selected-step details. Small terminals collapse other stages and keep the selection visible. Status colors remain visible when selected: green completed, cyan running, amber awaiting input, red failed, and muted pending. Green **LIVE** means the last service refresh succeeded; it does not mean an agent is running.

Tab cycles the current page's visible panes; Shift-Tab cycles backward. `z` expands a focused pane; `z` or Esc restores the layout. Arrows operate within that pane. While typing, ordinary keys are message text; Ctrl-J adds a newline; Esc leaves the prompt and Tab changes focus. `g` opens the participant index on Messages. On Logs, `l` cycles between all activity, selected agent output, and workflow events; `s` selects an agent session and opens its logs. `f` follows the current step and new output. Press `?` for help or `q` outside text input to leave executions running. The [tutorial](tutorial.md) walks through a complete run.

`stream` reads a durable, ordered execution activity log and prints each line as `{source}: {data}`. It includes workflow transitions, captured agent output, operator messages, and session state. Use `factory EXECUTION-ID attach` to follow the active agent while sending a line to it; a step or session ID can also select the agent. Attach is supported when the agent package accepts terminal input; the bundled Pi agents use an RPC adapter that turns those lines into live steering prompts and emits Pi's native event records. A message written to a different runner's PTY does not prove that runner read it. Existing sessions launched before the adapter cannot gain their missing internal events retroactively.

Input documents are copied into the execution's private runtime directory and hashed, so later source edits do not change what the execution received. The runner gives an agent the document paths, accepted prior step results, and their artifact locations, including reviewer findings. A package completes a run step only through an accepted MCP `step_result` message; process exit, an idle model turn, or prose cannot advance it. Agent packages in `factory.json` must define an entrypoint and, when needed, `args` that pass the generated prompt to that entrypoint. For example, a Pi wrapper can use `"args": ["-p", "{prompt}"]`. The registry agents use the [thread MCP runtime](adapters/README.md), with a scoped connection and shared communication skill provisioned automatically. Use the [Registry dependency setup](../registry/README.md#controls-dependencies) before launching these packages.

The local command runner is an integration slice, **not a sandbox**. It rejects requested access grants that it cannot enforce, but a host process launched without grants still has the permissions of the user running Factory. Do not run untrusted packages with it. Package bytes are checked against the digest recorded at execution start; changing an agent package during an execution blocks its next invocation.

Read the [factory.json format](docs/factory-json.md), [execution contract](docs/execution-contract.md), [Hatchet integration and database migrations](docs/hatchet-engine.md), and [session supervisor](docs/session-supervisor.md) for design and current limits. The [sandbox design note](docs/sandbox.md) evaluates a possible future agent runner. The [communication proposal](comms.md) defines thread-based MCP tools and A2A content reuse for agent communication. New evals and their evidence belong under the workspace's [evals directory](../evals/README.md). Run `go test ./...` and `go vet ./...` to check the implementation.

Monitor visual-review fixtures can be rendered without a service or provider:

```sh
FACTORY_MONITOR_PREVIEW=/tmp/factory-monitor-preview go test ./internal/dashboard -run TestMonitorPreview -count=1
```

The files contain ANSI output from the actual views with fictional fixture data. The monitor uses Bubble Tea for interaction, Lip Gloss for shared surfaces and status styles, Bubbles for input/scrolling/activity, and Glamour for expanded Markdown. Its data and workflow semantics remain in the control plane.

[Live resource metrics and step budgets](docs/resources.md) cover per-attempt limits, usage accounting, and runtime adapter integration.

[Lifecycle hooks](docs/hooks.md) inject deterministic utilities before and after executions, stages, and steps; the example includes optional Registry Git branch/commit hooks.

[Execution reports](docs/reports.md) separates persisted measurements from attributed assessments, with CLI and operator MCP access.

[Packages and explicit inputs](docs/packages.md) covers agents, scripts, classification bundles, package settings and named JSON handoffs. [Go coding factory](../registry/factories/golang/README.md) passes an original Jev request/response to an architect agent.

## Runner integrations and SDKs

[Adapters](adapters/README.md) connect native agent runners to Controls. The [Pi adapter](adapters/pi/README.md) uses the [TypeScript SDK](sdk/typescript/README.md), which also supports non-agent packages such as classifiers. The controller remains runner independent. Install their workspace dependencies with `npm ci` from this checkout; Registry declares local dependencies on these packages.
