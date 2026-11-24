import test from 'node:test';
import assert from 'node:assert/strict';
import { telemetry, createTelemetry } from './telemetry.mjs';

test('only final assistant usage is counted, with cache categories separate', () => {
 const message = { role: 'assistant', usage: { input: 13, cacheRead: 70, cacheWrite: 5, output: 12, cost: { total: 0 } } };
 assert.equal(telemetry({type:'message_update',message}), undefined);
 const event = telemetry({type:'message_end',message});
 assert.deepEqual(event.tokens, {input:13,cache_read:70,cache_write:5,output:12});
 assert.equal(event.cost_usd,undefined);
 message.usage.cost.total=0.01;
 assert.equal(telemetry({type:'message_end',message}).cost_usd,0.01);
 message.usage.input=-1;
 assert.equal(telemetry({type:'message_end',message}),undefined);
});
test('tools carry correlation IDs, command and actual failure state', () => {
 assert.deepEqual(telemetry({type:'tool_execution_start',toolCallId:'a',toolName:'bash',args:{command:'go test ./...'}}),{kind:'tool_started',tool_id:'a',tool_name:'bash',command:'go test ./...',arguments:JSON.stringify({command:'go test ./...'}),truncated:false});
 assert.deepEqual(telemetry({type:'tool_execution_end',toolCallId:'a',isError:true}),{kind:'tool_finished',tool_id:'a',failed:true,tool_name:'tool',output:'',error:'',truncated:false});
});

test('context limits follow actual provider/model and never use cumulative usage', () => {
 const translate = createTelemetry();
 translate({type:'response',success:true,data:{model:{id:'m',provider:'p',contextWindow:1000,headers:{Authorization:'private'}}}});
 const record={type:'message_end',message:{role:'assistant',provider:'p',model:'m',stopReason:'stop',usage:{input:100,cacheRead:200,output:50}}};
 assert.equal(translate(record).context_window,1000);
 assert.equal(translate(record).tokens.input,100);
 record.message.model='other';
 assert.equal(translate(record).context_window,undefined);
 assert.deepEqual(translate({type:'turn_start'}),{kind:'turn_started',turn:1});
 assert.deepEqual(translate({type:'turn_end'}),{kind:'turn_finished',turn:1});
});
test('failure evidence includes text, details, exit code and explicit capture limits', () => {
 const event=telemetry({type:'tool_execution_end',toolCallId:'bad',toolName:'bash',isError:true,result:{content:[{type:'text',text:'command failed <script>'},{type:'image',data:'omitted'}],details:{exitCode:2,token:'private',stderr:'x'.repeat(20000)}}});
 assert.equal(event.exit_code,2);
 assert.equal(event.output,'command failed <script>');
 assert.equal(event.truncated,true);
 assert(!event.error.includes('private'));
 assert(event.error.includes('[redacted]'));
 assert.equal(event.error.length,16384);
 assert.deepEqual(telemetry({type:'tool_execution_end',toolCallId:'ok',isError:false}),{kind:'tool_finished',tool_id:'ok',failed:false});
});

test('native compaction events retain the reason and explicitly estimated context reduction', () => {
 assert.deepEqual(telemetry({type:'compaction_start',reason:'overflow'}),{kind:'compaction_started',reason:'overflow'});
 assert.deepEqual(telemetry({type:'compaction_end',reason:'overflow',aborted:true,errorMessage:'failed',result:{tokensBefore:1000,estimatedTokensAfter:200}}),{kind:'compaction_finished',reason:'overflow',aborted:true,error:'failed',tokens_before:1000,estimated_tokens_after:200});
});

test('structured secrets do not reappear in the fallback command label', () => {
 const event=telemetry({type:'tool_execution_start',toolCallId:'a',toolName:'http',args:{url:'https://example.test',authorization:'private'}});
 assert(!JSON.stringify(event).includes('private'));
 assert(event.arguments.includes('[redacted]'));
});
