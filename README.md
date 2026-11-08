# Factory Workflows

Factory runs configured agent workflows. A workflow has runnable-package or decision steps with explicit outcomes and routes. Stages can bound review loops and require human approval before work proceeds. [Hatchet](https://docs.hatchet.run/v1/durable-execution) currently schedules durable executions; Factory's control plane records contexts, step attempts, sessions, decisions, and events for the CLI. The control plane uses a Go workflow-engine interface, and the scheduler uses a separate package-runner interface. This keeps Hatchet and agent launchers replaceable without changing Factory's policy or CLI.

To operate a configured workflow from Codex, Claude Code, or another agent, use the [Factory operator skill](skills/factory-operator/SKILL.md). See [installation and invocation](docs/agent-operator.md) for manually starting runs, following progress, messaging agents, and handling decisions through your agent session.

The CLI is the primary interface. Run `./install.sh` once from this checkout to install the CLI as `factory` in a directory on your `PATH` (by default `~/.local/bin`). The installer checks `PATH` before building. Run `factory` for command help. Start the local service explicitly with `factory start`; stop it with `factory stop`. Both commands work from any directory and never read `factory.yaml`; that file is used when submitting an execution. Operational commands such as `ls`, `exec`, and `inbox` only connect and fail quickly if the service is unavailable. Local startup manages the embedded engine and database without a separate account or client token. The first `start` may download and initialize the database; later starts reuse its data. Startup reports its wait and supports Ctrl-C. Factory stores execution and session state in Factory-owned PostgreSQL tables alongside Hatchet's tables; all terminals and projects connect to one local service through `~/.factory/runtime/factory.sock`. The inherited `FACTORY_RUNTIME_DIR` variable does not select another service; contexts organize executions within the shared service. Factory does not use Hatchet Cloud. A separately deployed, self-hosted Hatchet server remains an operator-managed option; see the [engine guide](docs/hatchet-engine.md).

`./install.sh` builds and installs only the Go binary. Managed Pi agents and Node.js classifiers also require the [adapter/SDK dependencies](adapters/README.md#setup) and [Registry package setup](../registry/README.md#controls-dependencies). Installing the binary does not install npm dependencies, Pi, or agent tools.

From a project directory containing `factory.yaml`:

For a complete first run with the Go agents and both human approvals, follow the [short tutorial](tutorial.md).

Start from [factory.example.yaml](factory.example.yaml) if you need a small coding workflow. Copy it to the project as `factory.yaml` and set the two agent paths and entrypoints to your installed packages.

```sh
factory start
factory exec "Add a health endpoint"
factory exec "Implement the attached proposal" -f proposal.md -f acceptance.txt
factory exec -f request.md                  # a file alone is enough
cat request.md | factory exec -f -           # stdin
factory exec "Another task" -c /path/to/project/factory.yaml

factory ls
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

`factory exec` reads **only** `./factory.yaml` unless `-c/--config` names another file. The file's directory is the project working directory, including when `-c` is used. Each invocation creates a unique execution. When the selected context is `*`, it also creates a new short context ID; selecting a context groups later executions under it. `--context ID` overrides the selection for one `exec`, `ls`, or `inbox` command. `factory ls` shows execution, context, current step, last activity, and any required action. `factory EXECUTION-ID` shows the pinned workflow, stage labels, pending and visited steps, attempts, and errors. `factory ID -o json` and `factory ls -o json` expose the records to other tools. `stream`, `logs`, and `events` accept `-f/--follow`.

The interactive UI lives in the separate [Pragma Monitor repository](../monitor/README.md). It is a Svelte/TypeScript web application for workflow graphs, execution status, messages and operator decisions. Run `factory monitor start` from any directory after installing Monitor (`pragma-monitor` remains available directly); the server prints its local browser URL. Use `factory monitor start -d` to detach; it prints the URL, PID, and private log path. `factory monitor stop` stops all Factory-managed detached monitors without affecting the execution service. `factory stop --all` also stops managed detached monitors; `factory stop` only stops the execution service. Bare `factory monitor` prints command help. Controls contains no monitor UI and retains the complete command-line lifecycle, logs and event stream.

`stream` reads a durable, ordered execution activity log and prints each line as `{source}: {data}`. It includes workflow transitions, captured agent output, operator messages, and session state. Use `factory EXECUTION-ID attach` to follow the active agent while sending a line to it; a step or session ID can also select the agent. Attach is supported when the agent package accepts terminal input; the bundled Pi agents use an RPC adapter that turns those lines into live steering prompts and emits Pi's native event records. A message written to a different runner's PTY does not prove that runner read it. Existing sessions launched before the adapter cannot gain their missing internal events retroactively.

Input documents are copied into the execution's private runtime directory and hashed, so later source edits do not change what the execution received. The runner gives an agent the document paths, accepted prior step results, and their artifact locations, including reviewer findings. A package completes a run step only through an accepted MCP `step_result` message; process exit, an idle model turn, or prose cannot advance it. Agent packages in `factory.yaml` must define an entrypoint and, when needed, `args` that pass the generated prompt to that entrypoint. For example, a Pi wrapper can use `"args": ["-p", "{prompt}"]`. The registry agents use the [thread MCP runtime](adapters/README.md), with a scoped connection and shared communication skill provisioned automatically. Use the [Registry dependency setup](../registry/README.md#controls-dependencies) before launching these packages.

The local command runner is an integration slice, **not a sandbox**. It rejects requested access grants that it cannot enforce, but a host process launched without grants still has the permissions of the user running Factory. Do not run untrusted packages with it. Package bytes are checked against the digest recorded at execution start; changing an agent package during an execution blocks its next invocation.

Read the [factory.yaml format](docs/factory-yaml.md), [execution contract](docs/execution-contract.md), [Hatchet integration and database migrations](docs/hatchet-engine.md), and [session supervisor](docs/session-supervisor.md) for design and current limits. The [sandbox design note](docs/sandbox.md) evaluates a possible future agent runner. The [communication contract and detailed diagram](docs/communications.md) describe MCP schemas, completion, delivery, and bounded recovery; [comms.md](comms.md) retains the broader design rationale. New evals and their evidence belong under the workspace's [evals directory](../evals/README.md). Run `go test ./...` and `go vet ./...` to check the implementation.

[Live resource metrics and step budgets](docs/resources.md) cover per-attempt limits, usage accounting, and runtime adapter integration.

[Lifecycle hooks](docs/hooks.md) inject deterministic utilities before and after executions, stages, and steps; the example includes optional Registry Git branch/commit hooks.

[Execution reports](docs/reports.md) separates persisted measurements from attributed assessments, with CLI and operator MCP access.

[Packages and explicit inputs](docs/packages.md) covers agents, scripts, classification bundles, package settings and named JSON handoffs. [Go coding factory](../registry/factories/golang/README.md) passes an original Jev request/response to an architect agent.

## Runner integrations and SDKs

[Adapters](adapters/README.md) connect native agent runners to Controls. The [Pi adapter](adapters/pi/README.md) uses the [TypeScript SDK](sdk/typescript/README.md), which also supports non-agent packages such as classifiers. The controller remains runner independent. Install their workspace dependencies with `npm ci` from this checkout; Registry declares local dependencies on these packages.

Execution documents are separate from project source. Configure `factory_dir` (default `.factory`) and use explicit frozen references to pass execution paths to commands. See [execution files and variables](docs/execution-files.md).

To integrate another native agent runner, see [Adding an agent runtime](docs/runtime-adapters.md). It defines preparation ownership, explicit runner selection, capabilities, compatibility and the non-Pi conformance test. Adding a role for an existing runner needs no new adapter.

For per-agent provider, model, endpoint and credential selection, see [Pi package overrides](docs/pi-overrides.md).

`factory stop` shuts down the shared service and its local agent sessions, retaining execution history and files. It affects every project using that service; use `factory cancel EXECUTION-ID` to cancel only one execution. Interrupted agent sessions are not reattached after restart; inspect execution state before retrying. `start` and `stop` are idempotent, and neither submits or cancels a workflow.

For recovery after an interrupted daemon, use `factory stop --all`; it also stops verified managed dependency leftovers without deleting data. See [local resource recovery](docs/local-resources.md).

Use `factory prune --dry-run` to inspect disposable cleanup, or `factory prune --all --dry-run` to include retained execution and database data. Stop resources first. Destructive `factory prune --all` requires confirmation; `--yes` supports automation. Source workspaces and worktrees are excluded.
