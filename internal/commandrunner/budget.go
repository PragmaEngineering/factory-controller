package commandrunner

import (
	"controls/execution"
	"controls/internal/sessions"
	"fmt"
	"time"
)

type budgetNotice struct {
	key, message string
	reached      bool
}

// The host checks wall time even when the runner emits no telemetry. Token
// and idle checks use completed events, not output bytes or thinking deltas.
func checkBudget(b *execution.Budget, s sessions.Session, now time.Time) []budgetNotice {
	if b == nil {
		return nil
	}
	progress := s.StartedAt
	if s.Resources.LastProgressAt != nil {
		progress = *s.Resources.LastProgressAt
	}
	elapsed := max(0, now.Sub(s.StartedAt).Seconds())
	idle := max(0, now.Sub(progress).Seconds())
	metrics := []struct {
		name        string
		used, limit float64
	}{
		{"tokens", float64(s.Resources.Tokens.Total()), float64(b.Tokens)},
		{"elapsed seconds", elapsed, float64(b.ElapsedSeconds)},
		{"seconds without progress", idle, float64(b.IdleSeconds)},
	}
	var notices []budgetNotice
	for _, m := range metrics {
		if m.limit == 0 {
			continue
		}
		reached := m.used >= m.limit
		if reached || (b.WarnPercent > 0 && m.used/m.limit*100 >= float64(b.WarnPercent)) {
			level := "warning"
			if reached {
				level = "limit"
			}
			notices = append(notices, budgetNotice{key: m.name + "/" + level, message: fmt.Sprintf("Budget %s: %.0f / %.0f %s; policy %s", level, m.used, m.limit, m.name, b.Action), reached: reached})
		}
	}
	return notices
}
