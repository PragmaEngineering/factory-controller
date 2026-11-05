package commandrunner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"controls/adapters"
	"controls/adapters/pi"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/packagehash"
	"controls/internal/sessions"
	"github.com/stretchr/testify/require"
)

// Preparation and the real package entrypoint must agree. The native fixture
// reads the generated files and arguments; it never calls a model provider.
func TestManagedPiOverridesReachNativeProcess(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	work := fixture(t)
	profile := filepath.Join(work.Package.Source, "pi")
	require.NoError(t, os.Mkdir(profile, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(profile, "settings.json"), []byte(`{"defaultProvider":"original","defaultModel":"original-model"}`), 0600))
	native := filepath.Join(t.TempDir(), "pi.mjs")
	program := `#!` + node + `
import fs from 'node:fs';
import readline from 'node:readline';
if(process.argv.includes('--version')) { console.log('1.0.3'); process.exit(0); }
const profile=process.env.PI_CODING_AGENT_DIR;
const settings=JSON.parse(fs.readFileSync(profile+'/settings.json'));
const models=JSON.parse(fs.readFileSync(profile+'/models.json'));
const provider=settings.defaultProvider, model=settings.defaultModel;
const connection=models.providers[provider];
const selected={...connection.models.find(m=>m.id===model),provider,api:connection.api,baseUrl:connection.baseUrl};
if(process.env.TEST_LAUNCH_CAPTURE) {
 for(const [flag,value] of [['--provider',provider],['--model',model],['--thinking','medium']]) {
  if(process.argv[process.argv.indexOf(flag)+1]!==value) process.exit(3);
 }
 fs.writeFileSync(process.env.TEST_LAUNCH_CAPTURE,JSON.stringify({provider,model,profile,hasCredential:!!process.env.TEST_PROVIDER_KEY}));
 process.exit(0);
}
for await(const line of readline.createInterface({input:process.stdin})) {
 const request=JSON.parse(line);
 console.log(JSON.stringify({id:request.id,success:true,data:request.type==='get_state'?{model:selected,thinkingLevel:'medium'}:{models:[selected]}}));
}
`
	require.NoError(t, os.WriteFile(native, []byte(program), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(work.Package.Source, "run.sh"), []byte("#!/bin/sh\nexec \"$AGENT_EXECUTABLE\" \"$@\"\n"), 0700))
	work.Package.Runtime = &execution.NativeRuntime{Name: "pi", Version: "=1.0.3", Executable: native}
	work.Package.Settings = json.RawMessage(`{"entrypoint":"run.sh","overrides":{"provider":"corporate","model":"coding-v1","api":"openai-completions","base_url":"https://corporate.example/v1","api_key_env":"TEST_PROVIDER_KEY","thinking":"medium","model_config":{"reasoning":true,"context_window":32000,"max_tokens":4000}}}`)
	work.Package.Digest, err = packagehash.Directory(work.Package.Source)
	require.NoError(t, err)
	t.Setenv("TEST_PROVIDER_KEY", "fixture-private-key")
	capture := filepath.Join(t.TempDir(), "native.json")
	communication := &testCommunication{connection: comms.Connection{New: true, ThreadID: "thread", ParticipantID: "participant"}}
	host := &testSessionHost{launch: func(request sessions.Launch) {
		command := exec.Command(request.Command[0], request.Command[1:]...)
		command.Dir = request.Directory
		command.Env = append(os.Environ(), "TEST_LAUNCH_CAPTURE="+capture)
		for name, value := range request.Environment {
			command.Env = append(command.Env, name+"="+value)
		}
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		data, err := os.ReadFile(capture)
		require.NoError(t, err)
		require.Contains(t, string(data), `"hasCredential":true`)
		require.Contains(t, string(data), `"provider":"corporate"`)
		require.Contains(t, string(data), `"model":"coding-v1"`)
		require.Contains(t, string(data), request.Environment["PI_CODING_AGENT_DIR"])
		metadata, err := json.Marshal(request.Runtime)
		require.NoError(t, err)
		require.Contains(t, string(metadata), `"api_key_env":"TEST_PROVIDER_KEY"`)
		require.NotContains(t, string(metadata), "fixture-private-key")
		communication.result = &execution.StepResult{Outcome: "ready", Summary: "native launch checked"}
	}}
	_, err = (Runner{Adapters: adapters.Registry{"pi": pi.Adapter{}}, Sessions: host, Recorder: testRecorder{}, Comms: communication, Root: t.TempDir()}).Run(context.Background(), work)
	require.NoError(t, err)
	require.Equal(t, 1, host.calls)
	original, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	require.NoError(t, err)
	require.Contains(t, string(original), "original-model")
}
