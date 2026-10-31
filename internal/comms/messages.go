package comms

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"controls/execution"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Send(ctx context.Context, p Participant, args Send) (Receipt, error) {
	return s.mutate(ctx, p, args.OperationID, struct {
		Tool string
		Args Send
	}{"send_message", args}, func(tx pgx.Tx) (Receipt, error) {
		t, err := member(ctx, tx, p, args.ThreadID, true)
		if err != nil {
			return Receipt{}, err
		}
		if args.Payload.Kind == "" || len(args.Payload.Parts) == 0 || args.Payload.Version != 1 {
			return Receipt{}, fault("invalid_argument", "payload requires kind, version 1, and content parts")
		}
		recipients := append([]string(nil), args.Recipients...)
		if args.Recipients != nil && len(args.Recipients) == 0 {
			return Receipt{}, fault("invalid_argument", "recipients cannot be empty")
		}
		if recipients == nil {
			rows, err := tx.Query(ctx, `SELECT participant_id FROM factory.comms_members WHERE thread_id=$1 AND NOT removed`, t.ID)
			if err != nil {
				return Receipt{}, err
			}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return Receipt{}, err
				}
				recipients = append(recipients, id)
			}
			rows.Close()
		}
		sort.Strings(recipients)
		for _, id := range recipients {
			var visible bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_members WHERE thread_id=$1 AND participant_id=$2 AND NOT removed)`, t.ID, id).Scan(&visible)
			if err != nil {
				return Receipt{}, err
			}
			if !visible {
				return Receipt{}, fault("not_authorized", "recipient is not a member")
			}
		}
		if args.InReplyTo != "" {
			var visible bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_messages m JOIN factory.comms_members b ON b.thread_id=m.thread_id WHERE m.id=$1 AND m.thread_id=$2 AND b.participant_id=$3 AND m.sequence>b.joined_sequence AND ($3=ANY(m.audience) OR m.sender=$3))`, args.InReplyTo, t.ID, p.ID).Scan(&visible)
			if err != nil {
				return Receipt{}, err
			}
			if !visible {
				return Receipt{}, fault("not_authorized", "reply target is not visible in this thread")
			}
		}
		m := Message{ID: uuid.NewString(), ThreadID: t.ID, Sender: p.ID, SenderName: p.Name, Audience: recipients, InReplyTo: args.InReplyTo, Payload: args.Payload, At: time.Now().UTC()}
		effect, err := s.applyPayload(ctx, tx, p, &m)
		if err != nil {
			return Receipt{}, err
		}
		if err = tx.QueryRow(ctx, `UPDATE factory.comms_threads SET next_sequence=next_sequence+1 WHERE id=$1 RETURNING next_sequence`, t.ID).Scan(&m.Sequence); err != nil {
			return Receipt{}, err
		}
		raw, err := json.Marshal(m.Payload)
		if err != nil {
			return Receipt{}, err
		}
		// Domain effects and the message share the same commit. Request rows refer
		// to message IDs with a deferred constraint so validation can precede append.
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_messages(id,thread_id,sequence,sender,audience,in_reply_to,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, m.ID, m.ThreadID, m.Sequence, m.Sender, m.Audience, m.InReplyTo, raw, m.At)
		if err != nil {
			return Receipt{}, err
		}
		if err = appendMessageActivity(ctx, tx, p, m); err != nil {
			return Receipt{}, err
		}
		return Receipt{ThreadID: t.ID, MessageID: m.ID, Sequence: m.Sequence, State: "stored", Effect: effect}, nil
	})
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (s *Service) applyPayload(ctx context.Context, tx pgx.Tx, p Participant, m *Message) (*Effect, error) {
	kind := m.Payload.Kind
	if kind == "message" || kind == "progress" {
		return nil, nil
	}
	if kind != "publication" && kind != "step_result" && kind != "input_request" && kind != "input_response" {
		return nil, nil
	}
	if !contains(m.Audience, identity(p.ExecutionID+"/controller")) {
		return nil, fault("invalid_argument", "control payload must address the controller")
	}
	switch kind {
	case "publication":
		return s.publish(ctx, tx, p, m)

	case "input_request":
		if p.Kind != "agent" {
			return nil, fault("not_authorized", "formal input requests require an assigned worker")
		}
		var data struct {
			Prompt  string   `json:"prompt"`
			Options []string `json:"options"`
		}
		data, err := decodeData[struct {
			Prompt  string   `json:"prompt"`
			Options []string `json:"options"`
		}](m.Payload)
		if err != nil {
			return nil, err
		}
		if data.Prompt == "" || len(data.Options) > 20 {
			return nil, fault("invalid_argument", "prompt and at most 20 options required")
		}
		if data.Options == nil {
			data.Options = []string{}
		}
		if !contains(m.Audience, identity(p.ExecutionID+"/human")) {
			return nil, fault("invalid_argument", "input request must include the human participant")
		}
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_requests(id,execution_id,thread_id,owner,prompt,options,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, m.ID, p.ExecutionID, m.ThreadID, p.ID, data.Prompt, data.Options, m.At)
		return &Effect{State: "accepted"}, err
	case "input_response":
		if p.Kind != "human" {
			return nil, fault("not_authorized", "input requires the human participant's answer")
		}
		data, err := decodeData[struct {
			RequestID string `json:"request_id"`
			Answer    string `json:"answer"`
		}](m.Payload)
		if err != nil {
			return nil, err
		}
		var owner, state string
		var options []string
		err = tx.QueryRow(ctx, `SELECT owner,state,options FROM factory.comms_requests WHERE id=$1 AND thread_id=$2 AND execution_id=$3`, data.RequestID, m.ThreadID, p.ExecutionID).Scan(&owner, &state, &options)
		if err != nil {
			return nil, fault("invalid_argument", "unknown input request")
		}
		if state != "pending" {
			return nil, fault("operation_conflict", "input is already %s", state)
		}
		if data.Answer == "" || (len(options) > 0 && !contains(options, data.Answer)) {
			return nil, fault("invalid_argument", "answer must match the request")
		}
		if !contains(m.Audience, owner) {
			return nil, fault("invalid_argument", "answer must address the request owner")
		}
		raw, _ := json.Marshal(m.Payload)
		_, err = tx.Exec(ctx, `UPDATE factory.comms_requests SET state='answered',response=$2 WHERE id=$1`, data.RequestID, raw)
		return &Effect{State: "accepted"}, err
	case "step_result":
		if p.Kind != "agent" {
			return nil, fault("not_authorized", "only the assigned worker can submit a result")
		}
		result, err := decodeData[execution.StepResult](m.Payload)
		if err != nil {
			return nil, err
		}
		if result.Interruption != nil {
			return nil, fault("invalid_result", "interruption is reserved for the host runtime")
		}
		var workRaw, resultRaw []byte
		if err = tx.QueryRow(ctx, `SELECT work,result FROM factory.comms_participants WHERE id=$1`, p.ID).Scan(&workRaw, &resultRaw); err != nil {
			return nil, err
		}
		if len(resultRaw) > 0 {
			return nil, fault("already_completed", "a result is already accepted")
		}
		var work execution.Work
		if err = json.Unmarshal(workRaw, &work); err != nil {
			return nil, err
		}
		if m.ThreadID != threadID(work) {
			return nil, fault("not_authorized", "submit step results in the assigned control thread")
		}
		if _, ok := work.Step.Routes[result.Outcome]; !ok {
			return nil, fault("invalid_outcome", "outcome %q is not configured", result.Outcome)
		}
		if result.Summary == "" {
			return nil, fault("invalid_argument", "result summary is required")
		}
		var pending bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_requests WHERE owner=$1 AND state='pending')`, p.ID).Scan(&pending); err != nil {
			return nil, err
		}
		if pending {
			return nil, fault("input_pending", "answer pending input before completing")
		}
		if err = s.checkPublication(ctx, tx, work, result, m.ThreadID); err != nil {
			return nil, err
		}
		result.Actor = p.ID
		result.At = m.At
		var paths []string
		for _, a := range result.Artifacts {
			paths = append(paths, a.Name)
		}
		if s.Collector != nil {
			workCopy := work
			if !work.Step.RequiresOutputs(result.Outcome) {
				workCopy.Step.Outputs = nil
			}
			result.Artifacts, err = s.Collector.Collect(ctx, workCopy, paths)
			if err != nil {
				return nil, fault("artifact_validation_failed", "outcome %s: %s; %v", result.Outcome, result.Summary, err)
			}
		} else if len(paths) > 0 || (len(work.Step.Outputs) > 0 && work.Step.RequiresOutputs(result.Outcome)) {
			return nil, fault("artifact_validation_failed", "runner has no artifact collector")
		}
		raw, _ := json.Marshal(result)
		_, err = tx.Exec(ctx, `UPDATE factory.comms_participants SET result=$2 WHERE id=$1`, p.ID, raw)
		return &Effect{State: "accepted", ResultID: m.ID, Routing: "pending"}, err
	}
	return nil, nil
}
func (s *Service) Requests(ctx context.Context, executionID string) ([]Request, error) {
	rows, err := s.DB.Pool.Query(ctx, `SELECT r.id,r.execution_id,r.thread_id,r.owner,r.prompt,r.options,r.state,r.created_at,COALESCE(p.work->'step'->>'id',''),COALESCE((p.work->>'visit')::bigint,0) FROM factory.comms_requests r JOIN factory.comms_participants p ON p.id=r.owner WHERE ($1='' OR r.execution_id=$1) AND r.state='pending' ORDER BY r.created_at,r.id`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		var r Request
		var ownerStep string
		var visit uint
		if err = rows.Scan(&r.ID, &r.ExecutionID, &r.ThreadID, &r.Owner, &r.Prompt, &r.Options, &r.State, &r.At, &ownerStep, &visit); err != nil {
			return nil, err
		}
		if ownerStep != "" {
			r.StepID = execution.StepVisitID(r.ExecutionID, execution.StepID(ownerStep), visit)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) Answer(ctx context.Context, requestID, answer string) (Receipt, error) {
	var r Request
	err := s.DB.Pool.QueryRow(ctx, `SELECT execution_id,thread_id,owner FROM factory.comms_requests WHERE id=$1`, requestID).Scan(&r.ExecutionID, &r.ThreadID, &r.Owner)
	if err != nil {
		return Receipt{}, err
	}
	human := s.Operator(r.ExecutionID)
	return s.Send(ctx, human, Send{OperationID: "answer/" + requestID, ThreadID: r.ThreadID, Recipients: []string{r.Owner, identity(r.ExecutionID + "/controller")}, InReplyTo: requestID, Payload: Structured("input_response", map[string]any{"request_id": requestID, "answer": answer})})
}
