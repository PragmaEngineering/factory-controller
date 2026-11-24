import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdtempSync, mkdirSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createServer} from 'node:http';
import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StreamableHTTPServerTransport} from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import {z} from 'zod';

test('concurrent adapters do not turn lifecycle observations into model prompts', {timeout:15000}, async t => {
  const root=mkdtempSync(join(tmpdir(),'factory-lifecycle-'));
  t.after(()=>rmSync(root,{recursive:true,force:true}));
  // Deliberately replay historical broadcast traffic. It must be acknowledged,
  // but never handed to either native session as a new prompt.
  const messages=[{id:'historical-status',sequence:1,sender:'old-worker',sender_name:'old-worker',audience:['author','reviewer'],payload:{kind:'runtime_status',version:1,parts:[{kind:'data',data:{state:'idle'}}]}}];
  const acknowledgments=[];
  const respond=value=>({content:[{type:'text',text:JSON.stringify(value)}],structuredContent:value});
  const server=createServer(async (request,response)=>{
    const identity=request.headers.authorization?.replace('Bearer ','');
    if(!['author','reviewer'].includes(identity)){response.writeHead(401).end();return;}
    const mcp=new McpServer({name:'fixture',version:'1'});
    mcp.registerTool('list_participants',{inputSchema:z.object({thread_id:z.string()})},async()=>respond({participants:[{id:'controller',kind:'controller'},{id:'author',kind:'agent'},{id:'reviewer',kind:'agent'}]}));
    mcp.registerTool('read_messages',{inputSchema:z.object({thread_id:z.string(),after:z.string().optional(),ack_through:z.string().optional(),limit:z.number().optional()})},async args=>{
      acknowledgments.push({identity,cursor:Number(args.ack_through)});
      return respond({messages:[],cursor:args.after||'0',more:false,closed:false});
    });
    mcp.registerTool('wait_for_message',{inputSchema:z.object({thread_id:z.string(),after:z.string().optional(),wait_seconds:z.number().optional(),limit:z.number().optional()})},async args=>{
      await new Promise(resolve=>setTimeout(resolve,20));
      const selected=messages.filter(message=>message.sequence>Number(args.after||0)&&(message.sender===identity||message.audience.includes(identity)));
      return respond({messages:selected,cursor:String(selected.at(-1)?.sequence||args.after||0),more:false,closed:false});
    });
    mcp.registerTool('send_message',{inputSchema:z.object({operation_id:z.string(),thread_id:z.string(),recipients:z.array(z.string()),payload:z.any()})},async args=>{
      if(args.payload.kind==='runtime_status')assert.deepEqual(args.recipients,['controller']);
      messages.push({id:args.operation_id,sequence:messages.length+1,sender:identity,sender_name:identity,audience:args.recipients,payload:args.payload});
      return respond({message_id:args.operation_id,state:'stored',effect:{state:'accepted'}});
    });
    const transport=new StreamableHTTPServerTransport({sessionIdGenerator:undefined});
    await mcp.connect(transport);
    response.on('close',()=>{transport.close();mcp.close();});
    await transport.handleRequest(request,response);
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  t.after(()=>{server.closeAllConnections();server.close();});
  const native=join(root,'native.mjs');
  writeFileSync(native,`#!/usr/bin/env node
import {createInterface} from 'node:readline';
import {Client} from '${import.meta.resolve('@modelcontextprotocol/sdk/client/index.js')}';
import {StreamableHTTPClientTransport} from '${import.meta.resolve('@modelcontextprotocol/sdk/client/streamableHttp.js')}';
const client=new Client({name:'fixture',version:'1'});
await client.connect(new StreamableHTTPClientTransport(new URL(process.env.FACTORY_MCP_URL),{requestInit:{headers:{Authorization:'Bearer '+process.env.FACTORY_MCP_TOKEN}}}));
let turns=0;
for await(const line of createInterface({input:process.stdin})) {
 const request=JSON.parse(line);
 if(request.type==='get_state') {console.log(JSON.stringify({type:'response',id:request.id,success:true,data:{}}));continue;}
 console.log(JSON.stringify({type:'response',command:'prompt',id:request.id,success:true}));
 console.log(JSON.stringify({type:'fixture_prompt',text:request.message}));
 const first=++turns===1;
 const recipient=first?(process.env.FACTORY_PARTICIPANT_ID==='author'?'reviewer':'author'):'controller';
 const payload=first?{kind:'message',version:1,parts:[{kind:'text',text:'Review this publication'}]}:{kind:'step_result',version:1,parts:[{kind:'data',data:{outcome:'completed',summary:'Peer feedback received'}}]};
 await client.callTool({name:'send_message',arguments:{operation_id:process.env.FACTORY_PARTICIPANT_ID+'-'+turns,thread_id:'thread',recipients:[recipient],payload}});
 console.log(JSON.stringify({type:'agent_settled'}));
}
await client.close();
`,{mode:0o700});
  const run=identity=>{
    const profile=join(root,identity);mkdirSync(profile);
    const child=spawn(process.execPath,[join(import.meta.dirname,'pi-rpc-session.mjs'),'-p','Initial assignment'],{env:{...process.env,AGENT_EXECUTABLE:native,PI_CODING_AGENT_DIR:profile,FACTORY_MCP_URL:`http://127.0.0.1:${server.address().port}/mcp`,FACTORY_MCP_TOKEN:identity,FACTORY_THREAD_ID:'thread',FACTORY_PARTICIPANT_ID:identity},stdio:['pipe','pipe','pipe']});
    t.after(()=>child.kill('SIGKILL'));
    let output='',error='';
    child.stdout.on('data',chunk=>{output+=chunk;});child.stderr.on('data',chunk=>{error+=chunk;});
    return new Promise(resolve=>child.on('close',code=>resolve({identity,code,output,error})));
  };
  const results=await Promise.all([run('author'),run('reviewer')]);
  for(const result of results){
    assert.equal(result.code,0,result.error);
    const prompts=result.output.trim().split('\n').map(JSON.parse).filter(record=>record.type==='fixture_prompt');
    assert.equal(prompts.length,2,'one assignment and one genuine peer message');
    assert(!prompts.some(prompt=>prompt.text.includes('historical-status')));
    assert(acknowledgments.some(ack=>ack.identity===result.identity&&ack.cursor>=1),'filtered traffic must advance acknowledgments');
  }
  assert.equal(messages.filter(message=>message.payload.kind==='step_result').length,2);
  const lifecycle=messages.filter(message=>message.payload.kind==='runtime_status'&&message.id!=='historical-status');
  assert(lifecycle.length>=4&&lifecycle.length<=8,'at most one busy/idle pair per actual turn');
});
