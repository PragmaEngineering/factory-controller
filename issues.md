# Issues

## CTRL-001 — Runtime lifecycle reports broadcast into agent conversations

**Priority:** High  
**Status:** Open

### Defect

The Pi adapter sends `runtime_status` payloads (`busy` / `idle`) through MCP `send_message` without specifying `recipients`. The communication service treats omitted recipients as a broadcast to all thread participants, including peer agents and the operator.

Peer adapters inject incoming messages into their native agent sessions without excluding lifecycle reports. Receiving a peer's status can therefore trigger a model turn, which emits another status report. This creates a possible feedback loop, unnecessary token consumption, misleading activity and operator inbox noise. The broadcast defect is confirmed in code; a sustained feedback loop has not yet been established from execution evidence.

Lifecycle reports describe runtime observations for completion recovery. They are not agent-authored collaboration messages and must not wake peer agents.

### Relevant code

- [Pi lifecycle reporting and incoming delivery](adapters/pi/pi-rpc-session.mjs)
- [Broadcast recipient resolution](internal/comms/messages.go)
- [Runtime status handling and completion recovery](internal/comms/recovery.go)

### Required change

1. Have the adapter explicitly address lifecycle reports to the assigned controller.
2. Enforce controller-only routing for `runtime_status` at the communication-service boundary, including direct service calls. Define an explicit rejection for omitted or additional recipients rather than allowing a broadcast.
3. Defensively exclude lifecycle reports from native model-input delivery, while preserving cursor advancement and acknowledgment.
4. Retain lifecycle state in execution observability without treating it as operator inbox correspondence. Operator access to the audit history must remain available.
5. Update the communication contract, adapter documentation, skill and diagram to reflect enforced routing.

### Acceptance criteria

- Busy/idle reports update the correct invocation's persisted lifecycle state and remain observable.
- Peer agents and the operator are not recipients of lifecycle reports.
- A lifecycle report cannot cause a model turn or another lifecycle report through incoming-message injection.
- An omitted, broadcast or non-controller audience is rejected with a classified, actionable error.
- Defensive filtering does not prevent later genuine messages from being delivered or acknowledged.
- Regression tests include two simultaneously running adapter fixtures and prove bounded message counts with no lifecycle-induced turns.
- Result acceptance, bounded completion recovery and ordinary peer/controller messages continue to work.

### Existing executions

Source changes will not update already-running adapter processes. Assess affected execution traces for extra turns and token usage; disclose any intervention or contamination when reporting an eval. Do not cancel, restart or resubmit an execution automatically as part of this fix.
