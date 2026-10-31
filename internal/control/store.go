package control

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"controls/execution"
	"controls/internal/inputs"
	"controls/internal/state"
	"controls/internal/telemetry"

	"github.com/jackc/pgx/v5"
)

type Context struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Step struct {
	Stage      string                `json:"stage,omitempty"`
	Attempts   []Attempt             `json:"attempts,omitempty"`
	Result     *execution.StepResult `json:"result,omitempty"`
	Resources  *telemetry.Summary    `json:"resources,omitempty"`
	ID         string                `json:"id"`
	Key        execution.StepID      `json:"key"`
	Title      string                `json:"title,omitempty"`
	Visit      uint                  `json:"visit"`
	Try        uint                  `json:"try"`
	State      string                `json:"state"`
	Outcome    execution.Outcome     `json:"outcome,omitempty"`
	Error      string                `json:"error,omitempty"`
	Sessions   []string              `json:"sessions,omitempty"`
	StartedAt  time.Time             `json:"started_at"`
	FinishedAt *time.Time            `json:"finished_at,omitempty"`
}

type Request struct {
	Kind        string              `json:"kind,omitempty"`
	ThreadID    string              `json:"thread_id,omitempty"`
	Owner       string              `json:"owner,omitempty"`
	ID          string              `json:"id"`
	ExecutionID string              `json:"execution_id"`
	StepID      string              `json:"step_id"`
	Prompt      string              `json:"prompt"`
	Options     []execution.Outcome `json:"options"`
	State       string              `json:"state"`
	Answer      execution.Outcome   `json:"answer,omitempty"`
	Reason      string              `json:"reason,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	AnsweredAt  *time.Time          `json:"answered_at,omitempty"`
}

type Event struct {
	Sequence    uint64    `json:"sequence"`
	ExecutionID string    `json:"execution_id"`
	StepID      string    `json:"step_id,omitempty"`
	Kind        string    `json:"kind"`
	Message     string    `json:"message"`
	At          time.Time `json:"at"`
}

type Execution struct {
	Stages        []StageVisit             `json:"stages,omitempty"`
	FinishedAt    *time.Time               `json:"finished_at,omitempty"`
	Hooks         []execution.HookRun      `json:"hooks,omitempty"`
	Resources     *telemetry.Summary       `json:"resources,omitempty"`
	ID            string                   `json:"id"`
	ContextID     string                   `json:"context_id"`
	Workflow      string                   `json:"workflow"`
	Project       string                   `json:"project"`
	ConfigPath    string                   `json:"config_path"`
	ConfigDigest  string                   `json:"config_digest"`
	WorkflowRunID string                   `json:"workflow_run_id,omitempty"`
	Brief         string                   `json:"brief,omitempty"`
	Input         inputs.Bundle            `json:"input"`
	Configuration execution.Configuration  `json:"configuration"`
	State         execution.ExecutionState `json:"state"`
	CurrentStepID string                   `json:"current_step_id,omitempty"`
	Error         string                   `json:"error,omitempty"`
	Steps         []Step                   `json:"steps,omitempty"`
	Requests      []Request                `json:"requests,omitempty"`
	Events        []Event                  `json:"events,omitempty"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

// Store has no authoritative process-local state. Each operation reads or
// changes Factory-owned PostgreSQL records, with row locks for updates.
type Store struct{ db *state.Store }

func Open(db *state.Store) (*Store, error) {
	if db == nil {
		return nil, errors.New("Factory database is required")
	}
	_, err := db.Pool.Exec(context.Background(), `
		INSERT INTO factory.records(kind,id,content)
		VALUES ('preference','selected_context','"*"'::jsonb)
		ON CONFLICT DO NOTHING`)
	return &Store{db: db}, err
}

func (s *Store) GetContext() (string, *Context, error) {
	ctx := context.Background()
	var selected string
	if err := s.db.Get(ctx, "preference", "selected_context", &selected); err != nil {
		return "", nil, err
	}
	if selected == "*" {
		return selected, nil, nil
	}
	var value Context
	if err := s.db.Get(ctx, "context", selected, &value); err != nil {
		return "", nil, err
	}
	return selected, &value, nil
}

func (s *Store) SetContext(id string) error {
	if id != "*" {
		var value Context
		if err := s.db.Get(context.Background(), "context", id, &value); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("unknown context %q", id)
			}
			return err
		}
	}
	return s.db.Change(context.Background(), "preference", "selected_context", func([]byte) ([]byte, error) {
		return json.Marshal(id)
	})
}

