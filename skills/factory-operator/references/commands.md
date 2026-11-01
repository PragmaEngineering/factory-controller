# CLI commands and resource IDs

Run from the intended project directory. Uppercase IDs and paths below stand for values returned by Factory; do not pass the labels literally.

## Start or locate an execution

```sh
factory                                      # help; does not launch work
factory exec "Add the requested behavior" --context '*'
factory exec "Use these documents" -f /path/brief.md -f /path/constraints.md --context '*'
factory exec -f /path/brief.md -c /path/project/factory.json --context '*'
factory exec -f - --context '*'              # provide the request on stdin
factory ls -o json
factory ls --context CONTEXT_ID -o json
factory EXECUTION_ID -o json
```

Config resolution is exact: `./factory.json`, or the supplied `-c` path. Factory does not search parents. Relative agent paths resolve from that configuration's directory, which is also the project working directory. Each `exec` creates a distinct execution; it is not a resume command. Input files are captured by Factory for that execution. Credentials belong in the configured environment, not input files or messages.

`exec` prints `CONTEXT`, `EXECUTION`, `CONFIG`, and `STATE`. JSON execution inspection returns a resource envelope with `type` and `execution`. The execution contains `state`, `steps`, `requests`, `error`, and pinned `configuration`. Steps have `key` (configured name), `id` (runtime ID), `visit`, `try`, `state`, and `sessions` (session IDs). Use the current visit/attempt, not an older session with the same step name.

## Observe

```sh
factory EXECUTION_ID                         # readable stage/step status and session IDs
factory EXECUTION_ID stream -f               # full activity, following new output
factory EXECUTION_ID messages -o json        # newest 500 thread messages
factory SESSION_ID -o json                   # session identity and process state
factory SESSION_ID logs -f                   # selected agent output
factory SESSION_ID messages -o json          # operator-to-session delivery records
pragma-monitor                              # optional separate web monitor (installed independently)
```

The execution stream prints `{source}: {data}` lines; it has no JSON-output option. `-o json` applies to inspection/list/messages commands shown here, not `stream` or `logs`. Stream records include captured agent output, which can itself be JSON; that does not make the entire text stream a JSON document. Omit `-f` for a snapshot. Stop a follower through the host's process/session controls without cancelling the execution. Keep tool output bounded while retaining the underlying capture when needed.

Execution thread messages and session operator messages are different views. Use the execution view for agent exchanges; use the session view to inspect an operator message's queued/delivered/read/failed state. Delivery or read acknowledgment does not prove execution of the requested action. Runner-specific input capability determines whether live steering is supported.

| Execution state | Operator interpretation |
|---|---|
| `ready`, `running` | Inspect active steps and sessions; no completion claim yet. |
| `waiting_input` | Inspect pending requests and seek or apply an authorized answer. |
| `blocked` | Inspect the cause and blocked step or hook before deciding whether to retry. |
| `completed` | Configured workflow completed; assess product claims from their evidence. |
| `failed`, `cancelled` | Terminal state; report the cause and retained evidence. |

## Send feedback or answer a request

```sh
factory SESSION_ID send "The user's scoped feedback"
factory SESSION_ID send --file /path/feedback.txt
factory SESSION_ID send --file -              # feedback on stdin
factory SESSION_ID attach                    # interactive output plus line-based steering
factory inbox --context CONTEXT_ID -o json
factory REQUEST_ID -o json
factory answer REQUEST_ID OPTION --reason "User decision or delegated rationale"
```

`send` targets a session ID, not an execution ID, agent name, or OS PID. Messages are limited to 16 KiB; large initial inputs should use `exec -f`. `attach` requires a running session and supported input. An execution/step ID can select a current session for attach, but select a session explicitly when several agents are active.

Pending requests can be formal workflow decisions or agent input questions. Use only the listed `options`. If an input request has no options, pass the user's free-text answer as the quoted second argument to `answer`, without `--reason` (for optionless input, a supplied reason takes precedence as the answer). An already-answered request should not be answered again. A step name is not a request ID.

## Retry or cancel

```sh
factory STEP_ID -o json
factory retry STEP_ID --reason "Diagnosed cause and why repeating is now appropriate"
factory cancel EXECUTION_ID
```

Retry accepts a blocked runtime step or lifecycle hook. For a hook, inspect `factory HOOK_ID -o json` and `factory HOOK_ID logs`, then use `factory retry HOOK_ID --reason "Diagnosed cause"`. This repeats only the hook; successful earlier hooks and agents remain complete. Hooks have noninteractive sessions and do not accept messages. It is distinct from a configured loop round or a new execution. Inspect the response and follow the existing execution; if retry is rejected, report that rather than inventing a resume command. A cancellation stops that execution's work; quitting a monitor or detaching does not.

## Execution reviews

```sh
factory report EXECUTION_ID -o json
factory report --factory FACTORY_NAME --from 2026-10-01 --to 2026-10-31 -o json
factory report --snapshot REPORT_ID -o json
```

Reports are saved database snapshots. Default history is the last 90 days, independent of the selected context. Preserve the returned report ID for exact retrieval. Separate reported counters from agent judgments and human decisions; unavailable quality measurements must not be invented. Generating a report does not rerun product tests or invoke a judge. See [report semantics](../../../docs/reports.md) for template and MCP access.
