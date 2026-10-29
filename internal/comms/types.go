// Package comms provides durable, execution-scoped conversations. It knows
// neither the workflow scheduler nor a particular agent runner.
package comms

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controls/execution"

	"github.com/a2aproject/a2a-go/a2a"
)

type Payload struct {
	Kind    string           `json:"kind"`
	Version int              `json:"version"`
	Parts   a2a.ContentParts `json:"parts"`
}
type Participant struct {
	Assignment  string `json:"assignment,omitempty"`
	ID          string `json:"id"`
	ExecutionID string `json:"execution_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Active      bool   `json:"active"`
	Removed     bool   `json:"removed,omitempty"`
}
type Thread struct {
	OwnerID       string           `json:"owner_id"`
	PublicationID string           `json:"publication_id,omitempty"`
	ID            string           `json:"id"`
	ExecutionID   string           `json:"execution_id"`
	Title         string           `json:"title"`
	Context       a2a.ContentParts `json:"context"`
	Closed        bool             `json:"closed"`
}
type Message struct {
	RecipientNames []string  `json:"recipient_names,omitempty"`
	ID             string    `json:"id"`
	ThreadID       string    `json:"thread_id"`
	Sequence       int64     `json:"sequence"`
	Sender         string    `json:"sender"`
	SenderName     string    `json:"sender_name"`
	Audience       []string  `json:"recipients"`
	InReplyTo      string    `json:"in_reply_to,omitempty"`
	Payload        Payload   `json:"payload"`
	At             time.Time `json:"at"`
}
type Effect struct {
	State    string `json:"state"`
	ResultID string `json:"result_id,omitempty"`
	Routing  string `json:"routing,omitempty"`
}
type Receipt struct {
	OperationID string  `json:"operation_id"`
	ThreadID    string  `json:"thread_id,omitempty"`
	MessageID   string  `json:"message_id,omitempty"`
	Sequence    int64   `json:"sequence,omitempty"`
	State       string  `json:"state"`
	Effect      *Effect `json:"effect,omitempty"`
}
type Batch struct {
	Thread   Thread    `json:"thread"`
	Messages []Message `json:"messages"`
	Cursor   string    `json:"cursor"`
	More     bool      `json:"more"`
	Closed   bool      `json:"closed"`
}
type Request struct {
	StepID      string    `json:"step_id,omitempty"`
	ID          string    `json:"id"`
	ExecutionID string    `json:"execution_id"`
	ThreadID    string    `json:"thread_id"`
	Owner       string    `json:"owner"`
	Prompt      string    `json:"prompt"`
	Options     []string  `json:"options"`
	State       string    `json:"state"`
	At          time.Time `json:"at"`
}
type PublishedFile struct {
	Artifact execution.Artifact
	Content  []byte
}

type Publisher interface {
	Publish(context.Context, execution.Work, []string, string) ([]PublishedFile, error)
}

type Connection struct {
	URL           string                `json:"url"`
	Token         string                `json:"-"`
	ParticipantID string                `json:"participant_id"`
	ThreadID      string                `json:"thread_id"`
	SessionID     string                `json:"session_id,omitempty"`
	New           bool                  `json:"-"`
	Result        *execution.StepResult `json:"result,omitempty"`
}
type Send struct {
	OperationID string   `json:"operation_id"`
	ThreadID    string   `json:"thread_id"`
	Recipients  []string `json:"recipients,omitempty"`
	InReplyTo   string   `json:"in_reply_to,omitempty"`
	Payload     Payload  `json:"payload"`
}
type Read struct {
	ThreadID    string `json:"thread_id"`
	Mode        string `json:"mode,omitempty"`
	After       string `json:"after,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	AckThrough  string `json:"ack_through,omitempty"`
	WaitSeconds int    `json:"wait_seconds,omitempty"`
}
type Create struct {
	OperationID  string           `json:"operation_id"`
	Title        string           `json:"title"`
	Context      a2a.ContentParts `json:"context"`
	Participants []string         `json:"participants"`
}
type Membership struct {
	OperationID    string   `json:"operation_id"`
	ThreadID       string   `json:"thread_id"`
	ParticipantID  string   `json:"participant_id,omitempty"`
	ParticipantIDs []string `json:"participant_ids,omitempty"`
	Reason         string   `json:"reason,omitempty"`
}
type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string                     { return f.Code + ": " + f.Message }
func fault(code, format string, args ...any) error { return &Fault{code, fmt.Sprintf(format, args...)} }
func dataOf(p Payload) (map[string]any, error) {
	var data map[string]any
	for _, part := range p.Parts {
		if d, ok := part.(a2a.DataPart); ok {
			if data != nil {
				return nil, fault("invalid_argument", "one data part is required")
			}
			data = d.Data
		}
	}
	if data == nil {
		return nil, fault("invalid_argument", "a structured data part is required")
	}
	return data, nil
}
func decodeData[T any](p Payload) (T, error) {
	var v T
	data, err := dataOf(p)
	if err != nil {
		return v, err
	}
	raw, err := json.Marshal(data)
	if err == nil {
		err = json.Unmarshal(raw, &v)
	}
	return v, err
}
func Structured(kind string, data any) Payload {
	raw, _ := json.Marshal(data)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	return Payload{Kind: kind, Version: 1, Parts: a2a.ContentParts{a2a.DataPart{Data: object}}}
}
