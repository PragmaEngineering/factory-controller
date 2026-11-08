---
name: control-plane-comms
description: Communicate with Factory participants through the configured thread MCP connection, including work handoffs, questions, progress, and explicit step results.
---

# Control-plane communication

Use the assigned thread and participant identity supplied by the runtime. Discover peer responsibilities and controller/human IDs with `list_participants`; do not guess identities or configure credentials yourself. Use the launcher-selected source working directory for project operations, and the separately assigned execution files directory for workflow documents/evidence. Do not infer a source root from configuration or artifact paths. Respect the assignment’s scope and permissions.

Use `send_message` for all structured communication. Its payload contains `kind`, `version: 1`, and A2A 0.3 content parts. A text part is `{"kind":"text","text":"..."}`. A data part is `{"kind":"data","data":{...}}`. Use stable `operation_id` values within this assignment; after an uncertain response, retry unchanged with the same ID. Never change content under an accepted ID.

- **Work and feedback:** send a `message` payload to peers or broadcast by omitting recipients. Publish exact work references and revision/digest information when requesting review. Respond to findings while your assignment remains active. A chat reply is not a final result or human approval.
- **Publish work:** send `publication` with one data part containing `files`, an explicit list of relevant execution-files-relative paths, addressed to peers and the controller. Factory retains their exact bytes and returns a `publication_id` with verified file references in the thread. Review the published copies. If files change, publish the new version and obtain new review; do not reuse an old readiness claim.
- **Progress:** use `progress` for substantive milestones and blockers, not heartbeat chatter. Process output is captured automatically.
- **Input:** send `input_request` with one data part containing `prompt` and optional string `options`. Include the controller and human participants as recipients. A reply is an `input_response` correlated by `request_id`; read its actual answer before continuing affected work. Questions cannot waive a configured workflow decision.
- **Completion:** send `step_result` to the controller with one data part containing an allowed `outcome`, factual `summary`, and the current `publication_id` when work was published in this thread. Normally omit `artifacts`: Factory collects configured output files and computes their digests. To include additional files, use objects such as `"artifacts":[{"name":"extra.md"}]`, never an array of filename strings. The host verifies the actual files; do not invent URIs or digests. Wait for that `step_result` call's receipt whose `effect.state` is `accepted`. Acceptance of a publication, input, or runtime-status message does not complete the invocation. A clean process exit, idle turn, final chat response, or `state: stored` without an accepted effect is insufficient.

In concurrent loops, keep the session available between messages. When automatic delivery is active, end your model turn with a concise waiting status when there is nothing actionable. The runtime keeps the process open and resumes you on incoming messages; an idle turn is not a completed assignment. Do not fill that wait with tool calls, shell sleeps, or polling. Do not submit a terminal result for an intermediate draft or each review exchange. Your result completes your invocation, not your peers, the loop, or a human decision. Do not launch other agents or choose workflow routes.

## Completion recovery

The assignment includes a complete completion-call example and allowed outcomes. Use it directly; do not invent a different envelope. `artifacts` is normally omitted. Automatic delivery handles incoming messages only: it does **not** resubmit rejected outgoing calls. After a rejection, correct the identified field and submit the result yourself. Do not end the turn assuming the runtime will retry it.

A controller completion-recovery message means no final result was accepted. Follow its concrete correction and resubmit the handoff without repeating finished implementation, editing unrelated files, or rerunning successful checks merely to repair the message. If work is genuinely incomplete, continue it or send a concrete input request for a real blocker. Do not claim success to satisfy a reminder. Recovery is bounded; ignoring it can block this invocation. Leave `runtime_status` messages to the runtime adapter; do not manufacture busy/idle observations. These reports require exactly one explicit controller recipient and are not collaboration messages. Historical lifecycle traffic in audit history does not require a reply or action.

A managed runtime may push messages into your session. Do not run a competing polling loop when automatic delivery is active. Without automatic delivery, use `wait_for_message` after the last returned cursor; empty timeouts are normal. `read_messages` can inspect history. Acknowledge a retained batch through `ack_through`; read is delivery acknowledgment, not evidence that the requested action was executed. Deduplicate repeated deliveries by message ID.

For `invalid_argument`, correct the identified field and submit the corrected payload with a new operation ID; repeating unchanged invalid data cannot succeed. For an uncertain transport response, retry the identical payload with its original ID. Report invalid outcomes or missing artifact errors accurately and repair the actual problem. Stop new submissions after stale-identity or permission errors. Do not invent an outcome, bypass authorization or an accepted-operation conflict with a new ID, or claim acceptance after a transport failure. For `internal_error`, report the blocker rather than assuming a transient outage. Never place credentials in content or logs.

## Call shapes

These are `send_message` arguments, independent of runner tool syntax. Replace the example IDs with the assigned thread, discovered participant IDs, unique operation IDs, and returned publication ID. `recipients` belongs beside `payload`, not inside it. Use only outcomes allowed by the assignment.

Publish a reviewable revision (the file list is explicit, not a glob):

```json
{
  "operation_id": "design-publication-r2",
  "thread_id": "ASSIGNED_THREAD_ID",
  "recipients": ["CONTROLLER_ID", "REVIEWER_ID"],
  "payload": {
    "kind": "publication",
    "version": 1,
    "parts": [
      {"kind": "text", "text": "Revision 2: addressed F-001; please review the changed acceptance criteria and affected plan items."},
      {"kind": "data", "data": {"files": ["spec.html", "plan.html"]}}
    ]
  }
}
```

Submit a final result for this invocation:

```json
{
  "operation_id": "design-result-r2",
  "thread_id": "ASSIGNED_THREAD_ID",
  "recipients": ["CONTROLLER_ID"],
  "payload": {
    "kind": "step_result",
    "version": 1,
    "parts": [{"kind": "data", "data": {
      "outcome": "ALLOWED_OUTCOME",
      "summary": "Revised the requested behavior and corresponding plan items; F-001 addressed. No unresolved design questions.",
      "publication_id": "RETURNED_PUBLICATION_ID"
    }}]
  }
}
```

For exchanges, state the publication/revision, concrete requested action, and open finding IDs. Consolidate related findings rather than sending each tentative observation. On subsequent reads, use the returned cursor instead of reloading the full history; inspect earlier messages only when needed. Runtime-delivered messages already contain their content: act on them rather than fetching the same history again. Do not repeat participant discovery unless membership or responsibilities change.

Workflow file publication and required output names resolve under the execution files directory supplied in your assignment. Project source and workflow documents are separate; do not copy deliverables beside factory.yaml. Use the assigned absolute deliverable paths when writing or rendering them.
