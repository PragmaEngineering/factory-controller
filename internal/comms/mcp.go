package comms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type principalKey struct{}

func (s *Service) MCPHandler() http.Handler { return s.MCPHandlerForHost("") }

// MCPHandlerForHost accepts an explicitly advertised network authority.
func (s *Service) MCPHandlerForHost(authority string) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "factory-comms", Version: "0.1.0"}, nil)
	for _, name := range []string{"create_thread", "add_participant", "remove_participants", "list_participants", "send_message", "read_messages", "wait_for_message", "close_thread", "assignment_get", "assignment_status", "assignment_complete"} {
		name := name
		server.AddTool(&mcp.Tool{Name: name, Description: descriptions[name], InputSchema: schemas[name]}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			p, ok := ctx.Value(principalKey{}).(Participant)
			if !ok {
				return nil, fmt.Errorf("authenticated participant missing")
			}
			value, err := s.Call(ctx, p, name, request.Params.Arguments)
			if err != nil {
				var f *Fault
				if !errors.As(err, &f) {
					slog.ErrorContext(ctx, "communication operation failed", "tool", name, "participant", p.ID, "error", err)
				}
				return mcpFailure(err), nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: json.RawMessage(raw)}, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: authority != ""})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		// This local endpoint is for authenticated native clients, not browsers.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not accepted", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if authority != "" && r.Host != authority || authority == "" && (err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "credential required", http.StatusUnauthorized)
			return
		}
		p, err := s.Authenticate(r.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			http.Error(w, "invalid credential", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// Unknown failures are not necessarily transient. Retry advice must come
// from an explicitly classified Fault, not from a failed operation alone.
func mcpFailure(err error) *mcp.CallToolResult {
	code := "internal_error"
	message := "communication operation failed; inspect controller diagnostics before retrying"
	var f *Fault
	if errors.As(err, &f) {
		code, message = f.Code, f.Message
	}
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: json.RawMessage(raw)}
}

func (s *Service) Listen(ctx context.Context) (func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.URL = "http://" + listener.Addr().String() + "/mcp"
	server := &http.Server{Handler: s.MCPHandler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() { _ = server.Serve(listener); close(done) }()
	stop := func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()
	return stop, nil
}
func (s *Service) Call(ctx context.Context, p Participant, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "assignment_get", "assignment_status", "assignment_complete":
		return s.assignmentCall(ctx, p, name, raw)
	case "send_message":
		var a Send
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fault("invalid_argument", "invalid send payload")
		}
		// AddTool advertises schemas but does not validate them automatically.
		// Preserve precise data errors before checking the full raw envelope.
		validationErr := validateControlPayload(a.Payload)
		if validationErr == nil {
			var value any
			if json.Unmarshal(raw, &value) != nil || resolvedSendContract.Validate(value) != nil {
				validationErr = fault("invalid_argument", "send_message arguments must match the advertised envelope schema; correct field names, required values, and content-part shapes")
			}
		}
		if validationErr != nil {
			if a.Payload.Kind == "step_result" {
				if err := s.recordRejectedResult(ctx, p, a, validationErr); err != nil {
					slog.ErrorContext(ctx, "persist completion rejection", "participant", p.ID, "error", err)
				}
			}
			return nil, validationErr
		}
		return s.Send(ctx, p, a)
	case "create_thread":
		var a Create
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fault("invalid_argument", "invalid thread payload")
		}
		return s.Create(ctx, p, a)
	case "list_participants":
		var a Read
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fault("invalid_argument", "thread_id required")
		}
		participants, err := s.Participants(ctx, p, a.ThreadID)
		return map[string]any{"participants": participants}, err
	case "read_messages", "wait_for_message":
		var a Read
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fault("invalid_argument", "invalid read payload")
		}
		if name == "wait_for_message" {
			return s.Wait(ctx, p, a)
		}
		return s.Read(ctx, p, a)
	case "add_participant", "remove_participants", "close_thread":
		var a Membership
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fault("invalid_argument", "invalid membership payload")
		}
		return s.Membership(ctx, p, name, a)
	default:
		return nil, fault("invalid_argument", "unknown tool")
	}
}

const resultDataGuidance = `For step_result, normally omit artifacts: the host collects configured outputs. Additional artifacts must be objects, e.g. "artifacts":[{"name":"output.txt"}], never filename strings. The host supplies verified URIs and digests.`

