# Lifecycle hooks

Hooks run trusted host utilities at deterministic execution, stage, and step boundaries. They are optional, runner-neutral, and independent of agent skills. No Git behavior is built into Controls.

## Configuration

The same object is accepted at the root of `factory.json`, on a stage, and on a step (including a step nested in a loop):

```json
"hooks": {
  "before": [
    {
      "name": "prepare",
      "command": "python3",
      "arguments": ["/path/to/utility.py", "argument with spaces"]
    }
  ],
  "after": []
}
```

| Scope | Before | After |
| --- | --- | --- |
| Execution: root `hooks` | Before entering the workflow. | After successful workflow completion. |
| Stage: `workflow.stages[].hooks` | On entry, before any member step. | On successful exit, after loops and the stage's human decision. |
| Step: `hooks` on a step | Before each agent attempt, or before waiting for a decision. | After a successfully returned invocation/decision, before following its route. |

A route to failure suppresses the corresponding `after` hook. A nonterminal result such as `changes_requested` is still a completed invocation: its step `after` hooks run, but its stage remains open when routing within that stage. Repeated loops inside a stage do not repeat stage hooks. Leaving and later re-entering a stage creates a new stage visit. Human feedback routed back inside the same stage does not close it.

Concurrent step `before` hooks execute in configuration order before **any** member launches. Step `after` hooks run in configuration order after every member has settled; they cannot run while a peer is still writing. Stage `after` hooks run once the whole stage exits, not once per loop round. Prefer a stage hook for a multi-agent commit.

Hooks in each list run sequentially. Names must be unique within a scope's `before` list and within its `after` list; the same name may appear in another scope or boundary. Unknown keys are rejected. There is no implicit shell, argument substitution, executable installation, or Registry lookup. Use `"command":"go","arguments":["run","./utility"]`, rather than `"command":"go run"`.

The working directory is the directory containing `factory.json`, which is also the project root. Relative script arguments resolve there. Bare commands resolve through the host service's PATH. Hooks inherit the service's permitted process environment; credentials and signing configuration must already be available. Keep secrets in the host environment rather than recorded hook arguments or context. Hook configuration is recorded with the execution; referenced utility files are not copied or hashed, so retain the selected utility version for reproducible runs.

## Standard input

Every hook receives one versioned JSON object followed by EOF. There is no interactive stdin and no model call:

```json
{
  "version": 1,
  "invocation_id": "c67eb4c6-7458-59dc-9afb-0e22bfb25eb1",
  "execution_id": "df61a2bc-da62-4c2a-a6bc-bd0f90cf0923",
  "project": "/work/project",
  "scope": "stage",
  "boundary": "after",
  "name": "commit-work",
  "stage": "implementation",
  "stage_visit": 1,
  "participants": [
    {"agent":"coder_golang","instance_id":"assigned-participant-id","step":"build","visit":1,"attempt":1,"outcome":"ready"},
    {"agent":"reviewer","instance_id":"review-participant-id","step":"review","visit":1,"attempt":1,"outcome":"completed"}
  ]
}
```

Step contexts additionally contain `step`, `step_visit`, and `step_attempt`. Their `after` context includes `outcome`. Stage and execution `after` contexts include successful agent invocations across their completed history; step `after` includes its own invocation. Agent instance identity is the result's assigned actor, not an invented model identity. Human decisions are not listed as agent participants. Failed/unaccepted attempts remain in execution/session evidence, not in successful-participation metadata.

The invocation ID identifies one configured hook at one lifecycle boundary. A hook retry keeps this ID and the exact stdin context. A new step attempt or stage entry has a new ID. Use `invocation_id` as the external idempotency key, not the process PID or hook retry count.

## Failure, retry, and observation

Exit `0` succeeds. Nonzero exit, interruption, or launch failure blocks progression. Later hooks and workflow work wait. Hook retries are explicit and retry **only that hook**; they do not rerun successful earlier hooks or agents.

```sh
factory EXECUTION-ID
factory HOOK-ID -o json
factory HOOK-ID logs
factory HOOK-ID events
factory EXECUTION-ID stream --follow
factory retry HOOK-ID --reason "Corrected host Git signing configuration"
factory monitor
```

Hook records include pending/running/succeeded/blocked states, attempt count, session identity, timing, and failure information. Raw stdout and stderr are retained together in supervised output and the execution stream; they do not require a structured output format. The monitor displays hook progress and offers inspection/retry for a blocked hook under Actions. Hook sessions are noninteractive; use logs rather than sending messages to them. They are excluded from model-usage totals.

Successful hook task results are reused during scheduler replay, and the host also checks the durable receipt before launching. A hook still running on another delivery is not started twice. A supervisor restart marks its lost sessions interrupted; ambiguous launches block for inspection. Cancellation stops hook processes and prevents later boundaries from running.

External effects and the database cannot be committed atomically. A process might create a commit and then be interrupted before success is recorded. Utilities **must be idempotent** and detect prior effects using the invocation ID. Inspect an interrupted utility's side effects before retrying. Hooks use the host command execution timeout (currently two hours) and are cancelled with their execution. They are trusted host commands, not a sandbox.

## Registry Git utilities

[The example configuration](../factory.example.json) demonstrates three injected hooks:

1. Execution `before`: [prepare the execution branch](../../registry/skills/git-work-branch/SKILL.md).
2. Stage `after`: [commit the combined work](../../registry/skills/git-commit/SKILL.md).
3. Execution `after`: release the checkout reservation, retaining the branch and history.

Replace the example agent paths and script arguments with paths for your installation. The example's `../registry/...` paths assume sibling Controls and Registry checkouts and are resolved from the project directory, not this documentation. Copying the example to another project requires updating them. Use absolute script paths when Registry remains outside the project.

Python 3.9+ and Git are needed for these optional Unix utilities; Controls itself has no Python/Git dependency. Configure Git user identity and any required signing tools first. Preparation refuses pre-existing changes and reserves the checkout for one execution. Commit paths are explicit; the example's `--path .` deliberately stages all nonignored project changes. Narrow them when appropriate and keep runtime/secrets excluded through ignore rules. The scripts cannot distinguish unrelated human edits in the same files.

A stage commit has a deterministic Conventional Commits header and identity trailers. It records participation, not proof that each agent reviewed the final bytes. Normal Git hooks and signing apply; nothing pushes, merges, resets, or amends. No changes creates a no-op receipt. A failed/cancelled execution keeps its checkout reservation for inspection; do not release it while its processes are active.
