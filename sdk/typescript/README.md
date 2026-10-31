# Controls TypeScript SDK

A small ESM client library with TypeScript declarations for programs participating in Controls executions. It wraps the pinned official MCP SDK; it does not launch agents, interpret classifier answers, or select workflow routes.

## Installation

From the Controls checkout, run `npm ci`. This installs the SDK and adapter workspace dependencies. Local consumers can install this directory as an npm dependency:

```sh
npm install /path/to/controls/sdk/typescript
```

The Registry already declares this dependency using a sibling Controls checkout. No package registry publication is required.

## API

```typescript
import { connect, withController } from '@pragma/controls-sdk/result-client';
```

`connect(environment, name?)` returns a connected official MCP `Client`, using `FACTORY_MCP_URL` and `FACTORY_MCP_TOKEN`. The caller owns closing the connection and can call the conversation tools through `client.callTool(...)`.

`withController(environment, callback)` connects, discovers the assigned thread's controller, and passes a `submit(result)` function to the callback. It always closes the connection afterward. Submission sends a `step_result` addressed to the controller and rejects unless its effect is `accepted`. Its operation ID is stable for the invocation's participant; repeated submissions must retain the same result.

The environment must include `FACTORY_THREAD_ID` and `FACTORY_PARTICIPANT_ID` in addition to the connection variables. Controls provisions these for managed invocations. Never include the token in prompts or result data.

The [Pi adapter](../../adapters/pi/README.md) uses `connect`. Registry's task-readiness classifier uses `withController`. The [communication contract](../../comms.md) defines the tool payloads and controller behavior.

The [interoperability fixture](fixtures/mcp-client.mjs) exercises the official MCP client against the Go server without a model.
