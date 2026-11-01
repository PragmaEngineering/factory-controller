# Run a Go workflow

This tutorial runs a Hello World task through a Go workflow: architecture, architecture review, your design approval, implementation, code review, and your final acceptance. Each agent pair can repeat its work before the human decision.

## 1. Set up Factory

Start in the `controls` checkout, with `registry` beside it. Before setup, have Go, Node.js 22+, Pi, and `rg` installed. The Go coder and reviewer also need `gofmt`, `golangci-lint`, and `gopls`; see [agent prerequisites](../registry/agents/README.md#local-tool-preflight). The destination `~/.local/bin` must already be on `PATH`.

Install the CLI binary and the separate managed-runtime dependencies:

```sh
./install.sh
npm ci
npm ci --prefix ../registry
```

`./install.sh` builds and installs only the Go binary as `factory`. Pass another directory on `PATH` to the installer if needed. The two `npm ci` commands install Controls’ adapter/SDK workspace first, then Registry’s local package dependencies. They do not install Pi, Go tools, or provider credentials.

## 2. Create the project and its workflow

Leave the `controls` checkout and create a new project beside it:

```sh
cd ..
mkdir hello-go
cd hello-go
```

Run Factory from this project root. Commands initially start beside `factory.json`; source work stays in the launcher-selected workspace. Workflow documents and review evidence go to `.factory/executions/<execution-id>/files/`. When using Git, add `.factory/` to `.gitignore`.

Create `factory.json` in `hello-go` with this content. The four `path` values assume `registry` is beside the project. If it lives elsewhere, change those values to the actual agent directories; absolute paths work too. `entrypoint` and `args` tell Factory how to start each agent.

```json
{
  "name": "golang",
  "factory_dir": ".factory",
  "agents": [
    {"name": "architect", "path": "../registry/agents/architect", "entrypoint": "start.sh", "args": ["-p", "{prompt}"]},
    {"name": "architecture_reviewer", "path": "../registry/agents/architecture_reviewer", "entrypoint": "start.sh", "args": ["-p", "{prompt}"]},
    {"name": "coder_golang", "path": "../registry/agents/coder_golang", "entrypoint": "start.sh", "args": ["-p", "{prompt}"]},
    {"name": "code_reviewer_golang", "path": "../registry/agents/code_reviewer_golang", "entrypoint": "start.sh", "args": ["-p", "{prompt}"]}
  ],
  "workflow": {
    "start": "specification",
    "stages": [
      {
        "name": "design",
        "loop": {
          "mode": "sequential",
          "limit": 3,
          "steps": [
            {
              "name": "specification", "type": "agent", "agent": "architect",
              "instructions": "Inspect the project and brief. Write spec.html and plan.html. Address prior architecture-review findings when present.",
              "inputs": ["task brief", "project source"],
              "outputs": ["spec.html", "plan.html"],
              "outputs_on": ["completed", "ready_for_review"],
              "on": {"completed": "architecture_review", "ready_for_review": "architecture_review", "failed": "$blocked"}
            },
            {
              "name": "architecture_review", "type": "agent", "agent": "architecture_reviewer",
              "instructions": "Critically review spec.html and plan.html against the brief and project. Write findings and verdict to reviews/architecture.md. Return changes_requested for material problems; otherwise completed.",
              "inputs": ["task brief", "spec.html", "plan.html", "project source"],
              "outputs": ["reviews/architecture.md"],
              "outputs_on": ["completed", "changes_requested"],
              "on": {"completed": "design_approval", "changes_requested": "specification", "failed": "$blocked"}
            }
          ]
        },
        "human": {"required": true, "step": "design_approval"}
      },
      {
        "name": "implementation",
        "loop": {
          "mode": "sequential",
          "limit": 3,
          "steps": [
            {
              "name": "build", "type": "agent", "agent": "coder_golang",
              "instructions": "Implement the approved spec and plan in the project root. Run relevant checks. Write commands, results, changed files, and limitations to reviews/build.md. Address prior code-review findings when present.",
              "inputs": ["approved spec.html", "approved plan.html", "project source"],
              "outputs": ["reviews/build.md"],
              "outputs_on": ["completed", "ready_for_review"],
              "on": {"completed": "code_review", "ready_for_review": "code_review", "failed": "$blocked"}
            },
            {
              "name": "code_review", "type": "agent", "agent": "code_reviewer_golang",
              "instructions": "Independently review the implementation against the approved spec and plan. Write findings and verification evidence to reviews/code.md. Return changes_requested for material problems; otherwise completed.",
              "inputs": ["approved spec.html", "approved plan.html", "implementation changes"],
              "outputs": ["reviews/code.md"],
              "outputs_on": ["completed", "changes_requested"],
              "on": {"completed": "acceptance", "changes_requested": "build", "failed": "$blocked"}
            }
          ]
        },
        "human": {"required": true, "step": "acceptance"}
      }
    ],
    "steps": [
      {"name": "design_approval", "type": "decision", "actor": "human", "artifacts": ["spec.html", "plan.html"], "on": {"approve": "build", "request_changes": "specification", "reject": "$blocked"}},
      {"name": "acceptance", "type": "decision", "actor": "human", "on": {"approve": "$done", "request_changes": "build", "reject": "$blocked"}}
    ]
  }
}
```

The `agents` list selects packages; `workflow` defines order, routes, review-loop limits, and human decisions. Edit those fields to customize this project. The complete format is in the [factory.json reference](docs/factory-json.md).

The bundled agents' native Pi profiles use OpenRouter by default. Set `OPENROUTER_API_KEY` in the environment **before starting Factory**; the local service and agents inherit it. Keep credentials out of `factory.json`. For a different provider or model, edit each agent package's native Pi configuration (`pi/models.json` and `pi/settings.json`) and supply that provider's credentials. `factory.json` selects packages; it does not override their native model settings. The local runner executes trusted agent packages with your user permissions.

## 3. Run and watch the execution

Start the execution service and submit the request from `hello-go`:

```sh
factory exec "Create a minimal Go program that prints Hello, world!"
factory ls
```

Install the separate [Pragma Monitor](../monitor/README.md) once from its checkout:

```sh
npm ci
npm run build
npm link
```

Then, from any directory:

```sh
pragma-monitor
```

Open the printed URL. Select the execution in the sidebar. **Workflow** shows the pinned stages, routes, step status and elapsed time. Select a step for visits, attempts and errors. **Messages** lets you inspect conversations and send feedback to a selected running agent session. **Decisions** presents the configured operator requests and evidence documents; choose an allowed outcome and confirm the response.

The connection indicator reports service refresh health, not agent activity. Closing the monitor leaves executions running. Logs are available through the CLI:

```sh
factory EXECUTION-ID stream -f
factory SESSION-ID logs -f
```

## 4. Complete both decisions

At design review, inspect `spec.html` and `plan.html` in **Decisions**, then approve or request changes. After implementation and code review, inspect the verification evidence before final acceptance. The example routes requested changes back to the responsible agent; review-loop limits remain enforced by Controls.

The same lifecycle is available entirely through commands:

```sh
factory inbox
factory REQUEST-ID
factory answer REQUEST-ID approve --reason "Reviewed the evidence"
factory SESSION-ID send "Please investigate the failing case"
factory retry STEP-ID --reason "Underlying issue resolved"
factory cancel EXECUTION-ID
```

Use the exact options shown on each request. After final approval, inspect the execution for `completed` and generate its report:

```sh
factory EXECUTION-ID
factory report EXECUTION-ID > report.html
```

Factory provisions the scoped MCP connection and communication skill. Agents signal completion through accepted result messages; process exit or model prose alone does not advance a step. See the [communication contract](docs/communications.md).
