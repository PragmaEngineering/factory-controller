// Package hooks executes trusted lifecycle utilities through the host supervisor.
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controls/execution"
	"controls/internal/sessions"
)

type Recorder interface {
	ClaimHook(execution.HookWork) (execution.HookRun, bool, error)
	SaveHook(string, execution.HookRun) error
}
type Supervisor interface {
	Launch(sessions.Launch) (sessions.Session, error)
	Get(string) (sessions.Session, error)
	List(string) ([]sessions.Session, error)
	Stop(string) error
}
type Runner struct {
	Recorder Recorder
	Sessions Supervisor
}

func (r Runner) RunHook(ctx context.Context, work execution.HookWork) (execution.HookRun, error) {
	// Any surviving attempt of this hook must stop before an operator retries it.
	existing, err := r.Sessions.List(work.Context.ExecutionID)
	if err != nil {
		return execution.HookRun{}, err
	}
	for _, session := range existing {
		if session.StepID == work.Context.InvocationID && session.Try != work.Try && (session.State == sessions.Running || session.State == sessions.Stopping) {
			return execution.HookRun{}, fmt.Errorf("previous hook session %s is still active", session.ID)
		}
	}
	run, claimed, err := r.Recorder.ClaimHook(work)
	if err != nil {
		return run, err
	}
	if !claimed {
		if run.State == "succeeded" || run.State == "failed" {
			return run, nil
		}
		// Resolve a crash between process launch and recording the session ID.
		if run.SessionID == "" {
			for _, session := range existing {
				if session.StepID == run.ID && session.Try == work.Try {
					run.SessionID = session.ID
					break
				}
			}
		}
		if run.SessionID == "" {
			return run, fmt.Errorf("hook launch ownership is unresolved; inspect the worker before retrying")
		}
	} else {
		input, err := json.Marshal(work.Context)
		if err != nil {
			return run, err
		}
		session, err := r.Sessions.Launch(sessions.Launch{ExecutionID: work.Context.ExecutionID, StepID: run.ID, Try: work.Try, Name: "hook/" + work.Hook.Name, Runner: "hook", InputMode: "none", Directory: work.Context.Project, Command: append([]string{work.Hook.Command}, work.Hook.Arguments...), Stdin: string(input) + "\n"})
		if err != nil {
			return r.finish(work, run, "failed", err.Error())
		}
		run.SessionID = session.ID
		if err := r.Recorder.SaveHook(work.Context.ExecutionID, run); err != nil {
			_ = r.Sessions.Stop(session.ID)
			return run, err
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		session, err := r.Sessions.Get(run.SessionID)
		if err != nil {
			return run, err
		}
		switch session.State {
		case sessions.Running, sessions.Stopping:
		case sessions.Exited:
			if session.ExitCode != nil && *session.ExitCode == 0 {
				return r.finish(work, run, "succeeded", "")
			}
			return r.finish(work, run, "failed", fmt.Sprintf("hook command exited %v; inspect session %s logs", exitCode(session), session.ID))
		default:
			return r.finish(work, run, "failed", "hook session "+string(session.State)+"; inspect effects before retrying")
		}
		select {
		case <-ctx.Done():
			_ = r.Sessions.Stop(run.SessionID)
			return r.finish(work, run, "failed", ctx.Err().Error())
		case <-tick.C:
		}
	}
}

func (r Runner) finish(work execution.HookWork, run execution.HookRun, state, message string) (execution.HookRun, error) {
	now := time.Now().UTC()
	run.State = state
	run.Error = message
	run.FinishedAt = &now
	return run, r.Recorder.SaveHook(work.Context.ExecutionID, run)
}

func exitCode(session sessions.Session) any {
	if session.ExitCode == nil {
		return "unknown"
	}
	return *session.ExitCode
}
