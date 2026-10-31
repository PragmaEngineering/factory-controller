package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"controls/execution"
	"controls/internal/reports"
	"controls/internal/sessions"
)

type Resource struct {
	Hook        *execution.HookRun `json:"hook,omitempty"`
	Type        string             `json:"type"`
	ExecutionID string             `json:"execution_id,omitempty"`
	Execution   *Execution         `json:"execution,omitempty"`
	Step        *Step              `json:"step,omitempty"`
	Request     *Request           `json:"request,omitempty"`
	Session     *sessions.Session  `json:"session,omitempty"`
}

func Handler(controller *Controller) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /reports", func(w http.ResponseWriter, r *http.Request) {
		var query reports.Query
		if !decode(w, r, &query) {
			return
		}
		document, err := controller.Store.Report(r.Context(), query)
		result(w, http.StatusOK, document, err)
	})
	mux.HandleFunc("GET /report-template", func(w http.ResponseWriter, r *http.Request) {
		source, err := reports.Template()
		result(w, http.StatusOK, map[string]string{"template": source}, err)
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, http.StatusOK, map[string]string{"api": "factory-control-v3"})
	})
	mux.HandleFunc("GET /contexts", func(w http.ResponseWriter, _ *http.Request) {
		contexts, err := controller.Store.ListContexts()
		result(w, http.StatusOK, contexts, err)
	})
	mux.HandleFunc("GET /context", func(w http.ResponseWriter, _ *http.Request) {
		id, context, err := controller.Store.GetContext()
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		respond(w, http.StatusOK, struct {
			ID      string   `json:"id"`
			Context *Context `json:"context,omitempty"`
		}{id, context})
	})
	mux.HandleFunc("PUT /context", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := controller.Store.SetContext(input.ID); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		id, context, err := controller.Store.GetContext()
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		respond(w, http.StatusOK, struct {
			ID      string   `json:"id"`
			Context *Context `json:"context,omitempty"`
		}{id, context})
	})
	mux.HandleFunc("GET /executions", func(w http.ResponseWriter, r *http.Request) {
		list, err := controller.Store.List(r.URL.Query().Get("context"))
		if err == nil {
			for i := range list {
				if err = controller.addInputRequests(&list[i]); err != nil {
					break
				}
			}
		}
		result(w, http.StatusOK, list, err)
	})
	mux.HandleFunc("POST /executions", func(w http.ResponseWriter, r *http.Request) {
		var input ExecRequest
		if !decode(w, r, &input) {
			return
		}
		record, err := controller.Exec(r.Context(), input)
		result(w, http.StatusCreated, record, err)
	})
	mux.HandleFunc("GET /resources/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := controller.Resource(r.PathValue("id"))
		resultResponse(w, result, err)
	})
	mux.HandleFunc("GET /resources/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		logs, err := controller.Logs(r.PathValue("id"))
		resultResponse(w, struct {
			Text string `json:"text"`
		}{logs}, err)
	})
	mux.HandleFunc("GET /resources/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		resource, err := controller.Resource(r.PathValue("id"))
		if err != nil {
			resultResponse(w, nil, err)
			return
		}
		if resource.Type != "execution" && resource.Type != "step" && resource.Type != "hook" {
			failure(w, http.StatusBadRequest, errors.New("events require an execution or step ID"))
			return
		}
		events := resource.Execution.Events
		if resource.Type == "step" || resource.Type == "hook" {
			var filtered []Event
			for _, event := range events {
				if (resource.Step != nil && event.StepID == resource.Step.ID) || (resource.Hook != nil && event.StepID == resource.Hook.ID) {
					filtered = append(filtered, event)
				}
			}
			events = filtered
		}
		respond(w, http.StatusOK, events)
	})
	mux.HandleFunc("GET /resources/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		resource, err := controller.Resource(r.PathValue("id"))
		if err != nil {
			resultResponse(w, nil, err)
			return
		}
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if r.URL.Query().Get("after") == "" {
			after, err = 0, nil
		}
		if err != nil || after < 0 {
			failure(w, http.StatusBadRequest, errors.New("after must be a nonnegative integer"))
			return
		}
		items, err := controller.Store.db.Activity(r.Context(), resource.ExecutionID, after, 500)
		if err != nil {
			resultResponse(w, nil, err)
			return
		}
		respond(w, http.StatusOK, items)
	})
	mux.HandleFunc("GET /executions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		resource, err := controller.Resource(r.PathValue("id"))
		if err != nil {
			resultResponse(w, nil, err)
			return
		}
		if controller.Comms == nil {
			respond(w, http.StatusOK, []any{})
			return
		}
		messages, err := controller.Comms.AuditMessages(r.Context(), resource.ExecutionID)
		result(w, http.StatusOK, messages, err)
	})
	mux.HandleFunc("GET /inbox", func(w http.ResponseWriter, r *http.Request) {
		requests, err := controller.Inbox(r.URL.Query().Get("context"))
		result(w, http.StatusOK, requests, err)
	})
	mux.HandleFunc("POST /requests/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Outcome execution.Outcome `json:"outcome"`
			Reason  string            `json:"reason"`
			Actor   string            `json:"actor"`
		}
		if !decode(w, r, &input) {
			return
		}
		record, err := controller.Answer(r.Context(), r.PathValue("id"), input.Outcome, input.Reason, input.Actor)
		result(w, http.StatusOK, record, err)
	})
	mux.HandleFunc("POST /steps/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Actor  string `json:"actor"`
			Reason string `json:"reason"`
		}
		if !decode(w, r, &input) {
			return
		}
		record, err := controller.Retry(r.Context(), r.PathValue("id"), input.Actor, input.Reason)
		result(w, http.StatusOK, record, err)
	})
	mux.HandleFunc("POST /executions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		record, err := controller.Cancel(r.Context(), r.PathValue("id"))
		result(w, http.StatusOK, record, err)
	})
	return mux
}