func (s *Store) ListContexts() ([]Context, error) {
	rows, err := s.db.List(context.Background(), "context")
	if err != nil {
		return nil, err
	}
	result := make([]Context, 0, len(rows))
	for _, row := range rows {
		var value Context
		if err := json.Unmarshal(row, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

type NewExecution struct {
	ID            string
	ContextID     string
	Description   string
	Workflow      string
	Project       string
	ConfigPath    string
	ConfigDigest  string
	Brief         string
	Input         inputs.Bundle
	Configuration execution.Configuration
}

func (s *Store) Create(input NewExecution) (Execution, error) {
	if input.ID == "" {
		return Execution{}, errors.New("execution ID is required")
	}
	ctx := context.Background()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer tx.Rollback(ctx)
	contextID := input.ContextID
	if contextID == "" {
		var content []byte
		if err := tx.QueryRow(ctx, `SELECT content FROM factory.records WHERE kind='preference' AND id='selected_context' FOR UPDATE`).Scan(&content); err != nil {
			return Execution{}, err
		}
		if err := json.Unmarshal(content, &contextID); err != nil {
			return Execution{}, err
		}
	}
	now := time.Now().UTC()
	if contextID == "*" {
		description := strings.TrimSpace(input.Description)
		if description == "" && len(input.Input.Documents) > 0 {
			description = input.Input.Documents[0].Name
		}
		created := false
		for attempt := 0; attempt < 10 && !created; attempt++ {
			contextID, err = newContextID()
			if err != nil {
				return Execution{}, err
			}
			value, err := json.Marshal(Context{ID: contextID, Description: description, CreatedAt: now})
			if err != nil {
				return Execution{}, err
			}
			result, err := tx.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ('context',$1,$2) ON CONFLICT DO NOTHING`, contextID, value)
			if err != nil {
				return Execution{}, err
			}
			created = result.RowsAffected() == 1
		}
		if !created {
			return Execution{}, errors.New("could not allocate a unique context ID")
		}
	} else {
		var found bool
		if err := tx.QueryRow(ctx, `SELECT true FROM factory.records WHERE kind='context' AND id=$1`, contextID).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Execution{}, fmt.Errorf("unknown context %q", contextID)
			}
			return Execution{}, err
		}
	}
	record := Execution{ID: input.ID, ContextID: contextID, Workflow: input.Workflow,
		Project: input.Project, ConfigPath: input.ConfigPath, ConfigDigest: input.ConfigDigest,
		Brief: input.Brief, Input: input.Input, Configuration: input.Configuration,
		State: execution.ExecutionReady, CreatedAt: now, UpdatedAt: now}
	appendEvent(&record, "execution_created", "Execution created", "")
	content, err := json.Marshal(record)
	if err != nil {
		return Execution{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ('execution',$1,$2)`, record.ID, content); err != nil {
		return Execution{}, err
	}
	if err := state.AppendActivityTx(ctx, tx, state.Activity{ExecutionID: record.ID, Source: "workflow", Data: []byte("execution_created: Execution created\n")}); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return record, nil
}

func (s *Store) List(contextID string) ([]Execution, error) {
	if contextID == "" {
		var err error
		contextID, _, err = s.GetContext()
		if err != nil {
			return nil, err
		}
	}
	if contextID != "*" {
		var value Context
		if err := s.db.Get(context.Background(), "context", contextID, &value); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("unknown context %q", contextID)
			}
			return nil, err
		}
	}
	rows, err := s.db.List(context.Background(), "execution")
	if err != nil {
		return nil, err
	}
	result := make([]Execution, 0, len(rows))
	for _, row := range rows {
		var record Execution
		if err := json.Unmarshal(row, &record); err != nil {
			return nil, err
		}
		if contextID == "*" || record.ContextID == contextID {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (s *Store) Get(id string) (Execution, error) {
	var record Execution
	err := s.db.Get(context.Background(), "execution", id, &record)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, os.ErrNotExist
	}
	return record, err
}

func (s *Store) Find(id string) (string, Execution, error) {
	if record, err := s.Get(id); err == nil {
		return "execution", record, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", Execution{}, err
	}
	rows, err := s.db.List(context.Background(), "execution")
	if err != nil {
		return "", Execution{}, err
	}
	for _, row := range rows {
		var record Execution
		if err := json.Unmarshal(row, &record); err != nil {
			return "", Execution{}, err
		}
		for _, hook := range record.Hooks {
			if hook.ID == id {
				return "hook", record, nil
			}
		}
		for _, step := range record.Steps {
			if step.ID == id {
				return "step", record, nil
			}
		}
		for _, request := range record.Requests {
			if request.ID == id {
				return "request", record, nil
			}
		}
	}
	return "", Execution{}, os.ErrNotExist
}

func (s *Store) Update(id, kind, message, stepID string, change func(*Execution) error) (Execution, error) {
	return s.Apply(id, kind, message, stepID, func(record *Execution) (bool, error) {
		return true, change(record)
	})
}

func (s *Store) Apply(id, kind, message, stepID string, change func(*Execution) (bool, error)) (Execution, error) {
	var result Execution
	err := s.db.Change(context.Background(), "execution", id, func(content []byte) ([]byte, error) {
		var record Execution
		if err := json.Unmarshal(content, &record); err != nil {
			return nil, err
		}
		changed, err := change(&record)
		if err != nil {
			return nil, err
		}
		result = record
		if !changed {
			return nil, nil
		}
		record.UpdatedAt = time.Now().UTC()
		appendEvent(&record, kind, message, stepID)
		result = record
		return json.Marshal(record)
	}, state.Activity{ExecutionID: id, Source: "workflow", Data: []byte(kind + ": " + message + "\n")})
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, os.ErrNotExist
	}
	return result, err
}

func appendEvent(record *Execution, kind, message, stepID string) {
	record.Events = append(record.Events, Event{Sequence: uint64(len(record.Events) + 1),
		ExecutionID: record.ID, StepID: stepID, Kind: kind, Message: message, At: time.Now().UTC()})
}

func newContextID() (string, error) {
	var random [5]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random[:]), nil
}

func StepID(executionID string, key execution.StepID, visit uint) string {
	return execution.StepVisitID(executionID, key, visit)
}
