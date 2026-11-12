# Controls TypeScript SDK

A small ESM client library with TypeScript declarations for programs participating in Controls executions. It wraps the pinned official MCP SDK; it does not launch agents, interpret classifier answers, or select workflow routes.

## Installation

From the Controls checkout, run `npm ci`. This installs the SDK and adapter workspace dependencies. Local consumers can install this directory as an npm dependency:

```sh
npm install /path/to/controls/sdk/typescript
```

The Registry already declares this dependency using a sibling Controls checkout. No package registry publication is required.

## Shared assignment API

```javascript
import { withAssignment } from '@pragma/controls-sdk/result-client';

await withAssignment(process.env, async (assignment, complete, client) => {
  const output = await implement(assignment.input, assignment.configuration);
  await complete(output);
});
```

`implement` represents your program's behavior. Declare `protocol: mcp` in
`factory.yaml` or `fabric.yaml`. The host supplies `AGENT_MCP_URL` and
`AGENT_MCP_TOKEN` for the existing scoped participant connection. Do not manually
configure assignment IDs, threads, input files, or controller identities.

`withAssignment(environment, callback)` fetches `assignment_get`, reports running,
and passes the assignment, `complete(output)`, and the connected official MCP
client to the callback. It closes the connection afterward. The assignment contains
input, configuration, instructions, identity/thread, directories, and output schema.
Configuration contains secret references rather than credential values. Provider
credentials remain in the execution-host environment referenced by YAML.

`complete(output)` validates against the assigned output schema on the server and
resolves only for `state: accepted`. Unchanged retries recover the same receipt;
changed completion is rejected. Direct clients can use `assignment_status` for
running/waiting and `assignment_complete` with a factual `error` instead of output.
Factory maps output to its existing result validation and records errors as runtime
failures. Fabric records capability results/errors. Agents do not select internal
message kinds or workflow routes.

`connect(environment, name?)` returns an official MCP `Client`. It prefers the
`AGENT_MCP_*` bootstrap, with `FACTORY_MCP_*` supported for legacy callers. The
caller owns closing this client. Existing conversation tools remain available for
peer messages and bounded waiting. Keep connection credentials out of output.

Registry readiness uses `withAssignment`; the Pi adapter uses `connect` and the
same assignment operations. Native runtime/profile preparation remains separate;
see [runtime adapters](../../docs/runtime-adapters.md).

## Legacy compatibility

`withController(environment, callback)` retains controller discovery and direct
`step_result` submission for `protocol: legacy`. It requires the legacy Factory
connection, thread and participant variables. `withAssignment` also adapts legacy
Factory input/configuration files for old callers. New implementations should use
the shared assignment API above.

The [interoperability fixture](fixtures/mcp-client.mjs) tests the official MCP
client against Controls without a model. See [communications](../../docs/communications.md)
for message contracts and [package bindings](../../docs/packages.md) for Factory
input/result interpretation.
