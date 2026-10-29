# Local agent sessions

Factory starts one local controller and session supervisor per user on demand. Every CLI and monitor connects through the same private Unix socket at `~/.factory/runtime/factory.sock`, regardless of the project directory or inherited shell settings. `FACTORY_RUNTIME_DIR` does not select another service. Projects and contexts organize executions inside this shared service; they do not create separate controllers or databases. The supervisor launches configured commands without a shell, captures their PTY output, and keeps them running when the CLI exits. Each session has an ID, optional parent session, execution and step IDs, runner name, state, output, and operator messages. Factory stores session records, messages, and output in its PostgreSQL schema. Only live PTY handles remain in the worker process.

The CLI exposes `factory SESSION-ID`, `logs`, `messages`, `send`, and `attach`. `factory EXECUTION-ID stream -f` follows workflow transitions and captured session activity in order. The package runner uses the supervisor API to launch top-level agent sessions. The monitor combines MCP conversation messages, formal input requests, and operator session messages in its right-hand inbox; `g` filters recipients and Enter opens the full record. `s` selects a session independently of the current workflow step, so concurrently running sessions remain individually inspectable. Concurrent scheduling is configured with `loop.mode`; the view shows each active member independently. Green LIVE indicates a successful service refresh, not process activity.

## Runner interface

The local HTTP API is served only on the Unix socket:

| Operation | Endpoint |
| --- | --- |
| List sessions, optionally filtered by `execution_id` | `GET /sessions` |
| Launch an agent process | `POST /sessions` |
| Read one session and its output | `GET /sessions/{id}` and `GET /sessions/{id}/output?offset=-1` |
| List and send operator messages | `GET` and `POST /sessions/{id}/messages` |
| Stop a process | `POST /sessions/{id}/stop` |
| Runner reads and acknowledges messages | `GET /sessions/{id}/inbox` and `POST /sessions/{id}/ack` |

The Go client in `internal/sessions` exposes these operations. A launch request names a runner, working directory, command arguments, and optional `execution_id`, `step_id`, and `parent_id`. It may also supply environment variables. The supervisor adds `FACTORY_SESSION_ID`, `FACTORY_SESSION_TOKEN`, and `FACTORY_CONTROL_SOCKET` to the process environment. A runner adapter can poll its inbox with the bearer token and acknowledge a message only after it has presented it to the agent. Unacknowledged messages are returned on subsequent polls. Factory stores a hash of the token in PostgreSQL; the plaintext token is not written to a session record.

For a command that accepts line-oriented terminal input, set `input_mode` to `pty`. The supervisor writes a message to its PTY and records it as *delivered* when the write succeeds. That does not prove the agent read it. The bundled Pi RPC adapter interprets each line as a steering prompt and exposes Pi's response in the stream. The default `queue` mode is appropriate for an adapter that polls and acknowledges the inbox. Message states distinguish queued, delivered, read, and failed.

The supervisor only knows about sessions it launches. Runner-internal subagents must be started through it or reported through an adapter to appear individually. If the supervisor restarts on the same host and runtime root, it marks its previously running sessions *interrupted*: their saved output remains, but the lost PTY cannot be reattached. The Hatchet package runner launches sessions through this supervisor. This local interface has no remote authentication or sandbox; both are required before exposing it beyond a trusted single-user host. A deployed multi-worker service also needs worker leases and routing for commands addressed to a session on another host.

## Structured agent communication

Agents use the [thread MCP interface](../comms.md) for progress, publications, questions, responses, and final results. The session inbox/ack endpoints above are runtime infrastructure for operator delivery; they are not a second agent workflow protocol. `factory EXECUTION-ID messages` exposes the newest 500 conversation records, while the execution stream retains full activity. A read acknowledgment is not an executed action.

The Pi adapter injects the connection and shared skill, receives peer messages through the official MCP client, and translates them into Pi RPC input. `agent_settled` leaves the session available. A committed `step_result` completes only that invocation; the workflow adapter owns the concurrent round barrier. The runner allows a bounded receipt/output drain before stopping the process.

## Idle turns and interrupted model responses

Idle waiting and model failure are distinct. A push-enabled adapter retains the session after a normal idle turn and resumes the model only when a message or operator input arrives. It must not require model-driven polling to stay alive.

The registry Pi adapter hides the model-facing `wait_for_message` tool while its own MCP delivery client waits in the background. On `stopReason: length`, it issues at most two continuation prompts per invocation, retaining the existing Pi session and asking for incremental work. Set `FACTORY_PI_CONTINUATION_LIMIT` in the service environment before startup to change this to an integer from 0 to 10; 0 disables continuation. Recovery notices are captured in agent output. Exhaustion, provider errors, and aborted model turns terminate the session with the actual cause; the command runner blocks the attempt and the concurrent round stops its peers. Ordinary idle turns remain open. This is response continuation, not a new workflow round or step attempt.

Cancellation is persisted before engine/session shutdown. Late shutdown progress cannot overwrite the cancelled execution, reopen its requests, or revive its steps. Repeating cancellation retries cleanup without adding another cancellation event.
