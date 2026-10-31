package sessions

import (
	"context"
	"controls/internal/state"
	"encoding/json"
	"time"
)

type BudgetNotice struct {
	Key     string    `json:"key"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

func (m *Manager) ReportBudget(id, key, message string) error {
	session, err := m.Get(id)
	if err != nil {
		return err
	}
	return m.db.Change(context.Background(), "session", id, func(raw []byte) ([]byte, error) {
		var current Session
		if err := json.Unmarshal(raw, &current); err != nil {
			return nil, err
		}
		for _, notice := range current.BudgetNotices {
			if notice.Key == key {
				return nil, nil
			}
		}
		current.BudgetNotices = append(current.BudgetNotices, BudgetNotice{Key: key, Message: message, At: time.Now().UTC()})
		return json.Marshal(current)
	}, state.Activity{ExecutionID: session.ExecutionID, SessionID: id, Source: "budget/" + session.Name, Data: []byte(message + "\n")})
}
