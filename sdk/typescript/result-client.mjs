// Minimal runner-neutral completion client. Packages still own their work;
// this helper only discovers the controller and submits the common MCP result.
import { fileURLToPath } from 'node:url';
import { readFile } from 'node:fs/promises';

export async function connect(environment, name = 'factory-package') {
  let Client, StreamableHTTPClientTransport;
  try {
    ({ Client } = await import('@modelcontextprotocol/sdk/client/index.js'));
    ({ StreamableHTTPClientTransport } = await import('@modelcontextprotocol/sdk/client/streamableHttp.js'));
  } catch (error) {
    if (error.code !== 'ERR_MODULE_NOT_FOUND') throw error;
    throw new Error(`Factory MCP dependency missing; run npm ci --prefix "${fileURLToPath(new URL('../../', import.meta.url))}"`);
  }
  const client = new Client({ name, version: '0.1.0' });
  try {
    await client.connect(new StreamableHTTPClientTransport(new URL(environment.AGENT_MCP_URL ?? environment.FACTORY_MCP_URL), {
      requestInit: { headers: { Authorization: `Bearer ${environment.AGENT_MCP_TOKEN ?? environment.FACTORY_MCP_TOKEN}` } },
    }));
    return client;
  } catch (error) {
    await client.close().catch(() => {});
    throw error;
  }
}

export async function withController(environment, run) {
  const client = await connect(environment);
  try {
    const participants = await tool(client, 'list_participants', { thread_id: environment.FACTORY_THREAD_ID });
    const controllers = participants.participants?.filter(participant => participant.kind === 'controller');
    if (controllers?.length !== 1) throw new Error('Assigned thread must have one controller');
    return await run(async result => {
      const receipt = await tool(client, 'send_message', {
        operation_id: `${environment.FACTORY_PARTICIPANT_ID}:result`,
        thread_id: environment.FACTORY_THREAD_ID, recipients: [controllers[0].id],
        payload: { kind: 'step_result', version: 1, parts: [{ kind: 'data', data: result }] },
      });
      if (receipt.effect?.state !== 'accepted') throw new Error('Controller did not accept the package result');
      return receipt;
    });
  } finally { await client.close(); }
}

async function tool(client, name, args) {
  const result = await client.callTool({ name, arguments: args });
  // Do not print server responses that may contain supplied task data.
  if (result.isError) throw new Error(`Factory MCP ${name} rejected the request; inspect the execution messages`);
  if (result.structuredContent) return result.structuredContent;
  return JSON.parse(result.content.find(part => part.type === 'text')?.text ?? '{}');
}

/** Common contract: bootstrap connection only; assignment data comes from MCP. */
export async function withAssignment(environment, run) {
  if (!environment.AGENT_MCP_URL) {
    // Explicit legacy runner compatibility: old Factory packages remain supported.
    return withController(environment, async submit => {
      const input = JSON.parse(await readFile(environment.FACTORY_INPUTS_PATH, 'utf8'));
      const configuration = JSON.parse(await readFile(environment.FACTORY_PACKAGE_CONFIG, 'utf8'));
      return run({version:1,input,configuration}, submit);
    });
  }
  const client = await connect(environment, 'agent-assignment');
  try {
    const assignment = await tool(client, 'assignment_get', {});
    const complete = async output => {
      const receipt = await tool(client, 'assignment_complete', {id:assignment.id,output});
      if (receipt.state !== 'accepted') throw new Error('Assignment completion was not accepted');
      return receipt;
    };
    await tool(client,'assignment_status',{id:assignment.id,state:'running'});
    return await run(assignment, complete, client);
  } finally { await client.close(); }
}
