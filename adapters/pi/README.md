# Pi adapter

Connects Pi's native RPC sessions to Controls. It starts Pi, delivers thread messages and operator input, translates Pi usage/tool events, and keeps the session alive between turns. It uses the [TypeScript SDK](../../sdk/typescript/README.md) for its authenticated MCP connection.

Requires Node.js 22+ and an installed Pi executable. Install dependencies with `npm ci` from the Controls root; the Go binary installer does not install these dependencies. Run adapter checks with `npm test --workspace @pragma/adapter-pi` there. Tests use a fake Pi process and a loopback MCP server, without model calls.

The `@pragma/adapter-pi` package provides `pi-rpc-session.mjs`. Registry installs this package as a local dependency; managed agent launchers invoke that entrypoint. Standalone agent sessions continue to invoke Pi directly.

See the [adapter contract and runtime setup](../README.md) for provisioned environment variables, delivery semantics, failure handling, and other runner integrations.
