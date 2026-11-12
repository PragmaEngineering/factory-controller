package comms

import (
	"context"
	"controls/execution"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Assignment is provisioned by a trusted host; agents cannot select ownership or routing.
type Assignment struct {
	Version          int             `json:"version"`
	ID               string          `json:"id"`
	Owner            string          `json:"participant_id"`
	ThreadID         string          `json:"thread_id"`
	Recipient        string          `json:"recipient"`
	Mode             string          `json:"mode"`
	Capability       any             `json:"capability"`
	Input            any             `json:"input"`
	Configuration    any             `json:"configuration"`
	Instructions     string          `json:"instructions"`
	WorkingDirectory string          `json:"working_directory"`
	FilesDirectory   string          `json:"files_directory,omitempty"`
	OutputSchema     json.RawMessage `json:"output_schema"`
	Metadata         map[string]any  `json:"metadata,omitempty"`
}
type assignmentRecord struct {
	Definition Assignment      `json:"definition"`
	State      string          `json:"state"`
	Message    string          `json:"message,omitempty"`
	Sequence   int             `json:"sequence,omitempty"`
	Completion json.RawMessage `json:"completion,omitempty"`
	Receipt    *Receipt        `json:"receipt,omitempty"`
}

func assignmentValidate(schema json.RawMessage, value any) error {
	var definition jsonschema.Schema
	if err := json.Unmarshal(schema, &definition); err != nil {
		return fault("invalid_argument", "invalid assignment output schema")
	}
	resolved, err := definition.Resolve(nil)
	if err != nil {
		return fault("invalid_argument", "invalid assignment output schema")
	}
	if err = resolved.Validate(value); err != nil {
		return fault("invalid_argument", "assignment output does not match its contract")
	}
	return nil
}
func (s *Service) PutAssignment(ctx context.Context, a Assignment) error {
	if a.Version != 1 || a.ID == "" || a.Owner == "" || a.ThreadID == "" || a.Recipient == "" || (a.Mode != "factory" && a.Mode != "fabric") {
		return fault("invalid_argument", "invalid assignment definition")
	}
	var definition jsonschema.Schema
	if json.Unmarshal(a.OutputSchema, &definition) != nil {
		return fault("invalid_argument", "invalid output schema")
	}
	if _, err := definition.Resolve(nil); err != nil {
		return fault("invalid_argument", "invalid output schema")
	}
	var allowed bool
	err := s.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_participants p JOIN factory.comms_members m ON m.participant_id=p.id JOIN factory.comms_threads t ON t.id=m.thread_id WHERE p.id=$1 AND p.active AND t.id=$2 AND EXISTS(SELECT 1 FROM factory.comms_members recipient WHERE recipient.thread_id=t.id AND recipient.participant_id=$3))`, a.Owner, a.ThreadID, a.Recipient).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return fault("not_authorized", "assignment requires existing scoped thread participants")
	}
	record := assignmentRecord{Definition: a, State: "assigned"}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.DB.Pool.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES('agent_assignment',$1,$2) ON CONFLICT DO NOTHING`, a.Owner, raw)
	if err != nil {
		return err
	}
	current, err := json.Marshal(a)
	if err != nil {
		return err
	}
	var same bool
	if err = s.DB.Pool.QueryRow(ctx, `SELECT content->'definition' = $2::jsonb FROM factory.records WHERE kind='agent_assignment' AND id=$1`, a.Owner, current).Scan(&same); err != nil {
		return err
	}
	if !same {
		return fault("operation_conflict", "participant already owns a different immutable assignment")
	}

	return nil
}
func assignmentView(record assignmentRecord) map[string]any {
	raw, _ := json.Marshal(record.Definition)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	delete(value, "mode")
	delete(value, "recipient")
	value["state"] = record.State
	if record.Message != "" {
		value["message"] = record.Message
	}
	if len(record.Completion) > 0 {
		var completed map[string]any
		_ = json.Unmarshal(record.Completion, &completed)
		for key, v := range completed {
			value[key] = v
		}
	}
	return value
}

var assignmentContracts = map[string]*jsonschema.Resolved{
	"assignment_get":      resolveContract(schemas["assignment_get"]),
	"assignment_status":   resolveContract(schemas["assignment_status"]),
	"assignment_complete": resolveContract(schemas["assignment_complete"]),
}

