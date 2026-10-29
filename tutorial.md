# Run a Go workflow

This tutorial runs a Hello World task through a Go workflow: architecture, architecture review, your design approval, implementation, code review, and your final acceptance. Each agent pair can repeat its work before the human decision.

## 1. Set up Factory

Start in the `controls` checkout. This tutorial assumes the `registry` checkout is beside it. Install the CLI and terminal monitor as one binary:

```sh
./install.sh
```

This installs `factory` in `~/.local/bin`, which must already be on your `PATH`. You can pass another `PATH` directory to the installer if you use one. You need Go to build it. The bundled agents also need Node.js and Pi; the Go coder and reviewer use `gopls` through MCP. Install those tools before running a workflow.

## 2. Create the project and its workflow

Leave the `controls` checkout and create a new project beside it:

```sh
cd ..
mkdir hello-go
cd hello-go
```

Run Factory from this project root. The directory containing `factory.json` is the agents' working directory, and their output files will appear here.

Create `factory.json` in `hello-go` with this content. The four `path` values assume `registry` is beside the project. If it lives elsewhere, change those values to the actual agent directories; absolute paths work too. `entrypoint` and `args` tell Factory how to start each agent.

```json
{
  "name": "golang",
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

```sh
factory monitor
```

The first command starts one local Factory service for your user. All projects and terminals share it; the monitor can be opened from any directory. Your working directory only supplies the default `factory.json` for a new execution. On first use the service downloads and initializes its database before opening the monitor. Startup prints progress; Ctrl-C cancels the wait. Later commands reuse the running service.

Press `n` for a new execution. Enter this brief:

> Create a minimal Go Hello World command. It prints Hello, world! and exits. Add a small test.

Use ↑/↓ to move between **Brief**, **Input files**, **Config**, and **Start execution**. Left/right move the text cursor. Check that **Config** points to this project's `factory.json`, select **Start execution**, and press Enter. For a larger request, put it in a file and enter its path in **Input files** instead. Separate multiple paths with commas. Factory copies those files into the execution's private input area. If submission fails, the full error opens in a scrollable dialog; Enter or Esc returns to your filled-in form.

The main menu includes **1 Home**, **2 Messages**, and **3 Logs**. Home shows executions, the workflow in the full-height center column, and operator requests/messages. Select an execution before changing pages; it stays selected.

Messages shows recorded traffic above an inbox index. The index lists Operator, then every configured agent. Use ↑/↓ to select whose incoming messages to see. The selected agent's inbox and message prompt occupy the rest of the page. Press `m` or Enter in the index to type, then Enter to send. Ctrl-J inserts a newline. Esc leaves typing; Tab changes pane. Letters, spaces, and page-number keys are ordinary message text while the prompt is focused. The prompt retains a separate draft for each execution and agent; errors retain the draft and show the full cause. An agent needs a running session to receive messages. Operator questions/decisions are answered from their inbox with Enter.

Tab cycles the visible panes; Shift-Tab cycles backward. `z` expands the focused pane; `z` or Esc restores the layout. Tab still cycles while expanded. Arrows operate within the focused pane: select executions, steps, agents, or messages, and scroll logs. In a text field, left/right moves the cursor.

Workflow cards sit inside stage frames. Arrows follow configured routes, and labeled return routes show review loops. Decision steps use double borders and display their configured authority; this example uses human decisions. Green means completed, cyan running, amber awaiting input, red failed, and muted pending. The selection marker does not replace the status color. Elapsed time includes approval waits and stops when the execution ends. Smaller terminals show a compact view around the selected step.

On Logs (`3`), arrows select readable entries and Enter opens their full content. Press `v` to switch to retained raw output; `v` inside an expanded message or entry shows original content. Markdown is styled in expanded views. Press `l` to cycle between all activity, selected agent output, and workflow events. `s` selects an agent session and opens its output; `[`/`]` switches sessions for the selected step. Press `f` to follow the current step and new output, or `r` to refresh. On Home, Enter inspects a workflow step's full state, attempt, and error. Green **LIVE** means the service refresh succeeded, not that an agent is busy. Traffic lanes highlight a packet only when recorded traffic occurred in the last five seconds. Press `?` for help; `q` outside the prompt leaves executions running.

In the main view, press `c` to select a context. Inside an item dialog with listed files, `c` instead copies their locations. `*` shows all contexts; a named context filters the execution list and groups new executions under it. If you start this run with `*` selected, Factory creates a new context for it. The context ID is shown alongside the execution, so you can return to it later.

The CLI provides the same operations when you need commands or automation. For example:

```sh
factory ls
factory EXECUTION-ID
factory EXECUTION-ID stream -f
```

Replace `EXECUTION-ID` with the ID shown in the TUI. Stop following the stream with Ctrl-C; the execution continues.

## 4. Review and answer the two human decisions

When design approval appears, press `i` to focus the right-hand inbox and Enter to inspect the request. Press `o` to open its review files in your default application, or `c` to copy their absolute locations. Read `spec.html`, `plan.html`, and `reviews/architecture.md`; then choose an outcome with the arrow keys. Press Enter to move to **Reason**, enter feedback if needed, press Enter, and press `y` to confirm. Press Esc to leave a dialog without answering.

Choose `request_changes` with a specific reason if the design needs work; Factory routes back to the architect. `reject` blocks the execution. The TUI requires confirmation before it sends the answer.

After implementation and code review, inspect the code plus `reviews/build.md` and `reviews/code.md`. Press `i` again for the acceptance request and answer `approve`, `request_changes`, or `reject`. The coder and reviewer can also loop before this decision when the reviewer requests changes. Human `request_changes` sends the work back for another bounded set of rounds.

## 5. Intervene or recover when needed

To see what an agent is doing, press `3` for Logs, then `s` to select its session. To send feedback, press `2` for Messages, select the agent with ↑/↓ in the index, and press `m` to type. Enter queues the message to that agent's session; delivery alone does not prove it read or followed the message. `i` focuses the inbox; Enter reads a full message. To send the contents of a file, use **Actions → Message agent** or the CLI.

Press `a` for actions available on the selected execution. You can inspect a step, answer a pending decision, message or stop a running agent, or cancel the execution. Stopping an agent may block its step. Cancellation stops the execution's active work. Both actions ask for confirmation.

If an execution is `blocked`, inspect the selected step's error and output first. Fix the cause, then press `a` and choose **Retry blocked step**. Enter what changed and confirm. Retry continues from that blocked step; it does not create a new execution. A reviewer finding is handled through the configured agent loop or human decision, rather than by manually retrying a healthy step.

## 6. Check the result

After final approval, the execution should show `completed`. Select it and inspect the workflow and activity stream for the actual step sequence and outcomes. You can leave the TUI with `q` at any point and reopen it with `factory monitor`.

Use this new project as a disposable first run.

### Runtime communication

Before the first managed agent launch, install the registry adapter dependency:

```sh
npm ci --prefix /path/to/registry/resources
```

Factory provisions the MCP connection and communication skill automatically. Agent questions appear in `factory inbox` and the monitor. Reply with `factory answer REQUEST-ID "your response"`; choice requests require one of their listed options. Inspect the recent conversation with `factory EXECUTION-ID messages -o json`, or follow the complete execution stream.

This tutorial uses sequential loops. To configure concurrent peers, use [the loop-mode reference](docs/factory-json.md#loop-execution-mode) and adapt both routes and assignments. Merely changing the mode while retaining build→review internal routing is rejected.
