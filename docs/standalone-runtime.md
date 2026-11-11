# Standalone communication and supervision

Build with `go build -o bin/factory-runtime ./cmd/factory-runtime`, then run:

```sh
FACTORY_DATABASE_URL=postgres://USER:PASSWORD@127.0.0.1:5432/factory \
  bin/factory-runtime --root /absolute/private/runtime-directory
```

Supply a PostgreSQL database; this mode does not start Hatchet or provision a
PostgreSQL installation. Controls applies its normal migrations. Use a dedicated
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
