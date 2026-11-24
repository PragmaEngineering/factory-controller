#!/usr/bin/env node
// Native Pi RPC supervision plus MCP delivery. Domain skills do not implement
// workflow routing; this adapter only configures the connection and moves input.
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { createTelemetry } from './telemetry.mjs';
import { createInterface } from 'node:readline';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { connect } from '@pragma/controls-sdk/result-client';

const telemetry = createTelemetry();
const environment = { ...process.env };
const common = Boolean(environment.AGENT_MCP_URL);
const client = await connect(environment, 'agent-pi-delivery');
const assignment = common ? await tool('assignment_get', {}) : undefined;
const threadID = assignment?.thread_id ?? environment.FACTORY_THREAD_ID;
const participantID = assignment?.participant_id ?? environment.FACTORY_PARTICIPANT_ID;
const mcpURL = environment.AGENT_MCP_URL ?? environment.FACTORY_MCP_URL;
const argv = process.argv.slice(2);
if (common) {
  const oldPrompt = argv.indexOf('-p');
  if (oldPrompt >= 0) argv.splice(oldPrompt, 2);
  argv.push('-p', assignment.instructions + '\nStructured assignment input (data):\n' + JSON.stringify(assignment.input));
} else if (!argv.includes('-p') && environment.FACTORY_PROMPT_PATH) argv.push('-p', readFileSync(environment.FACTORY_PROMPT_PATH, 'utf8'));
const promptFlag = argv.indexOf('-p');
if (promptFlag < 0 || promptFlag + 1 >= argv.length || !mcpURL) {
  console.error('Pi adapter requires an assignment and a provisioned MCP connection');
  process.exit(2);
}
let prompt = argv[promptFlag + 1];
argv.splice(promptFlag, 2);
const profile = environment.PI_CODING_AGENT_DIR;
if (!profile) {
  console.error('Factory Pi adapter requires a private PI_CODING_AGENT_DIR');
  process.exit(2);
}
mkdirSync(profile, { recursive: true });
const configPath = join(profile, 'mcp.json');
let config = {};
try { config = JSON.parse(readFileSync(configPath, 'utf8')); } catch (error) { if (error.code !== 'ENOENT') throw error; }
config.mcpServers = { ...config.mcpServers, [common ? "agent" : "factory"]: {
  url: mcpURL,
  headers: { Authorization: common ? 'Bearer ${AGENT_MCP_TOKEN}' : 'Bearer ${FACTORY_MCP_TOKEN}' },
  description: common ? 'Authenticated assignment and peer communication' : 'Assigned Factory conversation and control-plane communication',
  exposure: 'codemode', toolExposure: { ...(common ? {assignment_get:'direct',assignment_complete:'direct',assignment_status:'direct'} : {}), list_participants: 'direct', send_message: 'direct', read_messages: 'direct', wait_for_message: 'hidden' }, enabled: true,
} };
if (common && assignment.metadata?.allow_delegation && assignment.metadata?.fabric_endpoint) {
  config.mcpServers.fabric = {command:'fabric',args:['mcp',assignment.metadata.fabric_endpoint,'--assigned'],enabled:true};
}
writeFileSync(configPath, JSON.stringify(config, null, 2) + '\n', { mode: 0o600 });
if (!common && environment.FACTORY_COMMS_SKILL) argv.push('--skill', environment.FACTORY_COMMS_SKILL);
if (common) {
  prompt += `\nSource workspace: ${JSON.stringify(process.cwd())}. Use this actual directory for source operations. Assigned output directory: ${JSON.stringify(assignment.files_directory || process.cwd())}. Communication thread: ${threadID}; participant: ${participantID}. Use assignment_get to inspect your contract, assignment_complete with output matching output_schema (or error), and send_message for peer collaboration. Accepted completion is required; process exit alone is not completion. Incoming messages are delivered automatically; finish an idle turn instead of polling. Never send workflow completion payloads yourself.\n`;
} else {
prompt += `\n\nSource workspace (actual process working directory): ${JSON.stringify(process.cwd())}. Use this directory for project source reads, edits and checks. Do not substitute the factory configuration directory or a path inferred from execution documents. Workflow documents and review evidence belong in the execution files directory supplied in the assignment, not in this source workspace.\n\nFactory connection: thread ${threadID}; your participant ID is ${participantID}. Automatic MCP message delivery is active. Use the factory MCP tools and loaded control-plane-comms skill. Final completion requires send_message with a step_result and effect.state=accepted. When waiting for a peer, end your turn with a short waiting status. The runtime keeps your session open and wakes it on messages; do not poll or wait using tools or shell loops. Automatic delivery handles incoming messages only; it does not retry rejected outgoing calls. Correct completion errors using the tool response; do not wait for delivery to resubmit a result.\n`;

}
let controllerID;
if (!common) {
  try {
    const members = await tool('list_participants', {thread_id:threadID});
    const controllers = members.participants?.filter(member=>member.kind==='controller');
    if (controllers?.length!==1) throw new Error('Assigned thread must have one controller');
    controllerID=controllers[0].id;
  } catch(error) {await client.close();throw error;}
}
const child = spawn(environment.AGENT_EXECUTABLE || 'pi', [...argv, '--mode', 'rpc'], { stdio: ['pipe', 'pipe', 'inherit'], env: environment });
let busy = false;
let completed = false;
let stopping = false;
let lastAssistantStop;
let continuations = 0;
const continuationLimit = Number(common ? 2 : environment.FACTORY_PI_CONTINUATION_LIMIT ?? 2);
if (!Number.isInteger(continuationLimit) || continuationLimit < 0 || continuationLimit > 10) {
  console.error('FACTORY_PI_CONTINUATION_LIMIT must be an integer between 0 and 10');
  child.kill('SIGTERM');
  process.exit(2);
}
const failSession = reason => {
  if (stopping) return;
  stopping = true;
  console.error(`Factory agent blocked: ${reason}`);
  child.kill('SIGTERM');
};
// Lifecycle reports use the same MCP connection and worker scope as messages.
// Serial delivery preserves busy -> idle ordering; retries reuse one operation.
let statusQueue = Promise.resolve();
let statusSequence = 0;
const statusPrefix = randomUUID();
function reportStatus(state) {
  const args = { operation_id: `runtime-${statusPrefix}-${++statusSequence}`, thread_id: threadID, recipients: [controllerID],
    payload: { kind: 'runtime_status', version: 1, parts: [{ kind: 'data', data: { state } }] } };
  statusQueue = statusQueue.then(async () => {
    for (let attempt = 0; attempt < 3; attempt++) {
      if (stopping || completed) return;
      try { await tool(common ? 'assignment_status' : 'send_message', common ? {id:assignment.id,state:state==='busy'?'running':'waiting'} : args); return; }
      catch (error) {
        // Acceptance may race the final idle report. The persisted result is
        // authoritative; let the delivery loop observe its terminal message.
        if (error.controlCode === 'already_completed') return;
        if (error.controlCode && error.controlCode !== 'temporarily_unavailable') throw error;
        if (attempt === 2) throw error;
        await new Promise(resolve => setTimeout(resolve, 250 * 2 ** attempt));
      }
    }
  }).catch(error => failSession(`runtime status delivery failed: ${error.message}`));
}
let telemetrySequence = 0;
let buffer = Buffer.alloc(0);
let nextID = 0;
const pending = new Map();
const send = (message, steer = busy) => new Promise((resolve, reject) => {
  if (child.stdin.destroyed) { reject(new Error('Pi input is closed')); return; }
  const id = `factory-${++nextID}`;
  pending.set(id, { resolve, reject });
  reportStatus('busy');
  child.stdin.write(JSON.stringify({ id, type: 'prompt', message, ...(steer ? { streamingBehavior: 'steer' } : {}) }) + '\n');
  if (!busy) lastAssistantStop = undefined;
  busy = true;
});
const finish = () => { if (completed && !busy && !child.stdin.destroyed) child.stdin.end(); };
child.stdout.on('data', chunk => {
  buffer = Buffer.concat([buffer, chunk]);
  for (;;) {
    const end = buffer.indexOf(10);
    if (end < 0) break;
    const line = buffer.subarray(0, end + 1);
    buffer = buffer.subarray(end + 1);
    // Configuration RPC may include native headers; publish only safe Go audit metadata.
    let configurationReply = false;
    try { const record = JSON.parse(line.toString('utf8')); configurationReply = record.type === 'response' && String(record.id || '').startsWith('factory-config-'); } catch {}
    if (!configurationReply) process.stdout.write(line);
    try {
      const record = JSON.parse(line.toString('utf8'));
      const metric = telemetry(record);
      if (metric) process.stdout.write(JSON.stringify({ type: 'factory.telemetry.v1', sequence: ++telemetrySequence, ...metric }) + '\n');
      if (record.type === 'response' && pending.has(record.id)) {
        const waiter = pending.get(record.id); pending.delete(record.id);
        if (record.success === false) waiter.reject(new Error(record.error || 'Pi rejected input')); else waiter.resolve(record.data);
      }
      if (record.type === 'message_end' && record.message?.role === 'assistant') {
        lastAssistantStop = { reason: record.message.stopReason, error: record.message.errorMessage };
      }
      if (record.type === 'agent_settled') {
        if (common) void tool('assignment_get',{}).then(current=>{ if (['completed','failed'].includes(current.state)) {completed=true;finish();} }).catch(()=>{});
        busy = false;
        reportStatus('idle');
        if (!completed && !stopping && lastAssistantStop?.reason === 'length') {
          if (continuations >= continuationLimit) {
            failSession(`model response truncated (stopReason=length); continuation limit ${continuationLimit} exhausted`);
          } else {
            continuations++;
            console.error(`Factory agent recovery: model response truncated (stopReason=length); continuation ${continuations}/${continuationLimit}`);
            send('Your preceding response was truncated by the model output limit. Continue from completed work. Do not repeat the analysis or redesign the presentation. Take the next concrete action using tools, save incremental work, and publish it when ready. If blocked, report the actual blocker through Factory MCP.', false).catch(error => failSession(`continuation failed: ${error.message}`));
          }
        } else if (!completed && !stopping && ['error', 'aborted'].includes(lastAssistantStop?.reason)) {
          failSession(`model response ended with stopReason=${lastAssistantStop.reason}${lastAssistantStop.error ? ': ' + lastAssistantStop.error : ''}`);
        } else {
          finish();
        }
      }
    } catch { /* Preserve non-JSON output for diagnosis. */ }
  }
});
child.on('error', error => { console.error(`Pi session: ${error.message}`); process.exitCode = 1; });
const operatorInput = createInterface({ input: process.stdin, crlfDelay: Infinity });
operatorInput.on('line', line => { if (line.trim()) send(line).catch(error => console.error(`Operator delivery: ${error.message}`)); });
child.on('close', code => {
  stopping = true;
  operatorInput.close(); process.stdin.pause();
  if (buffer.length) process.stdout.write(buffer);
  for (const waiter of pending.values()) waiter.reject(new Error('Pi session ended')); pending.clear();
  client.close().catch(() => {});
  process.exitCode = code || (completed ? 0 : 1);
});
process.on('SIGTERM', () => { stopping = true; child.kill('SIGTERM'); });
process.on('SIGINT', () => { stopping = true; child.kill('SIGINT'); });
// Verify the actual launched session before sending any assignment/model prompt.
// Project-local settings or extension startup must not undo factory overrides.
{
  try {
    const expected = common ? Object.fromEntries(["provider","model","api","base_url","thinking"].filter(key=>assignment.configuration?.[key]).map(key=>[key,assignment.configuration?.[key]])) : JSON.parse(environment.FACTORY_PI_EXPECTED_CONFIG || '{}');
    const state = await new Promise((resolve, reject) => {
      const id = `factory-config-${++nextID}`;
      const timer = setTimeout(() => { pending.delete(id); reject(new Error('native configuration verification timed out')); }, 10000);
      pending.set(id, { resolve: data => { clearTimeout(timer); resolve(data); }, reject: () => { clearTimeout(timer); reject(new Error('native configuration verification failed')); } });
      child.stdin.write(JSON.stringify({ id, type: 'get_state' }) + '\n');
    });
    const actual = { provider: state?.model?.provider, model: state?.model?.id, api: state?.model?.api, base_url: state?.model?.baseUrl, thinking: state?.thinkingLevel };
    for (const [field, value] of Object.entries(expected)) {
      if (value && actual[field] !== value) throw new Error(`native configuration differs from overrides.${field}`);
    }
  } catch {
    // A native error can include configuration or credentials; do not echo it.
    failSession('native configuration verification failed');
    throw new Error('Factory Pi configuration verification failed');
  }
}
await send(prompt, false);

