package commandrunner

import (
	"context"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/sessions"
	"controls/internal/telemetry"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestBudgetThresholdsIncludeCachesAndIgnoreChatter(t *testing.T) {
	now := time.Now()
	progress := now.Add(-9 * time.Second)
	s := sessions.Session{StartedAt: now.Add(-15 * time.Second), Resources: telemetry.Summary{Tokens: telemetry.Tokens{Input: 10, CacheRead: 70}, LastProgressAt: &progress}}
	b := &execution.Budget{Tokens: 100, ElapsedSeconds: 20, IdleSeconds: 10, WarnPercent: 80, Action: "pause"}
	notices := checkBudget(b, s, now)
	require.Len(t, notices, 2)
	require.False(t, notices[0].reached)
	require.False(t, notices[1].reached)
	s.Resources.Tokens.CacheWrite = 20
	notices = checkBudget(b, s, now)
	require.True(t, notices[0].reached)
	require.Contains(t, notices[0].message, "100 / 100 tokens")
	require.Empty(t, checkBudget(nil, s, now))
}

type budgetHost struct {
	stopped bool
	notices []string
}

func (h *budgetHost) Launch(sessions.Launch) (sessions.Session, error) {
	return sessions.Session{ID: "session"}, nil
}
func (h *budgetHost) Get(string) (sessions.Session, error) {
	return sessions.Session{ID: "session", State: sessions.Running, StartedAt: time.Now().Add(-2 * time.Second), Resources: telemetry.Summary{Samples: 1, Tokens: telemetry.Tokens{CacheRead: 100}}}, nil
}
func (h *budgetHost) Stop(string) error { h.stopped = true; return nil }
func (h *budgetHost) Output(string, int64, int) (sessions.Output, error) {
	return sessions.Output{}, nil
}
func (h *budgetHost) ReportBudget(_ string, _ string, message string) error {
	h.notices = append(h.notices, message)
	return nil
}
func TestLimitsStopTheProcessAndReturnHostInterruption(t *testing.T) {
	for _, action := range []string{"pause", "stop", "warn"} {
		t.Run(action, func(t *testing.T) {
			work := fixture(t)
			work.Step.Budget = &execution.Budget{Tokens: 100, Action: action}
			host := &budgetHost{}
			communication := &testCommunication{connection: comms.Connection{New: true, ParticipantID: "participant"}}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			result, err := (Runner{Sessions: host, Recorder: testRecorder{}, Comms: communication, Root: t.TempDir()}).Run(ctx, work)
			require.True(t, host.stopped)
			require.NotEmpty(t, host.notices)
			if action == "warn" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Nil(t, result.Interruption)
			} else {
				require.NoError(t, err)
				require.Equal(t, action, result.Interruption.Action)
			}
			require.True(t, communication.ended)
		})
	}
}

func TestTimeBudgetWorksWithoutTelemetry(t *testing.T) {
	now := time.Now()
	session := sessions.Session{StartedAt: now.Add(-30 * time.Second)}
	notices := checkBudget(&execution.Budget{ElapsedSeconds: 20, IdleSeconds: 10, Action: "pause"}, session, now)
	require.Len(t, notices, 2)
	require.True(t, notices[0].reached)
	require.True(t, notices[1].reached)
}
