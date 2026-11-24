import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {mkdtempSync,writeFileSync,readFileSync,mkdirSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawn} from 'node:child_process';
import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StreamableHTTPServerTransport} from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import {z} from 'zod';

test('Pi reads assignments and completes over the common MCP API without Factory context',async t=>{
 const root=mkdtempSync(join(tmpdir(),'pi-assignment-'));t.after(()=>rmSync(root,{recursive:true,force:true}));
 let state='assigned';let completed;
 const server=createServer(async(req,res)=>{
  const mcp=new McpServer({name:'assignment',version:'1'});
  const respond=value=>({content:[{type:'text',text:JSON.stringify(value)}],structuredContent:value});
  mcp.registerTool('assignment_get',{inputSchema:z.object({})},async()=>respond({version:1,id:'assignment',thread_id:'thread',participant_id:'peer',instructions:'Inspect the supplied task.',input:{task:'readiness'},output_schema:{type:'object'},state}));
  mcp.registerTool('assignment_status',{inputSchema:z.object({id:z.string(),state:z.string()})},async args=>{
   if(state!=='completed')state=args.state;return respond({state});
  });
  mcp.registerTool('assignment_complete',{inputSchema:z.object({output:z.any()})},async args=>{completed=args.output;state='completed';return respond({state:'accepted'});});
  mcp.registerTool('wait_for_message',{inputSchema:z.any()},async()=>{await new Promise(r=>setTimeout(r,20));return respond({messages:[],cursor:'0',closed:false});});
  mcp.registerTool('read_messages',{inputSchema:z.any()},async()=>respond({messages:[],cursor:'0',closed:false}));
  const transport=new StreamableHTTPServerTransport({sessionIdGenerator:undefined});await mcp.connect(transport);
  res.on('close',()=>{transport.close();mcp.close();});await transport.handleRequest(req,res);
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));t.after(()=>{server.closeAllConnections();server.close();});
 const native=join(root,'native.mjs');
 writeFileSync(native,`#!/usr/bin/env node
import {createInterface} from 'node:readline';
import {readFileSync} from 'node:fs';
import {Client} from '${import.meta.resolve('@modelcontextprotocol/sdk/client/index.js')}';
import {StreamableHTTPClientTransport} from '${import.meta.resolve('@modelcontextprotocol/sdk/client/streamableHttp.js')}';
const config=JSON.parse(readFileSync(process.env.PI_CODING_AGENT_DIR+'/mcp.json','utf8'));
if(!config.mcpServers.agent || process.env.FACTORY_THREAD_ID || process.env.FACTORY_INPUTS_PATH)process.exit(9);
const client=new Client({name:'native',version:'1'});await client.connect(new StreamableHTTPClientTransport(new URL(process.env.AGENT_MCP_URL),{requestInit:{headers:{Authorization:'Bearer '+process.env.AGENT_MCP_TOKEN}}}));
for await(const line of createInterface({input:process.stdin})){
 const request=JSON.parse(line);
 if(request.type==='get_state') {console.log(JSON.stringify({type:'response',id:request.id,success:true,data:{model:{provider:'fixture',id:'fixture',contextWindow:32000}}}));continue;}
 if(!request.message.includes('assignment_complete'))process.exit(8);
 console.log(JSON.stringify({type:'response',id:request.id,success:true}));
 const result=await client.callTool({name:'assignment_complete',arguments:{output:{outcome:'completed',summary:'done'}}});
 if(result.isError)process.exit(7);
 console.log(JSON.stringify({type:'agent_settled'}));
}
await client.close();
`,{mode:0o700});
 const profile=join(root,'profile');mkdirSync(profile);
 const env=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('FACTORY_')&&!key.startsWith('FABRIC_')&&!key.startsWith('AGENT_MCP_')));
 Object.assign(env,{AGENT_MCP_URL:`http://127.0.0.1:${server.address().port}/mcp`,AGENT_MCP_TOKEN:'scoped-credential',AGENT_EXECUTABLE:native,PI_CODING_AGENT_DIR:profile});
 const child=spawn(process.execPath,[join(import.meta.dirname,'pi-rpc-session.mjs')],{env,stdio:['ignore','pipe','pipe']});
 t.after(()=>child.kill('SIGKILL'));let error='';child.stderr.on('data',v=>error+=v);child.stdout.resume();
 const timer=setTimeout(()=>child.kill('SIGKILL'),8000);
 const code=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('close',resolve);});clearTimeout(timer);
 assert.equal(code,0,error);assert.equal(completed.outcome,'completed');
 const settings=JSON.parse(readFileSync(join(profile,'mcp.json'),'utf8'));
 assert.equal(settings.mcpServers.agent.headers.Authorization,'Bearer ${AGENT_MCP_TOKEN}');
});
