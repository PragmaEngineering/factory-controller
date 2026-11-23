# Pi package overrides

Reuse a Registry agent without editing its native profile. Set `runtime.name` to `pi` and put supported settings in that package's `overrides` object. Each package resolves independently; there is no global provider or model default.

## Custom provider example

```yaml
name: builder
path: ../registry/agents/coder_golang
entrypoint: start.sh
runtime:
  name: pi
  version: ">=1.0.0 <2.0.0"
overrides:
  provider: corporate
  model: coding-model
  base_url: https://ai.example.com/v1
  api: openai-completions
  api_key_env: CORPORATE_AI_KEY
  thinking: medium
  model_config:
    reasoning: true
    context_window: 128000
    max_tokens: 8192
    input:
      - text
```

This is a package registration fragment. Supply actual model capabilities and limits from your provider. Metadata is a declaration, not a capability probe against a remote service. Set `CORPORATE_AI_KEY` in the **execution service's environment** before starting that service. Exporting it only in the submitting terminal does not update an already running service. No key belongs in this configuration.

A reviewer can use a different registration and credential environment name. To change credentials alone while retaining its bundled provider/model, use `"overrides": {"api_key_env": "REVIEW_AI_KEY"}`.

## Supported fields

| Field | Native mapping and validation |
|---|---|
| `provider` | `settings.json.defaultProvider`; changing providers requires an explicit `model`. |
| `model` | `settings.json.defaultModel`; exact provider/model match, without fuzzy selection or fallback. |
| `base_url` | Selected `models.json.providers[provider].baseUrl`; HTTP(S), no embedded credentials, query or fragment. |
| `api` | Selected provider's native API protocol: `openai-completions`, `openai-responses`, `anthropic-messages`, or `google-generative-ai`. |
| `api_key_env` | Environment-variable **name**, without `$`; native `apiKey` becomes `$NAME`. Value is supplied only to the process. Reserved runtime/control environment names are rejected. |
| `thinking` | Native default plus explicit CLI selection: `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. A native clamp to another level is an error. |
| `model_config` | Explicit custom model metadata: required `reasoning` boolean, positive `context_window`, positive `max_tokens` not exceeding context. Optional `name`, `input` (`text`/`image`) and `cost` (`input`, `output`, `cache_read`, `cache_write`, per million tokens). |

Unknown fields, nulls, empty strings and invalid types fail validation. No arbitrary JSON patching is supported. A new custom connection requires `base_url`, `api`, `api_key_env` and `model_config`. A built-in model can use Pi's catalog without supplying custom metadata; an unknown model fails rather than receiving invented limits.

## Resolution and precedence

1. Validate the finite override schema before persisting a submitted execution.
2. Probe the exact installed executable. The supported range is stable **Pi 1.x (`>=1.0.0 <2.0.0`)**, intersected with the requested `runtime.version`. Other majors and prereleases are rejected; no installation or upgrade is performed.
3. Copy bundled `pi/*.json` into a new private invocation profile (`0700` directory, `0600` files). Without overrides, retain native bytes and behavior. With overrides, change only supplied fields and conflicting selected-provider/model settings; preserve unrelated native configuration.
4. A credential override replaces the selected provider's copied stored auth entry and known authentication headers. The published bundle and user's global profile remain unchanged.
5. Check resolved selection and credential availability through **offline native RPC** with resource discovery disabled. No model prompt or provider request is sent. This verifies local resolution, not remote credential validity, network access or actual provider capabilities.
6. Launch with that exact executable/profile and explicit provider/model/thinking arguments. The RPC bridge checks the actual session's resolved state before sending its assignment; a conflicting launcher/project setting fails visibly.

When overrides are present, selection/authentication/resumption flags in configured arguments are rejected (`--provider`, `--model`, `--models`, `--thinking`, `--api-key`, session/resume/continue/fork flags). Put selection in `overrides`. Prompts remain ordinary argument values. A package launcher must preserve prepared arguments, `AGENT_EXECUTABLE` and `PI_CODING_AGENT_DIR`; see [adapter integration](runtime-adapters.md).

Overrides are pinned with the execution definition. Failed preparation removes its incomplete private profile. Concurrent invocations use independent profiles. This is configuration isolation, **not a sandbox**: the current trusted host process still inherits service privileges and environment. Native auth files may contain sensitive data and remain private.

## Audit and retry behavior

Sessions record native name/version, adapter revision, executable and resolved non-secret `runtime.configuration`: provider, model, protocol, endpoint, thinking, reasoning, limits and credential environment **name**. Credential values are excluded. Unchanged native-default invocations retain existing metadata without a fabricated resolved model record.

A retry within the same step visit rejects a different native version, adapter revision or runtime name. Start a new execution for those changes. Requested configuration remains pinned; this is not resume-with-new-configuration support. Executable bytes are not vendored or frozen.

Errors identify fields and codes such as `invalid_config`, `missing_credentials`, `unknown_model`, `unsupported_protocol`, `unsupported_capability`, `config_conflict`, `runtime_preflight`. Native diagnostic output is not copied into configuration errors because it can contain credentials. Inspect private profiles without publishing their contents.

## Verification and extension

`go test ./adapters/pi` covers schema, credential precedence, private profiles, concurrent preparation, process launch and redaction. `npm test --workspace @pragma/adapter-pi` checks actual startup selection before assignment plus existing MCP result/delivery behavior.

To check an installed supported binary without model calls:

```sh
FACTORY_TEST_INSTALLED_PI=/absolute/path/to/pi go test ./adapters/pi -run TestInstalledPi1x -v -count=1
```

The smoke test supplies a local test endpoint and asserts zero provider requests. It does not use your production API key. Supporting another Pi version requires native configuration/RPC compatibility evidence and this smoke test, not a wider version range alone. For another runner, implement its own translation behind the [adapter preparation port](../adapters/adapter.go), optional side-effect-free `Validator`, native bridge and conformance tests; do not add runner branches to the controller or workflow engine.
