# Runtime adapters

Controls owns these runner integrations. Each adapter translates a runner’s native interface into the public execution and communication contract; the Go controller does not import Pi-specific code.

- [Pi](pi/README.md): native RPC session adapter.
- [SDKs](../sdk/README.md): shared protocol clients used by adapters and runnable packages.

Agent roles and domain skills describe the work. Runtime adapters connect a native runner to the control plane's conversation protocol.

## Setup

Install the adapter and SDK workspace dependencies once in the Controls checkout:

```sh
npm ci --prefix /path/to/controls
```

This is separate from `./install.sh`, which builds and installs only the Go binary. The Pi adapter requires Node.js 22+ and an installed Pi executable. Registry consumers also need their [local package dependencies installed](../../registry/README.md#controls-dependencies); the default dependency paths assume sibling Controls and Registry checkouts.

Each agent owns its native Pi defaults, provider/model, tools, and domain skills. Managed `runtime: {"name":"pi"}` packages use the Go Pi preparer for their private profile and exact executable; standalone entrypoints retain profile preparation. Standalone Registry entrypoints use Pi directly; managed sessions invoke the installed adapter package.

## Pi under Factory

Factory supplies `FACTORY_MCP_URL`, a scoped `FACTORY_MCP_TOKEN`, `FACTORY_THREAD_ID`, `FACTORY_PARTICIPANT_ID`, and the runtime-provided `FACTORY_COMMS_SKILL` directory. Do not configure these manually or put credentials in model prompts.

For explicitly selected managed Pi runtimes, the Registry entrypoint reuses the prepared profile and invokes the [Pi adapter](pi/pi-rpc-session.mjs). The adapter:

- Adds the Factory MCP server to the private native Pi profile, preserving existing MCPs. The credential stays in the environment.
- Loads the controller's `control-plane-comms` skill and starts Pi in RPC mode.
- Streams native JSONL output unchanged and accepts operator steering input.
- Waits for thread messages with the official MCP client, delivering peer messages through Pi RPC and acknowledging retained delivery.
- Reports native prompt dispatch (`busy`) and settled turns (`idle`) through typed MCP `runtime_status` messages. Reports are ordered and use stable IDs for bounded transport retries. The controller uses these observations for [completion recovery](../docs/communications.md#errors-and-bounded-recovery); it does not infer idle from quiet stdout.
- Keeps the session available when Pi settles normally between turns. The agent ends its turn while waiting; the adapter waits in the background and wakes it on messages. The model-facing `wait_for_message` tool is hidden to avoid competing polling.
- Continues a response truncated with `stopReason: length` at most twice per invocation. Each continuation is visible in captured output and asks for concrete incremental work. `FACTORY_PI_CONTINUATION_LIMIT` (0–10, default 2) can change that bound in the service environment before startup.
- Terminates visibly when continuation is exhausted or a model turn ends with `error` or `aborted`. It does not disguise those states as idle waiting. An accepted final result ends the invocation; a normal idle turn does not.

The agent completes its invocation by sending a `step_result` payload to the controller through `send_message`. Its data contains the allowed outcome and factual summary. Factory validates and collects applicable files according to `outputs`/`outputs_on`. There is no completion file or shell result helper.

An accepted member result does not complete a concurrent loop. The engine waits for every member and reduces configured routes: failure takes priority, then a route back to the loop entry, then the common exit. Human approval remains a separate configured decision.

## Another runner

Configure its native MCP client with the provisioned endpoint and environment credential. Load the shared communication skill, accept the supplied assignment, and submit typed messages through the same eight tools. To deliver messages into an idle session, translate them into that runner's supported queued/steering input mechanism. Without automatic delivery, the agent can use `wait_for_message` itself. Do not run two competing delivery loops.

Discover the assigned controller with `list_participants` and supply exactly that one recipient; omitted or extra recipients are rejected. Filter incoming lifecycle observations from model prompts, while advancing/acknowledging their cursor. Report `runtime_status` with exactly one data part containing `state: "busy"` or `state: "idle"` in the assigned control thread, addressed to its controller. Report native transitions rather than output silence. A runner without this capability has no idle-completion recovery; rejected-result recovery and ordinary invocation supervision still apply. Automatic incoming-message delivery never retries an outgoing result.

No adapter chooses outcomes, workflow routes, or human authority. Deterministic programs can use an MCP SDK client without an agent skill. A runner that cannot remain available across idle turns needs an explicit continuation capability before it supports concurrent collaboration.

## Verification

```sh
npm test --prefix /path/to/controls
```

The RPC test uses an official MCP server/client fixture and a fake Pi process. It checks idle-session delivery, operator steering, preservation of existing MCP configuration, accepted-result termination, bounded truncation recovery, and visible provider/abort failures without model calls. It needs permission to bind a local loopback port.

Registry owns agent launchers and [local tool preflight](../../registry/agents/README.md#local-tool-preflight). Its [verification instructions](../../registry/README.md#verification) cover skill discovery and launcher checks. Those are separate from the Controls adapter tests above.

## Pi tool exposure

In managed Pi sessions, `list_participants`, `send_message`, and `read_messages` use native **direct** exposure, so the model calls their schemas without writing a codemode script. Other Factory tools remain available through codemode; the background-delivery wait tool stays hidden. This is a Pi presentation choice. The MCP protocol and shared communication skill remain runner independent; another adapter can expose the same schemas through its runner's native tools.

## Resource telemetry

The Pi RPC adapter emits `factory.telemetry.v1` records alongside retained native output. `telemetry.mjs` translates completed assistant usage into separate input, cache-read, cache-write, and output categories; it also reports tool start/end and settled progress. Streaming usage snapshots are excluded. Missing or zero-filled Pi pricing remains unknown.

Other runners may emit the same versioned, newline-delimited protocol without depending on Pi. The control plane owns persistence, display, and per-attempt budget enforcement. See [resource metrics and the adapter contract](../docs/resources.md). Agent completion and collaboration still use MCP, independently of resource reporting.

## Generic package inputs

The Go controller supports `packages`, `type: "run"`, package-specific `overrides`, and named `input_bindings`. It supplies private JSON files through `FACTORY_INPUTS_PATH` and `FACTORY_PACKAGE_CONFIG`; an LLM assignment at `FACTORY_PROMPT_PATH` also contains the bound JSON. The Pi adapter reads that prompt file when no explicit `-p` was supplied. Classification bundles use the same MCP completion contract and need no Pi runtime. See [package bindings](../docs/packages.md).

## Adding a native runner

Follow [Adding an agent runtime](../docs/runtime-adapters.md) for the Go preparation port, selection/identity, capabilities, lifecycle ownership and runnable non-Pi conformance fixture. The native runner is separate from the `command` host execution mechanism. Pi supports [per-package overrides](../docs/pi-overrides.md); lifecycle controller-only routing is enforced.

Pi implements [per-package overrides](../docs/pi-overrides.md). An optional side-effect-free `Validator` rejects unsupported settings before execution persistence; host-dependent preparation remains in `Prepare`.
