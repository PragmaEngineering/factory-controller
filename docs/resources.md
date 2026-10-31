# Live resources and step budgets

Factory projects resource events emitted by a runner adapter. It does not infer usage from prose, output length, or provider-specific log records. The Pi RPC adapter in Registry emits this protocol; other runners can implement the same contract.

## Inspect an execution

```sh
factory <execution-id>
factory <step-id>
factory <session-id>
factory <session-id> -o json
factory <execution-id> stream --follow
factory monitor
```

Execution and step inspection aggregate every supervised attempt, including failed attempts. Session inspection provides exact token categories, elapsed time, time since progress, active tool commands, tool failures, budget notices, and reported cost. The monitor puts these values inside workflow cards and above the selected agent's logs. Full commands remain in the retained output; cards truncate long commands. Concurrent tool calls remain individually available in JSON `resources.active_tools`.

Tokens are uncached input, cache-read input, cache-write input, and output. Their sum includes cached tokens. Token usage is a reported lower bound, updated on completed assistant responses, not a continuously metered provider balance. Missing usage remains unknown; execution totals identify sessions that have not reported usage. Old sessions are not backfilled from native logs.

Cost is shown only when every counted response has a reported price and no aggregated session has missing usage. Pi's zero-filled pricing is ambiguous, so its adapter treats zero cost as unknown. Reported prices are estimates, not an invoice. Cache hits are never assumed free. Tool failures count runtime-reported failures; a shell command that masks a failing exit code is not detected by this metric.

## Configure a budget

Add `budget` to an executable step, including a step nested inside `stage.loop.steps`:

```json
"budget": {
  "tokens": 2000000,
  "elapsed_seconds": 1800,
  "idle_seconds": 600,
  "warn_percent": 80,
  "action": "pause"
}
```

| Field | Meaning |
| --- | --- |
| `tokens` | Sum of the four reported token categories. |
| `elapsed_seconds` | Wall time since this attempt's agent process started. |
| `idle_seconds` | Time since a completed model response, tool start/end, or explicit progress event; starts at process launch before the first event. Includes waiting for peers. |
| `warn_percent` | Warn once per metric at this percentage. Omit or set to zero to disable early warnings. Range 1–100 when enabled. |
| `action` | Required: `warn`, `pause`, or `stop`. |

At least one positive limit is required. Omitted or zero limits are disabled. No budget is imposed when the object is absent. A limit is reached when **any** configured metric reaches its threshold. Warnings and reached limits appear durably in the event stream and session notices.

- `warn`: report the limit and keep the agent running.
- `pause`: stop the process and block the step for an explicit `factory retry <step-id>`. This starts a new attempt with a fresh budget; it does not resume the old process. Earlier usage remains in totals.
- `stop`: stop the process and fail the execution. Start another execution to try again.

Automatic retries do not reset a reached budget. In a concurrent loop, pause interrupts the whole round; an explicit retry starts every member again. Stop interrupts the round and fails the execution. A human decision outside a run step is not subject to an agent budget.

Budgets are **per attempt**, not a spending cap shared across the execution. Elapsed and idle time are checked approximately every 250 ms while the worker is supervising the agent. Token limits depend on adapter reports and may overshoot by a response; missing token telemetry cannot enforce a token cap. Use a time limit alongside token limits. A result already accepted by the controller takes precedence over a later budget check. Process termination is best effort; termination failures are reported. These are operational controls, not security isolation or a provider billing limit.

## Adapter protocol

Write newline-delimited JSON records to supervised stdout. Native output can coexist with them:

```json
{"type":"factory.telemetry.v1","sequence":1,"kind":"tool_started","tool_id":"call-1","command":"go test ./..."}
{"type":"factory.telemetry.v1","sequence":2,"kind":"tool_finished","tool_id":"call-1","failed":false}
{"type":"factory.telemetry.v1","sequence":3,"kind":"usage","tokens":{"input":100,"cache_read":500,"cache_write":0,"output":40},"provider":"example","model":"example-model"}
```

- Sequence numbers must strictly increase within a supervised session. Retain the sequence if reconnecting to the same session. Duplicates and older events are ignored.
- `usage` is a delta for one completed model response, not a cumulative snapshot. Token categories must be disjoint, nonnegative integers. Do not count reasoning tokens again when included in output.
- Optional `cost_usd` is the finite, nonnegative total price reported for that response, including all token categories. Omit it when unknown.
- Pair tool events using `tool_id`. `command` can describe a shell command or another tool invocation. Multiple tools may be active.
- `progress` marks meaningful runtime progress. Do not emit it for every thinking or text delta just to keep an idle budget alive.
- Each record must fit within 256 KiB. Invalid and oversized records are ignored for accounting; raw output is retained.

This is runtime telemetry, separate from agent messages and completion. Agents still use MCP for control-plane communication. Telemetry cannot complete a step. Framing state, counters, output, and stream entries commit together in PostgreSQL; partial lines survive supervisor restart. No provider prices or runner-specific parsing live in the control plane.
