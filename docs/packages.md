# Packages and explicit inputs

A runnable package can be a coding agent, a script or a decision-model classifier. The control plane schedules the same `run` step for each. Provider APIs, model questions and native agent configuration belong to the package, not the controller.

## Registration and settings

```json
{
  "packages": [
    {
      "name": "readiness",
      "path": "../registry/classifiers/task-readiness",
      "entrypoint": "start.sh",
      "overrides": {
        "provider": "openrouter",
        "model": "typesafe/jev-1.14",
        "api_key_env": "OPENROUTER_API_KEY"
      }
    },
    {
      "name": "architect",
      "path": "../registry/agents/architect",
      "entrypoint": "start.sh",
      "runtime": {"name":"pi","version":"=1.0.3"}
    }
  ]
}
```

This is a configuration fragment. Paths resolve from `factory.json`, and every package starts with that file's directory as its working directory. `entrypoint` selects a file in the package; optional `args` are literal arguments except `{prompt}`, which substitutes the generated assignment. The package directory digest, `overrides` settings and optional `runtime` requirement participate in the pinned execution configuration. Secret values belong in the service environment, never `overrides`.

`overrides` is a JSON object interpreted by the selected package/runtime. The Pi adapter translates its finite [override schema](pi-overrides.md) into a private native profile; generic packages interpret their own schema. The classifier example reads `api_key_env` as an environment-variable **name**; another package may implement a different configuration schema. Packages should reject unsupported settings. No global provider/model default is introduced.

The existing `agents` registration and `type: "agent"` / `agent` reference remain accepted shorthand for the same execution mechanism. Package names are unique across both registration lists. New generic examples use `packages` and `type: "run"` / `package`. A human `decision` step does not reference a runnable package.

## Binding inputs

```json
{
  "name": "architecture",
  "type": "run",
  "package": "architect",
  "input_bindings": {
    "task": { "execution": "input" },
    "evaluation_request": { "step": "check-readiness", "output": "request" },
    "evaluation_response": { "step": "check-readiness", "output": "response" }
  },
  "on": { "completed": "$done" }
}
```

Each binding selects exactly one source:

| Selector | Meaning |
|---|---|
| `{"execution":"input"}` | Original brief plus captured document metadata: `{"brief":"...","documents":[{"name":"...","uri":"/absolute/captured/path","digest":"..."}]}`. Document bytes are not automatically embedded. |
| `{"step":"NAME","output":"KEY"}` | Value at `result.data[KEY]` from that step's latest preceding accepted, completed visit in this execution's history. |

Selectors are structured objects, not expressions or JSONPath. Named outputs can hold any valid JSON value. The latest preceding result must contain the requested key; missing values do not fall back to an older visit. A missing producer result or output blocks the attempt before process launch. It never becomes an empty string or invented feedback.

Bindings cannot reference the same step, a human decision or a member of the same concurrent group. Concurrent members receive the history captured before their group starts; they cannot depend on a peer finishing first. Put dependent evaluation after the group. Sequential loops can consume an earlier member's completed result. Routing continues to use existing outcome maps; bindings introduce no conditional syntax or new routing tokens.

Factory retains its original assignment/history prompt context. Bindings add explicitly selected structured values; they are not a context-isolation mechanism. Bound external content is assessment/task data, not authority to override the agent's instructions.

## Runtime interface

The host writes private JSON files for an invocation and passes their paths:

| Environment variable | Contents |
|---|---|
| `FACTORY_INPUTS_PATH` | Object mapping binding names to resolved JSON values; `{}` without bindings. |
| `FACTORY_PACKAGE_CONFIG` | The package's `overrides` object; `{}` when omitted. |
| `FACTORY_PROMPT_PATH` | Assignment instructions, allowed outcomes, prior context and bound JSON values for an LLM. |
| `FACTORY_INVOCATION_DIR` | Private runtime directory for this step visit/attempt, outside the project. |

These supplement the existing execution/step IDs and scoped MCP connection. They contain no injected provider credential values. Packages still inherit the trusted service environment under the current host runner. Controls' Pi adapter automatically reads `FACTORY_PROMPT_PATH` if no explicit `-p` prompt was supplied; standalone Pi calls retain their existing behavior.

## Publishing named outputs

A package sends the existing MCP `step_result` payload, with an optional `data` map:

```json
{
  "outcome": "completed",
  "summary": "Readiness evaluation returned valid typed answers.",
  "data": {
    "request": {"model":"...","state":{},"questions":{}},
    "response": {"model":"...","answers":{}}
  }
}
```

Bodies here are abbreviated. The package sends the original request and response JSON values without constructing another feedback envelope. `request` and `response` are output names chosen by this package, not mandatory names for every step. File `outputs` remain separate: those are execution-files-relative files collected and hashed by the host.

Use `list_participants` to discover the controller, submit through `send_message`, and wait for `effect.state: accepted`. Accepted structured data is persisted with the result and travels in durable workflow history. It is inspectable in execution JSON (`steps[].result.data`) and the message audit. Process exit or writing a JSON file is not completion. The current MCP mutation limit remains 64 KiB including the envelope.

An LLM receiving the evaluation pair gets the original task, exact evaluated questions/criteria and typed answers. It can formulate a contextual clarification question through the operator inbox. Advisory use needs no threshold or feedback template. The readiness bundle also accepts optional `overrides.admission: {"minimum_probability":0.85}`: it returns `admitted` only for a selected `accept` meeting that probability and `review_required` otherwise. The example factory routes the latter to human admission before architecture. This is package policy using ordinary outcome routes, not a new control-plane conditional construct. Valid `clarify`/uncertain answers complete the evaluation call; transport errors, malformed answers and missing bindings are execution failures with the existing retry/recovery semantics.

## Registry example

See [task readiness](../../registry/classifiers/task-readiness/README.md) and the [Go coding factory](../../registry/factories/golang/README.md). The classification bundle currently uses the OpenRouter Decisions protocol. Controls remains provider-agnostic.

## Native runtime selection

A package can declare `runtime: {"name":"pi","version":"=1.0.3"}`; optional `executable` selects the installed native program. Omitting it selects an opaque command package with no advertised agent capabilities. Concurrent peer stages require an explicitly capable adapter. The command host and native runtime are recorded separately. Pi preserves bundled defaults and supports explicit [per-package overrides](pi-overrides.md). Other packages continue to interpret their own `overrides` JSON. See [Adding an agent runtime](runtime-adapters.md) for configuration, lifecycle ownership, capabilities and conformance tests.