var descriptions = map[string]string{
	"assignment_get":      "Read your authenticated assignment, input, configuration, instructions and contract. No ID is needed for the current assignment.",
	"assignment_status":   "Inspect your assignment or report running/waiting. This does not complete work.",
	"assignment_complete": "Submit one schema-validated output or error. Unchanged retries are idempotent. Accepted means durably recorded; workflow routing belongs to the owner.",
	"create_thread":       "Create a conversation among known participants in your execution. Does not launch workers.",
	"add_participant":     "Add an existing participant. Requires thread-owner or human/controller authority; does not start an agent.",
	"remove_participants": "Remove memberships atomically. Requires thread-owner or human/controller authority; does not kill processes.",
	"list_participants":   "Return {participants:[...]} with participant identities, assignments, and active/removed states. Discover peer and controller IDs here.",
	"send_message":        "Send addressed or broadcast content. payload has kind, version:1, and A2A 0.3 content parts. Text: {kind:text,text:...}. Structured: {kind:data,data:{...}}. Reserved runtime kind: runtime_status (state: busy or idle, emitted by adapters). Control kinds: publication (files: execution-files-relative paths), progress, input_request (prompt, options), input_response (request_id, answer), step_result (outcome, summary, publication_id when work was published). Control requests address controller; input requests also address human. Reuse operation_id only for unchanged uncertain retries; correct invalid_argument payloads before resubmitting. A result is complete only when effect.state is accepted. " + resultDataGuidance,
	"read_messages":       "Read all or unread messages without consuming them. after is a returned cursor. ack_through acknowledges a retained batch for yourself only. Returns messages, cursor, more, closed.",
	"wait_for_message":    "Wait up to 30 seconds for visible messages after a returned cursor. Does not acknowledge them. Timeout returns an empty batch; reconnect using the same cursor.",
	"close_thread":        "Close a conversation, preserving history. Requires thread-owner or human/controller authority. Does not complete an execution.",
}

func schema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": false}
}
func prop(kind string) map[string]string { return map[string]string{"type": kind} }
func stringsProp() map[string]any        { return map[string]any{"type": "array", "items": prop("string")} }

var schemas = map[string]any{
	"assignment_get":      schema([]string{}, map[string]any{"id": prop("string")}),
	"assignment_status":   schema([]string{}, map[string]any{"id": prop("string"), "state": map[string]any{"type": "string", "enum": []string{"running", "waiting"}}, "message": prop("string")}),
	"assignment_complete": map[string]any{"type": "object", "properties": map[string]any{"id": prop("string"), "output": map[string]any{}, "error": map[string]any{"type": "string", "minLength": 1}}, "oneOf": []any{map[string]any{"required": []string{"output"}, "not": map[string]any{"required": []string{"error"}}}, map[string]any{"required": []string{"error"}, "not": map[string]any{"required": []string{"output"}}}}, "additionalProperties": false},
	"create_thread":       schema([]string{"operation_id", "title", "context", "participants"}, map[string]any{"operation_id": prop("string"), "title": prop("string"), "context": map[string]any{"type": "array", "items": prop("object")}, "participants": stringsProp()}),
	"list_participants":   schema([]string{"thread_id"}, map[string]any{"thread_id": prop("string")}),
	"send_message":        schema([]string{"operation_id", "thread_id", "payload"}, map[string]any{"operation_id": prop("string"), "thread_id": prop("string"), "recipients": stringsProp(), "in_reply_to": prop("string"), "payload": payloadSchema()}),
	"read_messages":       schema([]string{"thread_id"}, map[string]any{"thread_id": prop("string"), "mode": prop("string"), "after": prop("string"), "limit": prop("integer"), "ack_through": prop("string")}),
	"wait_for_message":    schema([]string{"thread_id"}, map[string]any{"thread_id": prop("string"), "after": prop("string"), "limit": prop("integer"), "wait_seconds": prop("integer")}),
	"add_participant":     schema([]string{"operation_id", "thread_id", "participant_id"}, map[string]any{"operation_id": prop("string"), "thread_id": prop("string"), "participant_id": prop("string")}),
	"remove_participants": schema([]string{"operation_id", "thread_id", "participant_ids"}, map[string]any{"operation_id": prop("string"), "thread_id": prop("string"), "participant_ids": stringsProp()}),
	"close_thread":        schema([]string{"operation_id", "thread_id", "reason"}, map[string]any{"operation_id": prop("string"), "thread_id": prop("string"), "reason": prop("string")}),
}
