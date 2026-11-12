# Standalone communication and supervision

Build with `go build -o bin/factory-runtime ./cmd/factory-runtime`, then run:

```sh
FACTORY_DATABASE_URL=postgres://USER:PASSWORD@127.0.0.1:5432/factory \
  bin/factory-runtime --root /absolute/private/runtime-directory
```

Alternatively, run `bin/factory-runtime --root DIRECTORY --local-database` to
manage a private PostgreSQL installation and persistent data under that directory.
The first run may download PostgreSQL. Logs are in `DIRECTORY/postgres.log`.
This mode does not start Hatchet. Controls applies its normal migrations. Use a dedicated
runtime directory and database for independent operation. SIGINT/SIGTERM stops the
host and its supervised processes. Session records and conversations are retained;
live sessions become interrupted on restart, not transparently resumed.

The owner-only Unix socket is `DIRECTORY/factory.sock`. It exposes the existing
session API described in session-supervisor.md plus these trusted host operations:

| Method | Path | JSON body / response |
| --- | --- | --- |
| GET | `/comms` | Current authenticated MCP endpoint URL and mode |
| POST | `/comms/scopes` | `{ "name": "project" }` returns `{ "id": "scope:...", "name": "project" }` |
| POST | `/comms/scopes/{scope}/participants` | `{ "key": "stable-instance-key", "name": "reviewer" }` returns `url`, `participant_id`, `token` |
| GET | `/comms/participants/{id}` | Standalone identity, scope, and active state; no credential |
| POST | `/comms/authenticate` | `{ "token": "..." }` returns an active standalone identity |
| DELETE | `/comms/participants/{id}` | Retires a standalone identity |
| POST | `/shutdown` | Stops the host and its supervised processes |

Scope creation and identical registration retries are idempotent. Changed names
under the same key fail. Retired keys cannot be reactivated; replacements use new
keys. Credentials survive restart; discover the listener's current URL with GET
/comms. Keep credentials private. Provisioning is never exposed on the MCP port.
The socket grants trusted host authority, including process launch.

Participants use the existing eight comms MCP tools with bearer credentials.
They can create threads among registered peers in the same scope and send custom
structured payloads. Workflow control payloads (`step_result`, `publication`,
`input_request`, `input_response`, `runtime_status`) are rejected for standalone
peers. Application-level invocation/result contracts belong to Fabric. Existing
wire fields retain `execution_id`, containing a namespaced communication scope ID;
this does not create a workflow execution.

Comms is not a process sandbox. Do not expose the trusted Unix API remotely.

## Network access

Use `--listen 127.0.0.1:7331 --public-url https://controls.example.com
--token-file /run/secrets/controls-token` with an HTTPS reverse proxy preserving
Host. The origin binds only to loopback. The token file must contain a random
credential of at least 32 characters. Management routes require that bearer
credential; `/mcp` uses participant credentials and the advertised authority.
Browser origins are rejected. Discovery is written to `ROOT/endpoint.json`.

`POST /workspaces/{uuid}` accepts `{ "files": { "input.json": "..." } }` and
returns an execution-host directory. Only invocation input/config/context/prompt
and the embedded skill filenames are accepted; existing files are not overwritten.
`POST /launch` accepts `{ "session": <existing session launch>,
"environment_references": { "TARGET": "HOST_SOURCE" } }`, resolves environment
references on this host, then uses the existing supervisor. Source packages and
working directories belong to this machine. Neither endpoint requires a shared
filesystem with Fabric. These are trusted host operations, not a sandbox.