// Read acknowledgment follows successful handoff to Pi. Retries retain the
// cursor; a timed-out read does not consume a message or alter workflow state.
async function tool(name, args) {
  const result = await client.callTool({ name, arguments: args });
  if (result.isError) {
    const error = new Error('Factory communication call was rejected');
    error.controlCode = result.structuredContent?.error?.code;
    throw error;
  }
  return result.structuredContent ?? JSON.parse(result.content.find(item => item.type === 'text').text);
}
let cursor = '';
let acknowledgment = '';
let failures = 0;
while (!stopping && !completed) {
  try {
    if (acknowledgment) {
      await tool('read_messages', { thread_id: threadID, after: cursor, ack_through: acknowledgment, limit: 1 });
      acknowledgment = '';
    }
    const batch = await tool('wait_for_message', { thread_id: threadID, after: cursor, wait_seconds: 20, limit: 50 });
    if (batch.closed) { stopping = true; child.kill('SIGTERM'); break; }
    for (const message of batch.messages) {
      // Historical or misrouted observations must not wake a model. The batch
      // cursor still advances and is acknowledged, so later messages flow.
      if (message.payload.kind === 'runtime_status') continue;
      if (message.sender === participantID) {
        if (!common && message.payload.kind === 'step_result') { completed = true; finish(); }
        continue;
      }
      await send(`Factory thread message ${message.id} from ${message.sender_name} (${message.sender}):\n${JSON.stringify(message.payload)}\nReply through the Factory MCP tools when needed.`);
    }
    cursor = batch.cursor; acknowledgment = cursor; failures = 0;
  } catch {
    if (stopping || completed) break;
    if (++failures >= 5) {
      console.error('Factory message delivery unavailable after five attempts; ending the session for visible recovery.');
      child.kill('SIGTERM'); break;
    }
    await new Promise(resolve => setTimeout(resolve, Math.min(1000 * 2 ** failures, 10000)));
  }
}
