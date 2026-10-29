package comms_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"controls/execution"
	"controls/internal/artifacts"
	"controls/internal/comms"
	"controls/internal/testdb"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type credentials struct{ token string }

func (a credentials) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(r)
}
func connect(t *testing.T, ctx context.Context, c comms.Connection) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.URL, HTTPClient: &http.Client{Transport: credentials{c.Token}}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func call(t *testing.T, ctx context.Context, client *mcp.ClientSession, name string, args any) json.RawMessage {
	t.Helper()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, result.IsError, fmt.Sprint(result.Content))
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	return raw
}
func TestDurableConversations(t *testing.T) {
	db := testdb.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service, err := comms.New(ctx, db, artifacts.HostCollector{Root: t.TempDir()})
	require.NoError(t, err)
	stop, err := service.Listen(ctx)
	require.NoError(t, err)
	t.Cleanup(stop)
	pair := func(t *testing.T) (execution.Work, comms.Connection, comms.Participant, comms.Connection, comms.Participant) {
		id := uuid.NewString()
		project := t.TempDir()
		work := execution.Work{ExecutionID: id, Project: project, Step: execution.Step{ID: "author", Stage: "design", Outputs: []string{"spec.html"}, OutputsOn: []execution.Outcome{"ready"}, Routes: map[execution.Outcome]execution.StepID{"ready": execution.Complete, "blocked": execution.Fail}}, Package: execution.PackageSnapshot{Name: "author"}, Visit: 1, Try: 1, Peers: []execution.Peer{{Step: "author", Name: "author", Visit: 1, Try: 1}, {Step: "reviewer", Name: "reviewer", Visit: 1, Try: 1}}}
		a, err := service.Provision(ctx, work)
		require.NoError(t, err)
		author, err := service.Authenticate(ctx, a.Token)
		require.NoError(t, err)
		peerWork := work
		peerWork.Step.ID = "reviewer"
		peerWork.Package.Name = "reviewer"
		b, err := service.Provision(ctx, peerWork)
		require.NoError(t, err)
		reviewer, err := service.Authenticate(ctx, b.Token)
		require.NoError(t, err)
		require.Equal(t, a.ThreadID, b.ThreadID)
		return work, a, author, b, reviewer
	}

	t.Run("reserve a whole peer group before starting members", func(t *testing.T) {
		service.Capacity = 2
		defer func() { service.Capacity = 0 }()
		work, a, author, _, reviewer := pair(t)
		other := work
		other.ExecutionID = uuid.NewString()
		_, err := service.Provision(ctx, other)
		require.ErrorContains(t, err, "capacity_unavailable")
		var count int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM factory.comms_threads WHERE execution_id=$1`, other.ExecutionID).Scan(&count))
		require.Zero(t, count)
		_, err = service.Send(ctx, author, comms.Send{OperationID: "blocked", ThreadID: a.ThreadID, Payload: comms.Structured("step_result", map[string]any{"outcome": "blocked", "summary": "done for capacity fixture"})})
		require.NoError(t, err)
		service.Capacity = 1
		restored, err := service.Provision(ctx, work)
		require.NoError(t, err)
		require.NotNil(t, restored.Result, "accepted replay does not need another slot")
		service.Capacity = 2
		require.NoError(t, service.End(ctx, author.ID))
		require.NoError(t, service.End(ctx, reviewer.ID))
		admitted, err := service.Provision(ctx, other)
		require.NoError(t, err)
		require.True(t, admitted.New)
		participant, err := service.Authenticate(ctx, admitted.Token)
		require.NoError(t, err)
		members, err := service.Participants(ctx, participant, admitted.ThreadID)
		require.NoError(t, err)
		for _, member := range members {
			if member.Kind == "agent" {
				require.NoError(t, service.End(ctx, member.ID))
			}
		}
	})
	t.Run("SDK tools, addressed delivery, idempotency, and cursors", func(t *testing.T) {
		_, a, author, b, reviewer := pair(t)
		client := connect(t, ctx, a)
		peer := connect(t, ctx, b)
		tools, err := client.ListTools(ctx, nil)
		require.NoError(t, err)
		require.Len(t, tools.Tools, 8)
		raw := call(t, ctx, client, "list_participants", map[string]any{"thread_id": a.ThreadID})
		require.Contains(t, string(raw), "controller")
		if script := os.Getenv("FACTORY_MCP_INTEROP_SCRIPT"); script != "" {
			check, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			command := exec.CommandContext(check, "node", script)
			command.Env = append(os.Environ(), "FACTORY_TEST_MCP_URL="+a.URL, "FACTORY_TEST_MCP_TOKEN="+a.Token, "FACTORY_TEST_THREAD_ID="+a.ThreadID, "FACTORY_TEST_PARTICIPANT_ID="+a.ParticipantID)
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "TS_MCP_OK")
		}
		request := comms.Send{OperationID: "draft-1", ThreadID: a.ThreadID, Recipients: []string{reviewer.ID}, Payload: comms.Structured("message", map[string]any{"draft": "first"})}
		first := call(t, ctx, client, "send_message", request)
		second := call(t, ctx, client, "send_message", request)
		require.JSONEq(t, string(first), string(second))
		changed := request
		changed.Payload = comms.Structured("message", map[string]any{"draft": "second"})
		_, err = service.Send(ctx, author, changed)
		require.ErrorContains(t, err, "operation_conflict")
		batch, err := service.Read(ctx, reviewer, comms.Read{ThreadID: a.ThreadID, Mode: "unread"})
		require.NoError(t, err)
		require.Len(t, batch.Messages, 1)
		again, err := service.Read(ctx, reviewer, comms.Read{ThreadID: a.ThreadID, Mode: "unread"})
		require.NoError(t, err)
		require.Equal(t, batch.Messages[0].ID, again.Messages[0].ID)
		_, err = service.Read(ctx, author, comms.Read{ThreadID: a.ThreadID, After: batch.Cursor})
		require.ErrorContains(t, err, "not_authorized")
		raw = call(t, ctx, peer, "read_messages", comms.Read{ThreadID: a.ThreadID, Mode: "unread", AckThrough: batch.Cursor})
		var acked comms.Batch
		require.NoError(t, json.Unmarshal(raw, &acked))
		require.Empty(t, acked.Messages)
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			time.Sleep(50 * time.Millisecond)
			_, _ = service.Send(ctx, author, comms.Send{OperationID: "draft-2", ThreadID: a.ThreadID, Recipients: []string{reviewer.ID}, Payload: comms.Structured("message", map[string]any{"draft": "second"})})
		}()
		raw = call(t, ctx, peer, "wait_for_message", comms.Read{ThreadID: a.ThreadID, After: acked.Cursor, WaitSeconds: 2})
		var waited comms.Batch
		require.NoError(t, json.Unmarshal(raw, &waited))
		require.Len(t, waited.Messages, 1)
		<-sent
		_, other, _, _, _ := pair(t)
		outsider, err := service.Authenticate(ctx, other.Token)
		require.NoError(t, err)
		_, err = service.Read(ctx, outsider, comms.Read{ThreadID: a.ThreadID})
		require.ErrorContains(t, err, "not_authorized")
	})
	t.Run("formal input, outcome-specific files, and replay", func(t *testing.T) {
		work, a, author, _, _ := pair(t)
		request, err := service.Send(ctx, author, comms.Send{OperationID: "question", ThreadID: a.ThreadID, Payload: comms.Structured("input_request", map[string]any{"prompt": "Which behavior?", "options": []string{"one", "two"}})})
		require.NoError(t, err)
		resultArgs := comms.Send{OperationID: "finish", ThreadID: a.ThreadID, Payload: comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "spec ready"})}
		_, err = service.Send(ctx, author, resultArgs)
		require.ErrorContains(t, err, "input_pending")
		_, err = service.Answer(ctx, request.MessageID, "invalid")
		require.ErrorContains(t, err, "invalid_argument")
		_, err = service.Answer(ctx, request.MessageID, "one")
		require.NoError(t, err)
		_, err = service.Send(ctx, author, resultArgs)
		require.ErrorContains(t, err, "artifact_validation_failed")
		require.ErrorContains(t, err, "spec ready")
		require.NoError(t, os.WriteFile(filepath.Join(work.Project, "spec.html"), []byte("spec"), 0600))
		accepted, err := service.Send(ctx, author, resultArgs)
		require.NoError(t, err)
		require.Equal(t, "accepted", accepted.Effect.State)
		replay, err := service.Send(ctx, author, resultArgs)
		require.NoError(t, err)
		require.Equal(t, accepted, replay)
		rejected := resultArgs
		rejected.OperationID = "other-finish"
		_, err = service.Send(ctx, author, rejected)
		require.ErrorContains(t, err, "already_completed")
		reopened, err := comms.New(ctx, db, artifacts.HostCollector{Root: t.TempDir()})
		require.NoError(t, err)
		restored, err := reopened.Provision(ctx, work)
		require.NoError(t, err)
		require.False(t, restored.New)
		require.NotNil(t, restored.Result)
		require.Equal(t, execution.Outcome("ready"), restored.Result.Outcome)
		require.Len(t, restored.Result.Artifacts, 1)
		require.NoError(t, service.End(ctx, author.ID))
		_, err = service.Read(ctx, author, comms.Read{ThreadID: a.ThreadID})
		require.ErrorContains(t, err, "attempt_inactive")
		_, err = service.Participants(ctx, author, a.ThreadID)
		require.ErrorContains(t, err, "attempt_inactive")
		_, err = service.Send(ctx, author, resultArgs)
		require.NoError(t, err, "a fenced sender may recover its own receipt")
	})
	t.Run("blocked result preserves cause without success files", func(t *testing.T) {
		_, a, author, _, _ := pair(t)
		_, err := service.Send(ctx, author, comms.Send{OperationID: "blocked", ThreadID: a.ThreadID, Payload: comms.Structured("step_result", map[string]any{"outcome": "blocked", "summary": "Credential was not supplied"})})
		require.NoError(t, err)
		result, err := service.Result(ctx, author.ID)
		require.NoError(t, err)
		require.Equal(t, "Credential was not supplied", result.Summary)
		require.Empty(t, result.Artifacts)
	})
	t.Run("membership, private history, and closure", func(t *testing.T) {
		_, a, author, b, reviewer := pair(t)
		human := service.Operator(author.ExecutionID)
		made, err := service.Create(ctx, author, comms.Create{OperationID: "new", Title: "conversation", Participants: []string{human.ID}})
		require.NoError(t, err)
		_, err = service.Send(ctx, author, comms.Send{OperationID: "old", ThreadID: made.ThreadID, Payload: comms.Structured("message", map[string]any{"text": "old"})})
		require.NoError(t, err)
		args := comms.Membership{OperationID: "add", ThreadID: made.ThreadID, ParticipantID: reviewer.ID}
		stageArgs := args
		stageArgs.ThreadID = a.ThreadID
		_, err = service.Membership(ctx, author, "add_participant", stageArgs)
		require.ErrorContains(t, err, "not_authorized")
		_, err = service.Membership(ctx, author, "add_participant", args)
		require.NoError(t, err)
		_, err = service.Membership(ctx, human, "add_participant", args)
		require.NoError(t, err)
		batch, err := service.Read(ctx, reviewer, comms.Read{ThreadID: made.ThreadID})
		require.NoError(t, err)
		require.Empty(t, batch.Messages)
		_, err = service.Send(ctx, author, comms.Send{OperationID: "new-message", ThreadID: made.ThreadID, Payload: comms.Structured("message", map[string]any{"text": "new"})})
		require.NoError(t, err)
		batch, err = service.Read(ctx, reviewer, comms.Read{ThreadID: made.ThreadID})
		require.NoError(t, err)
		require.Len(t, batch.Messages, 1)
		_, err = service.Membership(ctx, human, "remove_participants", comms.Membership{OperationID: "remove", ThreadID: made.ThreadID, ParticipantIDs: []string{b.ParticipantID}})
		require.NoError(t, err)
		_, err = service.Read(ctx, reviewer, comms.Read{ThreadID: made.ThreadID})
		require.ErrorContains(t, err, "not_authorized")
		_, err = service.Membership(ctx, human, "close_thread", comms.Membership{OperationID: "close", ThreadID: made.ThreadID, Reason: "finished"})
		require.NoError(t, err)
		batch, err = service.Wait(ctx, author, comms.Read{ThreadID: made.ThreadID, After: batch.Cursor, WaitSeconds: 1})
		require.ErrorContains(t, err, "not_authorized", "cursor belongs to reviewer")
		batch, err = service.Read(ctx, author, comms.Read{ThreadID: made.ThreadID})
		require.NoError(t, err)
		require.True(t, batch.Closed)
		_, err = service.Send(ctx, author, comms.Send{OperationID: "after-close", ThreadID: made.ThreadID, Payload: comms.Structured("message", map[string]any{"text": "late"})})
		require.ErrorContains(t, err, "thread_closed")
	})

	t.Run("published revision remains stable and stale results are rejected", func(t *testing.T) {
		work, a, author, _, reviewer := pair(t)
		require.NoError(t, os.WriteFile(filepath.Join(work.Project, "spec.html"), []byte("revision one"), 0600))
		published, err := service.Send(ctx, author, comms.Send{OperationID: "publish-1", ThreadID: a.ThreadID, Payload: comms.Structured("publication", map[string]any{"files": []string{"spec.html"}})})
		require.NoError(t, err)
		batch, err := service.Read(ctx, reviewer, comms.Read{ThreadID: a.ThreadID})
		require.NoError(t, err)
		require.Equal(t, published.MessageID, batch.Thread.PublicationID)
		var bytes []byte
		var uri string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT body,uri FROM factory.comms_publications WHERE message_id=$1 AND name='spec.html'`, published.MessageID).Scan(&bytes, &uri))
		require.Equal(t, "revision one", string(bytes))
		require.NoError(t, os.WriteFile(filepath.Join(work.Project, "spec.html"), []byte("revision two"), 0600))
		retained, err := os.ReadFile(uri)
		require.NoError(t, err)
		require.Equal(t, "revision one", string(retained))
		args := comms.Send{OperationID: "ready", ThreadID: a.ThreadID, Payload: comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "Reviewed", "publication_id": published.MessageID})}
		_, err = service.Send(ctx, reviewer, args)
		require.ErrorContains(t, err, "stale_revision")
		next, err := service.Send(ctx, author, comms.Send{OperationID: "publish-2", ThreadID: a.ThreadID, Payload: comms.Structured("publication", map[string]any{"files": []string{"spec.html"}})})
		require.NoError(t, err)
		_, err = service.Send(ctx, reviewer, args)
		require.ErrorContains(t, err, "stale_revision")
		args.Payload = comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "Reviewed new publication", "publication_id": next.MessageID})
		_, err = service.Send(ctx, reviewer, args)
		require.NoError(t, err)
	})
	t.Run("concurrent appends have committed cursor order", func(t *testing.T) {
		_, a, author, _, _ := pair(t)
		var group sync.WaitGroup
		failures := make(chan error, 12)
		for i := range 12 {
			group.Add(1)
			go func() {
				defer group.Done()
				_, err := service.Send(ctx, author, comms.Send{OperationID: fmt.Sprint(i), ThreadID: a.ThreadID, Payload: comms.Structured("message", map[string]any{"n": i})})
				failures <- err
			}()
		}
		group.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		batch, err := service.Read(ctx, author, comms.Read{ThreadID: a.ThreadID})
		require.NoError(t, err)
		require.Len(t, batch.Messages, 12)
		for i, m := range batch.Messages {
			require.Equal(t, int64(i+1), m.Sequence)
		}
		_, err = service.Send(ctx, author, comms.Send{OperationID: "oversized", ThreadID: a.ThreadID, Payload: comms.Structured("message", map[string]any{"text": strings.Repeat("x", 65536)})})
		require.ErrorContains(t, err, "64 KiB")
	})
}
