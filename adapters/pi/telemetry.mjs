// Translate completed Pi events into the runner-neutral host telemetry protocol.
// Streaming usage snapshots are intentionally excluded to prevent double counts.
const limit = 16384;
const sensitive = /^(authorization|password|secret|token|api[_-]?key|access[_-]?token|refresh[_-]?token)$/i;
function detail(value) {
  const raw = typeof value === 'string' ? value : JSON.stringify(value, (key, item) => sensitive.test(key) ? '[redacted]' : item) ?? '';
  return { value: raw.slice(0, limit), truncated: raw.length > limit };
}
export function createTelemetry() {
  const windows = new Map();
  let turn = 0;
  return record => {
    if (record.type === 'response' && record.success && record.data?.model) {
      const model = record.data.model;
      if (Number.isSafeInteger(model.contextWindow) && model.contextWindow > 0) windows.set(`${model.provider}/${model.id}`, model.contextWindow);
    }
    if (record.type === 'turn_start') turn++;
    const event = telemetry(record, windows);
    if (event && turn && ['usage','tool_started','turn_started','turn_finished'].includes(event.kind)) event.turn = turn;
    return event;
  };
}
export function telemetry(record, windows = new Map()) {
  if (record.type === 'message_end' && record.message?.role === 'assistant' && record.message.usage) {
    const { usage, provider, model } = record.message;
    const names = ['input', 'cacheRead', 'cacheWrite', 'output'];
    if (!names.every(name => Number.isSafeInteger(usage[name] ?? 0) && (usage[name] ?? 0) >= 0)) return;
    const event = { kind: 'usage', tokens: { input: usage.input ?? 0, cache_read: usage.cacheRead ?? 0, cache_write: usage.cacheWrite ?? 0, output: usage.output ?? 0 }, provider, model };
    if (record.message.stopReason) event.stop_reason = record.message.stopReason;
    if (record.message.errorMessage) event.error = detail(record.message.errorMessage).value;
    const capacity = windows.get(`${provider}/${model}`);
    if (capacity) event.context_window = capacity;
    // Some providers omit prices or Pi supplies zero-filled pricing. Zero is
    // ambiguous, so it must remain unknown rather than imply free inference.
    if (Number.isFinite(usage.cost?.total) && usage.cost.total > 0) event.cost_usd = usage.cost.total;
    return event;
  }
  if (record.type === 'tool_execution_start' && record.toolCallId) {
    const args = detail(record.args ?? {});
    const command = record.args?.command ?? `${record.toolName || 'tool'} ${args.value}`;
    return { kind: 'tool_started', tool_id: record.toolCallId, tool_name: record.toolName || 'tool', command: String(command).slice(0, 2048), arguments: args.value, truncated: args.truncated || String(command).length > 2048 };
  }
  if (record.type === 'tool_execution_end' && record.toolCallId) {
    const event = { kind: 'tool_finished', tool_id: record.toolCallId, failed: record.isError === true };
    if (record.isError === true) {
      const result = record.result;
      const output = detail((result?.content ?? []).filter(part => part.type === 'text').map(part => part.text).join('\n'));
      const error = detail(result?.details ?? result?.error ?? '');
      Object.assign(event, { tool_name: record.toolName || 'tool', output: output.value, error: error.value, truncated: output.truncated || error.truncated });
      const exit = result?.details?.exitCode;
      if (Number.isSafeInteger(exit)) event.exit_code = exit;
    }
    return event;
  }
  const boundaries = { turn_start: 'turn_started', turn_end: 'turn_finished', compaction_start: 'compaction_started', compaction_end: 'compaction_finished', auto_compaction_start: 'compaction_started', auto_compaction_end: 'compaction_finished' };
  if (boundaries[record.type]) {
    const event = { kind: boundaries[record.type] };
    if (record.errorMessage || record.message?.errorMessage) event.error = detail(record.errorMessage || record.message.errorMessage).value;
    if (record.reason) event.reason = record.reason;
    if (record.aborted === true) event.aborted = true;
    if (Number.isSafeInteger(record.result?.tokensBefore) && record.result.tokensBefore >= 0) event.tokens_before = record.result.tokensBefore;
    if (Number.isSafeInteger(record.result?.estimatedTokensAfter) && record.result.estimatedTokensAfter >= 0) event.estimated_tokens_after = record.result.estimatedTokensAfter;
    return event;
  }
  if (record.type === 'agent_settled') return { kind: 'progress' };
}