func (c *Controller) Resource(id string) (Resource, error) {
	if c.Comms != nil {
		input, err := c.Comms.Input(context.Background(), id)
		if err != nil {
			return Resource{}, err
		}
		if input != nil {
			record, err := c.Store.Get(input.ExecutionID)
			if err != nil {
				return Resource{}, err
			}
			options := make([]execution.Outcome, len(input.Options))
			for i, v := range input.Options {
				options[i] = execution.Outcome(v)
			}
			request := Request{ID: input.ID, ExecutionID: input.ExecutionID, Kind: "step_input", ThreadID: input.ThreadID, Owner: input.Owner, Prompt: input.Prompt, Options: options, State: input.State, CreatedAt: input.At}
			return Resource{Type: "request", ExecutionID: record.ID, Execution: &record, Request: &request}, nil
		}
	}
	kind, record, err := c.Store.Find(id)
	if err == nil {
		if err = c.addInputRequests(&record); err != nil {
			return Resource{}, err
		}
		if err := c.addResources(&record); err != nil {
			return Resource{}, err
		}
		resource := Resource{Type: kind, ExecutionID: record.ID, Execution: &record}
		if kind == "hook" {
			for _, hook := range record.Hooks {
				if hook.ID == id {
					resource.Hook = &hook
					break
				}
			}
		}
		if kind == "step" {
			for _, step := range record.Steps {
				if step.ID == id {
					resource.Step = &step
					break
				}
			}
		}
		if kind == "request" {
			for _, request := range record.Requests {
				if request.ID == id {
					resource.Request = &request
					break
				}
			}
		}
		return resource, nil
	}
	session, sessionErr := c.Sessions.Get(id)
	if sessionErr == nil {
		return Resource{Type: "session", ExecutionID: session.ExecutionID, Session: &session}, nil
	}
	return Resource{}, os.ErrNotExist
}

func (c *Controller) Logs(id string) (string, error) {
	resource, err := c.Resource(id)
	if err != nil {
		return "", err
	}
	if resource.Type == "request" {
		return "", errors.New("decision requests have no process logs")
	}
	var ids []string
	if resource.Type == "session" {
		ids = append(ids, id)
	}
	if resource.Type == "step" {
		ids = append(ids, resource.Step.Sessions...)
	}
	if resource.Type == "hook" {
		all, err := c.Sessions.List(resource.ExecutionID)
		if err != nil {
			return "", err
		}
		for _, session := range all {
			if session.StepID == id {
				ids = append(ids, session.ID)
			}
		}
	}
	if resource.Type == "execution" {
		for _, hook := range resource.Execution.Hooks {
			if hook.SessionID != "" {
				ids = append(ids, hook.SessionID)
			}
		}
		for _, step := range resource.Execution.Steps {
			ids = append(ids, step.Sessions...)
		}
	}
	var output strings.Builder
	for _, sessionID := range ids {
		if len(ids) > 1 {
			fmt.Fprintf(&output, "\n--- session %s ---\n", sessionID)
		}
		var offset int64
		for {
			chunk, err := c.Sessions.Output(sessionID, offset, 65536)
			if err != nil {
				return "", fmt.Errorf("read session %s: %w", sessionID, err)
			}
			if chunk.NextOffset == offset {
				break
			}
			output.WriteString(chunk.Text)
			offset = chunk.NextOffset
		}
	}
	return output.String(), nil
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 48<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		failure(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func resultResponse(w http.ResponseWriter, value any, err error) {
	result(w, http.StatusOK, value, err)
}

func result(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			failure(w, http.StatusNotFound, err)
		} else {
			failure(w, http.StatusBadRequest, err)
		}
		return
	}
	respond(w, status, value)
}

func failure(w http.ResponseWriter, status int, err error) {
	respond(w, status, map[string]string{"error": err.Error()})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
