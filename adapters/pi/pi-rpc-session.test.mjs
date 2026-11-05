import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, rmSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'node:http';
import { pathToFileURL } from 'node:url';
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { z } from 'zod';

const moduleURL = file => import.meta.resolve(`@modelcontextprotocol/sdk/${file}`);

async function runSession(t, scenario, options = {}) {
  const root = mkdtempSync(join(tmpdir(), 'factory-pi-mcp-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const messages = [];
  const lifecycle = [];
  let repairSent = false;
  let rejectedResults = 0;
  const profile = join(root, 'profile');
  const { mkdirSync } = await import('node:fs');
  mkdirSync(profile);
  writeFileSync(join(profile, 'mcp.json'), JSON.stringify({ mcpServers: { gopls: { command: 'gopls' } } }));
  const http = createServer(async (req, res) => {
    if (req.headers.authorization !== 'Bearer test-secret') { res.writeHead(401).end(); return; }
    const server = new McpServer({ name: 'fixture', version: '1' });
    const respond = value => ({ content: [{ type: 'text', text: JSON.stringify(value) }], structuredContent: value });
    server.registerTool('list_participants', { inputSchema: z.object({ thread_id: z.string() }) }, async () => respond({participants:[{id:'controller',kind:'controller'}]}));
    server.registerTool('read_messages' , { inputSchema: z.object({ thread_id: z.string(), after: z.string().optional(), ack_through: z.string().optional(), limit: z.number().optional() }) }, async () => respond({ messages: [], cursor: '0', more: false, closed: false }));
    server.registerTool('wait_for_message', { inputSchema: z.object({ thread_id: z.string(), after: z.string().optional(), wait_seconds: z.number().optional(), limit: z.number().optional() }) }, async args => {
      await new Promise(resolve => setTimeout(resolve, 20));
      const selected = messages.filter(item => item.sequence > Number(args.after || 0));
      return respond({ messages: selected, cursor: String(selected.at(-1)?.sequence || args.after || 0), more: false, closed: false });
    });
    server.registerTool('send_message', { inputSchema: z.object({ operation_id: z.string(), thread_id: z.string(), recipients: z.array(z.string()).optional(), payload: z.any() }) }, async args => {
      const data = args.payload.parts?.find(part => part.kind === 'data')?.data;
      if (args.payload.kind === 'step_result' && data?.artifacts?.some(item => typeof item === 'string')) {
        rejectedResults++;
        return { isError: true, content: [{ type: 'text', text: 'step_result.artifacts must be objects; normally omit artifacts' }], structuredContent: { error: { code: 'invalid_argument', message: 'artifacts must be objects' } } };
      }
      if (args.payload.kind === 'runtime_status') {
        assert.deepEqual(args.recipients, ['controller']);
        lifecycle.push(data.state);
        if (messages.some(item => item.payload.kind === 'step_result')) {
          return { isError: true, content: [{ type: 'text', text: 'attempt already submitted its result' }], structuredContent: { error: { code: 'already_completed', message: 'attempt already submitted its result' } } };
        }
        if (scenario === 'completion-repair' && data.state === 'idle' && !repairSent) {
          repairSent = true;
          messages.push({ id: 'controller-repair', sequence: messages.length + 1, sender: 'controller', sender_name: 'controller', payload: { kind: 'message', version: 1, parts: [{ kind: 'text', text: 'Your step_result was rejected. Omit artifacts, use a new operation_id, and resubmit. Do not repeat completed implementation or checks.' }] } });
        }
      }
      messages.push({ id: args.payload.kind === 'step_result' ? 'accepted-result' : args.operation_id, sequence: messages.length + 1, sender: 'worker', sender_name: 'worker', payload: args.payload });
      return respond({ message_id: 'accepted-result', state: 'stored', effect: { state: 'accepted' } });
    });
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
    await server.connect(transport);
    res.on('close', () => { transport.close(); server.close(); });
    await transport.handleRequest(req, res);
  });
  await new Promise(resolve => http.listen(0, '127.0.0.1', resolve));
  t.after(() => { http.closeAllConnections(); http.close(); });
  const url = `http://127.0.0.1:${http.address().port}/mcp`;
  const fake = join(root, 'fake-pi.mjs');
  writeFileSync(fake, `#!/usr/bin/env node
import { createInterface } from 'node:readline';
import { writeFileSync } from 'node:fs';
import { Client } from '${moduleURL('client/index.js')}';
import { StreamableHTTPClientTransport } from '${moduleURL('client/streamableHttp.js')}';
const client = new Client({name:'fake-pi',version:'1'});
await client.connect(new StreamableHTTPClientTransport(new URL(process.env.FACTORY_MCP_URL),{requestInit:{headers:{Authorization:'Bearer '+process.env.FACTORY_MCP_TOKEN}}}));
let turns=0;
for await (const line of createInterface({input:process.stdin})) {
  turns++;
  const request=JSON.parse(line);
  console.log(JSON.stringify({type:'response',command:'prompt',success:true,id:request.id}));
  console.log(JSON.stringify({type:'tool_execution_start',input:request.message,mode:request.streamingBehavior || 'initial',cwd:process.cwd()}));
  if (${JSON.stringify(Boolean(options.workspaceFiles))}) {
    writeFileSync('source.txt','source edit');
    writeFileSync(${JSON.stringify(options.workspaceFiles || '')}+'/review.md','review evidence');
  }
  const scenario=${JSON.stringify(scenario)};
  if(scenario==='exhausted' || scenario==='no-recovery' || (scenario==='recover' && turns===1)) {
    console.log(JSON.stringify({type:'message_end',message:{role:'assistant',stopReason:'length',usage:{output:16384}}}));
  } else if(scenario==='error' || scenario==='aborted') {
    console.log(JSON.stringify({type:'message_end',message:{role:'assistant',stopReason:scenario,errorMessage:'fixture model failure'}}));
  }
  if(scenario==='completion-repair' && turns===1) {
    const rejected=await client.callTool({name:'send_message',arguments:{operation_id:'invalid-final',thread_id:'thread',payload:{kind:'step_result',version:1,parts:[{kind:'data',data:{outcome:'ready',summary:'done',artifacts:['reviews/build.md']}}]}}});
    if(!rejected.isError) process.exit(3);
  }
  if((scenario==='completion-repair' && request.message.includes('controller-repair')) || (scenario==='peer' && request.message.startsWith('Factory thread message')) || (scenario==='recover' && turns===2)) {
    const result=await client.callTool({name:'send_message',arguments:{operation_id:'final',thread_id:'thread',payload:{kind:'step_result',version:1,parts:[{kind:'data',data:{outcome:'ready',summary:'done'}}]}}});
    if(result.isError) process.exit(2);
  }
  console.log(JSON.stringify({type:'agent_settled'}));
}
await client.close();
`, { mode: 0o700 });
  const promptPath = join(root, 'prompt.md');
  writeFileSync(promptPath, 'initial work');
  const args = options.promptFile ? [] : ['-p', 'initial work'];
  const child = spawn(process.execPath, [join(import.meta.dirname, 'pi-rpc-session.mjs'), ...args], {
    cwd: options.workingDirectory,
    env: { ...process.env, ...(options.promptFile ? { FACTORY_PROMPT_PATH: promptPath } : {}), AGENT_EXECUTABLE: fake, PI_CODING_AGENT_DIR: profile, FACTORY_MCP_URL: url, FACTORY_MCP_TOKEN: 'test-secret', FACTORY_THREAD_ID: 'thread', FACTORY_PARTICIPANT_ID: 'worker', FACTORY_COMMS_SKILL: root, FACTORY_PI_CONTINUATION_LIMIT: scenario === 'no-recovery' ? '0' : '2' },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  t.after(() => child.kill('SIGKILL'));
  let output = '', error = '', injected = false;
  child.stdout.on('data', chunk => {
    output += chunk;
    if (scenario === 'peer' && !injected && output.includes('"type":"agent_settled"')) {
      injected = true;
      child.stdin.write('operator feedback\n');
      messages.push({ id: 'peer-review', sequence: 1, sender: 'reviewer', sender_name: 'reviewer', payload: { kind: 'message', version: 1, parts: [{ kind: 'text', text: 'Review findings' }] } });
    }
  });
  child.stderr.on('data', chunk => { error += chunk; });
  const code = await new Promise(resolve => child.on('close', resolve));
  const records = output.trim().split('\n').map(JSON.parse);
  const turns = records.filter(item => item.type === 'tool_execution_start');
  if (options.workingDirectory) {
    assert.equal(turns[0].cwd, realpathSync(options.workingDirectory));
    assert(turns[0].input.includes('Source workspace (actual process working directory): '+JSON.stringify(realpathSync(options.workingDirectory))));
  }
  if (['peer', 'recover', 'completion-repair'].includes(scenario)) {
    assert.equal(code, 0, error);
    assert(messages.some(item => item.id === 'accepted-result'));
  } else {
    assert.equal(code, 1, error);
    assert.match(error, /Factory agent blocked:/);
  }
  assert.equal(lifecycle[0], 'busy');
  if (['peer', 'recover', 'completion-repair'].includes(scenario)) assert(lifecycle.includes('idle'), 'normal settled state must be reported through MCP');
  if (scenario === 'completion-repair') {
    assert.equal(rejectedResults, 1);
    assert.equal(turns.length, 2, 'repair wakes exactly one new model turn');
    assert.match(turns[1].input, /controller-repair/);
    assert.match(turns[1].input, /Do not repeat completed implementation/);
    assert.equal(messages.filter(item => item.payload.kind === 'step_result').length, 1);
    assert.deepEqual(lifecycle.slice(0, 3), ['busy', 'idle', 'busy']);
  } else if (scenario === 'peer') {
    assert(records.some(item => item.input?.startsWith('initial work')));
    assert(records.some(item => item.input === 'operator feedback'));
    assert(records.some(item => item.input?.includes('peer-review')));
    assert.doesNotMatch(error, /recovery|blocked/);
  } else if (scenario === 'recover') {
    assert.equal(turns.length, 2);
    assert.match(error, /continuation 1\/2/);
    assert.match(turns[1].input, /preceding response was truncated/);
    assert.equal(turns[1].mode, 'initial');
  } else if (scenario === 'exhausted') {
    assert.equal(turns.length, 3, 'initial turn plus exactly two continuations');
    assert.match(error, /continuation limit 2 exhausted/);
  } else if (scenario === 'no-recovery') {
    assert.equal(turns.length, 1);
    assert.match(error, /continuation limit 0 exhausted/);
  } else {
    assert.equal(turns.length, 1, 'model errors must not become silent waiting or unbounded retries');
    assert.match(error, new RegExp('stopReason=' + scenario));
    assert.match(error, /fixture model failure/);
  }
  const config = JSON.parse(readFileSync(join(profile, 'mcp.json')));
  assert.equal(config.mcpServers.gopls.command, 'gopls');
  assert.equal(config.mcpServers.factory.headers.Authorization, 'Bearer ${FACTORY_MCP_TOKEN}');
  assert(!readFileSync(join(profile, 'mcp.json'), 'utf8').includes('test-secret'));
  assert.equal(config.mcpServers.factory.toolExposure.wait_for_message, 'hidden');
  if (process.env.PI_SKILL_LOADER) {
    const { validateMcpServerConfig, getMcpToolExposure } = await import(pathToFileURL(process.env.PI_SKILL_LOADER.replace(/skills\.js$/, 'mcp-servers.js')).href);
    const native = validateMcpServerConfig('factory', config.mcpServers.factory);
    assert.equal(typeof native, 'object', String(native));
    for (const name of ['list_participants', 'send_message', 'read_messages']) assert.equal(getMcpToolExposure(native, name), 'direct');
    assert.equal(getMcpToolExposure(native, 'wait_for_message'), 'hidden');
    assert.equal(getMcpToolExposure(native, 'create_thread'), 'codemode');
  }
  for (const tool of ['list_participants','send_message','read_messages']) assert.equal(config.mcpServers.factory.toolExposure[tool], 'direct');
}

for (const [scenario, description] of Object.entries({
  'completion-repair': 'rejected completion is repaired after a controller message wakes the idle agent',
  peer: 'idle Pi stays available, receives peer input, and finishes through MCP',
  recover: 'truncated response continues and completes through MCP',
  'no-recovery': 'zero continuation budget blocks on first truncation',
  exhausted: 'repeated truncation ends visibly after bounded continuations',
  error: 'provider error ends the session visibly',
  aborted: 'aborted model turn does not silently wait',
})) {
  test(description, { timeout: 15000 }, t => runSession(t, scenario));
}

test("managed Pi package reads its Factory prompt file without Pi-specific factory arguments", async t => { await runSession(t, "recover", { promptFile: true }); });

test('native source context follows launcher cwd while evidence uses execution storage', async t => {
  const root=mkdtempSync(join(tmpdir(),'factory-workspace-'));
  t.after(()=>rmSync(root,{recursive:true,force:true}));
  const {mkdirSync,existsSync}=await import('node:fs');
  const workspace=join(root,'selected source');mkdirSync(workspace);
  const files=join(root,'execution files');mkdirSync(files);
  await runSession(t,'recover',{workingDirectory:workspace,workspaceFiles:files});
  assert.equal(readFileSync(join(workspace,'source.txt'),'utf8'),'source edit');
  assert.equal(readFileSync(join(files,'review.md'),'utf8'),'review evidence');
  assert(!existsSync(join(root,'source.txt')));
  assert(!existsSync(join(workspace,'review.md')));
});
