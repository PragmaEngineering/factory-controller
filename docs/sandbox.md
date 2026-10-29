# Sandboxed agent execution

## Status and purpose

This is a design note, not a current Factory feature or a decision to adopt a specific product. The [local command runner](../internal/commandrunner/runner.go) starts agent packages as host processes. It records sessions and results, but it cannot confine an agent's filesystem, network, tools, or secrets. Factory does not need a sandbox to continue local evaluations with trusted agents. It does need an enforceable execution boundary before running untrusted packages or offering multi-user, remote execution.

[OpenSandbox](https://open-sandbox.ai/) is a candidate for that boundary. It is a self-hostable sandbox lifecycle and execution service with Docker and Kubernetes backends, a [Go SDK](https://open-sandbox.ai/sdks/go), command and file APIs, and an [interactive PTY](https://open-sandbox.ai/architecture/data-plane/execd). It would run *under* Factory's workflow controller. It would not decide routes, approvals, retries, or whether an agent's prose counts as a completed step. Hatchet would continue scheduling workflow work; Factory would continue recording execution state and evidence.

```text
Factory control plane → execution.Runner → OpenSandbox server → Docker/Kubernetes sandbox
        │                     │                    │
        └─ routes, decisions  └─ result/evidence   └─ process, files, network, resources
```

## Proposed boundary

The existing [`execution.Runner`](../execution/runtime.go) interface accepts pinned `Work` and returns a `StepResult`. An OpenSandbox-backed runner could implement it beside the host command runner. The agent package should still produce the Factory result protocol: an explicit outcome and summary, with required outputs collected and verified by Factory. Exit code zero, terminal text, or a sandbox reaching `Stopped` must not imply workflow success.

For a run step, the runner would create or select an isolated workspace, stage the pinned agent package and task inputs, launch the agent, stream its activity into Factory's durable event log, accept operator input when the agent supports it, collect the result and outputs, then terminate or retain the sandbox according to an explicit lifecycle policy. Sandbox IDs, image digests, resource settings, access policy, and cleanup failures should be recorded with the attempt. Recovery must reconcile Factory's record with actual sandbox state rather than launch a duplicate process after a controller restart.

Agent packages currently reference a local path and entrypoint. A remote sandbox cannot execute that path as-is. We would need a portable package form, probably an OCI image pinned by digest, and a defined way to place the generated prompt, result path, skills, MCP configuration, and tools inside it. OpenSandbox supplies an image-based runtime; it does not package Factory agents for us. The Pi-specific RPC adapter may still be needed to expose Pi's native events and interpret steering messages. OpenSandbox's command stream and PTY show process activity, not every internal agent event.

## Workspace and publication

This is the principal design choice. The options have materially different guarantees:

| Approach | Benefit | Cost or risk |
| --- | --- | --- |
| Writable host bind mount | Edits appear immediately in the developer's checkout; simplest local workflow | The agent can alter the mounted tree directly; concurrent attempts and rollback are difficult; host-path semantics do not carry cleanly to remote Kubernetes workers |
| Copy the project into a sandbox, then publish a change set | Explicit handoff, reviewable changes, better isolation and remote portability | Factory must define snapshots, diffs, conflict detection, publication, and how large repositories move between host and sandbox |
| Managed persistent volume | Workspace can survive a sandbox restart | State ownership, cleanup, concurrent access, and publication still need a contract |

OpenSandbox supports [host and managed volumes](https://open-sandbox.ai/architecture/), but volume support does not resolve publication semantics. A first evaluation can use a copied workspace and export changed files. Production behavior should explicitly answer: who owns the canonical project state, when changes become visible to later steps or the human, how a rejected result is discarded, and whether a reviewer sees a frozen snapshot or mutable files. A writable host mount may be an intentional local convenience mode, but it must be presented as a weaker isolation boundary.

We also need to decide sandbox lifetime. One sandbox per **attempt** gives the clearest cleanup and configuration pinning but requires workspace handoff at every step. One per **execution** reduces startup and copying but allows stages to share mutable state and privileges. A persistent interactive session is useful when a human needs to steer a stuck agent; that does not require keeping every sandbox alive indefinitely. Expiration, cancellation, and failure should have explicit cleanup rules.

## Security and operations

An OpenSandbox deployment adds a lifecycle server, its authentication and configuration, an `execd` component in each sandbox, and Docker or Kubernetes. It does not eliminate image builds, registry access, or corporate CA/proxy issues. Those must be tested with the same networks and machines on which Factory will run. OpenSandbox's default Docker container runtime is not equivalent to a VM boundary; stronger runtimes such as gVisor or Kata require host setup and compatibility testing. [Installation](https://open-sandbox.ai/getting-started/installation), [secure runtime guide](https://open-sandbox.ai/guides/secure-container).

Network access and provider credentials need a policy owned by Factory and enforced by the runner. OpenSandbox offers [egress policy and Credential Vault](https://open-sandbox.ai/guides/credential-vault), but the vault requires additional sidecar/network configuration and TLS interception. It should be evaluated against Pi, package managers, MCP servers, corporate certificates, and each model provider. Putting a real API key in the sandbox's environment, arguments, files, or event stream would defeat the intended secret boundary. Factory must also restrict access to the lifecycle API and sandbox endpoints; otherwise an agent could create or inspect sandboxes outside its assignment.

The [PTY interface](https://open-sandbox.ai/architecture/data-plane/execd) may support Factory's `attach` experience, including terminal input, but its current routes are not fully represented in OpenSandbox's published `execd` OpenAPI contract. Test reconnect, resize, cancellation, and transcript continuity against a pinned release. Factory's message states should still distinguish sent, delivered to a terminal, and read or acted on by an agent when an adapter can establish that evidence.

## Evaluation before adoption

Keep this work outside the current critical path. A focused spike should answer these questions with one existing Go agent:

1. Can Factory launch a digest-pinned agent image against a copied Go project, provide its prompt and inputs, and retrieve a valid `StepResult` plus changed files?
2. Can `factory stream` capture command output, Pi-native events where available, sandbox diagnostics, and failures in order, without losing data on disconnect?
3. Can an operator attach, send a steering message, disconnect, and reconnect to the same live agent? Which actions have evidence of delivery versus reading?
4. What happens on cancellation, sandbox expiry, Factory restart, OpenSandbox restart, and a partially published result? Are orphaned sandboxes discovered and removed?
5. Can resource limits, filesystem access, egress rules, and provider credentials actually be enforced on both a local Docker host and the intended remote runtime?
6. What are cold-start time, image distribution overhead, workspace transfer time, and steady-state resource cost for a representative coding step?

Pin a specific server, `execd`, image, and Go SDK combination for the spike. The project's [capability matrix](https://open-sandbox.ai/sdks/) notes that APIs vary by language and version. Review current upstream issues before using provider credentials; for example, the [Go SDK cross-origin redirect issue](https://github.com/opensandbox-group/OpenSandbox/issues/2170) concerns protected headers. A passing sandbox demo is not sufficient evidence that its access controls or recovery behavior meet Factory's requirements.

Adopt OpenSandbox only if the spike demonstrates a clearer, more reliable runner boundary than implementing the small set of required sandbox operations directly. The control plane and `execution.Runner` contract should remain independent of OpenSandbox either way.
