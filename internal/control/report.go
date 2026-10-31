package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controls/internal/reports"
	"controls/internal/sessions"
	"controls/internal/telemetry"

	"github.com/jackc/pgx/v5"
)

// Report captures a consistent database view once. No filesystem evidence,
// model calls or live process queries participate in report generation.
func (s *Store) Report(ctx context.Context, q reports.Query) (reports.Document, error) {
	if q.ReportID != "" {
		if q.Factory != "" || q.ExecutionID != "" || q.From != nil || q.To != nil {
			return reports.Document{}, errors.New("report_id cannot be combined with selectors")
		}
		var saved reports.Document
		err := s.db.Get(ctx, "report", q.ReportID, &saved)
		return saved, err
	}
	if q.Factory == "" && q.ExecutionID == "" {
		return reports.Document{}, errors.New("select a factory name or execution ID")
	}
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return reports.Document{}, err
	}
	defer tx.Rollback(ctx)
	var asOf time.Time
	if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&asOf); err != nil {
		return reports.Document{}, err
	}
	from, to, err := q.Window(asOf)
	if err != nil {
		return reports.Document{}, err
	}
	var selectedID string
	if q.ExecutionID != "" {
		rows, err := tx.Query(ctx, `SELECT content FROM factory.records WHERE kind='execution' AND starts_with(id,$1)`, q.ExecutionID)
		if err != nil {
			return reports.Document{}, err
		}
		var matched []Execution
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return reports.Document{}, err
			}
			var r Execution
			if err := json.Unmarshal(raw, &r); err != nil {
				rows.Close()
				return reports.Document{}, err
			}
			matched = append(matched, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return reports.Document{}, err
		}
		if len(matched) != 1 {
			return reports.Document{}, fmt.Errorf("execution ID must match exactly one execution (found %d)", len(matched))
		}
		selectedID = matched[0].ID
		if q.Factory != "" && q.Factory != matched[0].Workflow {
			return reports.Document{}, errors.New("execution does not belong to the selected factory")
		}
		q.Factory = matched[0].Workflow
	}
	rows, err := tx.Query(ctx, `SELECT content FROM factory.records
 WHERE kind='execution' AND content->>'workflow'=$1
 AND (((content->>'created_at')::timestamptz >= $2
       AND (content->>'created_at')::timestamptz < $3) OR id=$4)
 ORDER BY content->>'created_at',id`, q.Factory, from, to, selectedID)
	if err != nil {
		return reports.Document{}, err
	}
	var records []Execution
	var ids []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return reports.Document{}, err
		}
		var record Execution
		if err := json.Unmarshal(raw, &record); err != nil {
			rows.Close()
			return reports.Document{}, err
		}
		records = append(records, record)
		ids = append(ids, record.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return reports.Document{}, err
	}
	if len(records) == 0 {
		return reports.Document{}, errors.New("no executions match the factory and history window")
	}
	// Telemetry and session metadata come from the same MVCC snapshot as lifecycle data.
	rows, err = tx.Query(ctx, `SELECT s.content,t.content FROM factory.records s
 LEFT JOIN factory.records t ON t.kind='telemetry' AND t.id=s.id
 WHERE s.kind='session' AND s.content->>'execution_id'=ANY($1::text[])
 ORDER BY s.id`, ids)
	if err != nil {
		return reports.Document{}, err
	}
	byExecution := map[string][]sessions.Session{}
	for rows.Next() {
		var raw, usage []byte
		if err := rows.Scan(&raw, &usage); err != nil {
			rows.Close()
			return reports.Document{}, err
		}
		var session sessions.Session
		if err := json.Unmarshal(raw, &session); err != nil {
			rows.Close()
			return reports.Document{}, err
		}
		session.Resources = telemetry.Summary{}
		if usage != nil {
			var p telemetry.Projection
			if err := json.Unmarshal(usage, &p); err != nil {
				rows.Close()
				return reports.Document{}, err
			}
			session.Resources = p.Summary
		}
		byExecution[session.ExecutionID] = append(byExecution[session.ExecutionID], session)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return reports.Document{}, err
	}
	snapshot := reports.Snapshot{AsOf: asOf.UTC(), From: from, To: to, Factory: q.Factory, History: []reports.Execution{}}
	for _, record := range records {
		view := reportExecution(record, byExecution[record.ID])
		if record.ID == selectedID {
			copy := view
			snapshot.Selected = &copy
		}
		if !record.CreatedAt.Before(from) && record.CreatedAt.Before(to) {
			snapshot.History = append(snapshot.History, view)
		}
	}
	document, err := reports.Build(snapshot)
	if err != nil {
		return reports.Document{}, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return reports.Document{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ('report',$1,$2) ON CONFLICT DO NOTHING`, document.ID, raw); err != nil {
		return reports.Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return reports.Document{}, err
	}
	return document, nil
}

func reportExecution(record Execution, agents []sessions.Session) reports.Execution {
	view := reports.Execution{ID: record.ID, Factory: record.Workflow, ConfigDigest: record.ConfigDigest, State: string(record.State), Error: record.Error, CreatedAt: record.CreatedAt, FinishedAt: record.FinishedAt}
	for _, hook := range record.Hooks {
		view.Hooks = append(view.Hooks, reports.Hook{Name: hook.Context.Name, Scope: hook.Context.Scope, Boundary: hook.Context.Boundary, State: hook.State, Try: hook.Try, Error: hook.Error, SessionID: hook.SessionID, StartedAt: hook.StartedAt, FinishedAt: hook.FinishedAt})
	}
	for _, stage := range record.Stages {
		view.Stages = append(view.Stages, reports.Phase{Name: stage.Name, Visit: stage.Visit, State: stage.State, StartedAt: stage.StartedAt, FinishedAt: stage.FinishedAt})
	}
	for _, step := range record.Steps {
		measured := reports.Step{ID: step.ID, Stage: step.Stage, Phase: reports.Phase{Name: string(step.Key), Visit: step.Visit, State: step.State, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt}, Error: step.Error}
		for _, attempt := range step.Attempts {
			measured.Attempts = append(measured.Attempts, reports.Attempt{Try: attempt.Try, State: attempt.State, StartedAt: attempt.StartedAt, FinishedAt: attempt.FinishedAt, Error: attempt.Error})
		}
		for _, agent := range agents {
			if agent.StepID == step.ID && agent.Runner != "hook" {
				measured.Resources.Include(agent.Resources)
			}
		}
		view.Steps = append(view.Steps, measured)
		if step.Result != nil {
			r := step.Result
			view.Assessments = append(view.Assessments, reports.Assessment{Source: "step result", Step: string(step.Key), Actor: r.Actor, Outcome: string(r.Outcome), Summary: r.Summary, At: r.At})
		}
	}
	for _, agent := range agents {
		if agent.Runner == "hook" {
			continue
		}
		view.Resources.Include(agent.Resources)
		view.Agents = append(view.Agents, reports.Agent{Name: agent.Name, SessionID: agent.ID, StepID: agent.StepID, Runner: agent.Runner, Provider: agent.Resources.Provider, Model: agent.Resources.Model})
	}
	for _, request := range record.Requests {
		if request.AnsweredAt != nil {
			view.Assessments = append(view.Assessments, reports.Assessment{Source: "operator decision", Step: request.StepID, Outcome: string(request.Answer), Summary: request.Reason, At: *request.AnsweredAt})
		}
	}
	return view
}