func (s *Service) assignmentCall(ctx context.Context, p Participant, name string, raw json.RawMessage) (any, error) {
	var arguments any
	if json.Unmarshal(raw, &arguments) != nil || assignmentContracts[name].Validate(arguments) != nil {
		return nil, fault("invalid_argument", "assignment arguments do not match the MCP contract")
	}

	var args struct {
		ID      string          `json:"id"`
		State   string          `json:"state"`
		Message string          `json:"message"`
		Output  json.RawMessage `json:"output"`
		Error   string          `json:"error"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, fault("invalid_argument", "invalid assignment arguments")
	}
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockExecution(ctx, tx, "assignment:"+p.ID); err != nil {
		return nil, err
	}
	var record assignmentRecord
	if err = s.DB.Get(ctx, "agent_assignment", p.ID, &record); err != nil {
		return nil, fault("not_authorized", "no assignment for this participant")
	}
	a := record.Definition
	if args.ID != "" && args.ID != a.ID {
		return nil, fault("not_authorized", "assignment belongs to another participant")
	}
	if name == "assignment_get" {
		return assignmentView(record), nil
	}
	if name == "assignment_status" {
		if args.State != "" {
			if record.State == "completed" || record.State == "failed" {
				return nil, fault("already_completed", "assignment is terminal")
			}
			if args.State != "running" && args.State != "waiting" {
				return nil, fault("invalid_argument", "state must be running or waiting")
			}
			if a.Mode == "factory" && args.State != record.State {
				state := "idle"
				if args.State == "running" {
					state = "busy"
				}
				record.Sequence++
				if _, err = s.Send(ctx, p, Send{OperationID: fmt.Sprintf("assignment:%s:status:%d", a.ID, record.Sequence), ThreadID: a.ThreadID, Recipients: []string{a.Recipient}, Payload: Structured("runtime_status", map[string]any{"state": state})}); err != nil {
					return nil, err
				}
			}
			record.State = args.State
			record.Message = args.Message
		}
	} else {
		if (args.Output != nil) == (args.Error != "") || (args.Error != "" && strings.TrimSpace(args.Error) == "") {
			return nil, fault("invalid_argument", "provide exactly one output or nonempty error")
		}
		completion := map[string]any{}
		if args.Output != nil {
			var value any
			if json.Unmarshal(args.Output, &value) != nil {
				return nil, fault("invalid_argument", "invalid output")
			}
			if err = assignmentValidate(a.OutputSchema, value); err != nil {
				return nil, err
			}
			completion["output"] = value
		} else {
			completion["error"] = args.Error
		}
		completed, _ := json.Marshal(completion)
		if len(record.Completion) > 0 {
			var previous any
			_ = json.Unmarshal(record.Completion, &previous)
			canonical, _ := json.Marshal(previous)
			if string(canonical) != string(completed) {
				return nil, fault("operation_conflict", "assignment already completed with different content")
			}
			return map[string]any{"assignment_id": a.ID, "state": "accepted", "receipt": record.Receipt}, nil
		}
		var payload Payload
		if a.Mode == "factory" {
			var result map[string]any
			if args.Error != "" {
				payload = Structured("assignment.failed", map[string]any{"assignment_id": a.ID, "error": args.Error})
			} else if json.Unmarshal(args.Output, &result) != nil {
				return nil, fault("invalid_argument", "Factory assignment output must be a result object")
			}
			if args.Error == "" {
				payload = Structured("step_result", result)
			}
		} else {
			completion["invocation_id"] = a.ID
			payload = Structured("fabric.result", completion)
		}
		receipt, e := s.Send(ctx, p, Send{OperationID: "assignment:" + a.ID + ":completion", ThreadID: a.ThreadID, Recipients: []string{a.Recipient}, Payload: payload})
		if e != nil {
			return nil, e
		}
		if a.Mode == "factory" && args.Error == "" && (receipt.Effect == nil || receipt.Effect.State != "accepted") {
			return nil, fault("temporarily_unavailable", "assignment result has not been accepted")
		}
		record.Completion = completed
		record.Receipt = &receipt
		record.State = "completed"
		if args.Error != "" {
			record.State = "failed"
		}
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE factory.records SET content=$2 WHERE kind='agent_assignment' AND id=$1`, p.ID, encoded); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("persist assignment state: %w", err)
	}
	if name == "assignment_complete" {
		return map[string]any{"assignment_id": a.ID, "state": "accepted", "receipt": record.Receipt}, nil
	}
	return assignmentView(record), nil
}

// ControllerID identifies the existing workflow controller without agent-side discovery.
func ControllerID(executionID string) string { return identity(executionID + "/controller") }

// Factory's result contract remains explicit, even when delivered through common MCP.
func FactoryAssignmentOutputSchema(work execution.Work) json.RawMessage {
	raw, _ := json.Marshal(controlDataSchemas["step_result"])
	var schema map[string]any
	_ = json.Unmarshal(raw, &schema)
	outcomes := []string{}
	for outcome := range work.Step.Routes {
		outcomes = append(outcomes, string(outcome))
	}
	sort.Strings(outcomes)
	schema["properties"].(map[string]any)["outcome"] = map[string]any{"type": "string", "enum": outcomes}
	raw, _ = json.Marshal(schema)
	return raw
}
