# Pi adapter

Connects Pi's native RPC sessions to Controls. It starts Pi, delivers thread messages and operator input, translates Pi usage/tool events, and keeps the session alive between turns. It uses the [TypeScript SDK](../../sdk/typescript/README.md) for its authenticated MCP connection.

Requires Node.js 22+ and an installed Pi executable. Install dependencies with `npm ci` from the Controls root; the Go binary installer does not install these dependencies. Run adapter checks with `npm test --workspace @pragma/adapter-pi` there. Tests use a fake Pi process and a loopback MCP server, without model calls.

The `@pragma/adapter-pi` package provides `pi-rpc-session.mjs`. Registry installs this package as a local dependency; managed agent launchers invoke that entrypoint. Standalone agent sessions continue to invoke Pi directly.

The adapter reports busy/idle native transitions through MCP so the controller can issue bounded completion-recovery messages. It does not retry a model's rejected result; the model must correct the payload. See [communication and completion recovery](../../docs/communications.md).

See the [adapter contract and runtime setup](../README.md) for provisioned environment variables, delivery semantics, failure handling, and other runner integrations.

## Managed preparation

Select `runtime: {"name":"pi","version":"=1.0.3"}` on the factory package. The Go [preparer](prepare.go) resolves and probes the installed executable, checks compatibility, validates native JSON and copies the bundle’s `pi/*.json` files into its private invocation profile. Only Pi 1.0.3 is currently accepted; additional versions need compatibility evidence. The package launcher reuses that profile and exact executable; the JavaScript bridge alone injects Factory MCP configuration. Standalone launchers still own their own profiles.

Nonempty `with` overrides are rejected until CTRL-005. Native lifecycle observations exist, but CTRL-001’s broadcast defect is not fixed by this boundary work. See the [contributor guide and capability matrix](../../docs/runtime-adapters.md).
