package sessions

import "time"

type State string

const (
	Running     State = "running"
	Stopping    State = "stopping"
	Exited      State = "exited"
	Stopped     State = "stopped"
	Interrupted State = "interrupted"
)

// Session identifies a supervised process. ParentID links a Factory-managed
// subagent to the session that started it. A runner's private subagents cannot
// be discovered without that runner reporting them.
type Session struct {
	ID           string     `json:"id"`
	ParentID     string     `json:"parent_id,omitempty"`
	ExecutionID  string     `json:"execution_id,omitempty"`
	StepID       string     `json:"step_id,omitempty"`
	Visit        uint       `json:"visit,omitempty"`
	Try          uint       `json:"try,omitempty"`
	Name         string     `json:"name"`
	Runner       string     `json:"runner"`
	Owner        string     `json:"owner,omitempty"`
	InputMode    string     `json:"input_mode"`
	State        State      `json:"state"`
	PID          int        `json:"pid,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	LastOutputAt *time.Time `json:"last_output_at,omitempty"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	ExitCode     *int       `json:"exit_code,omitempty"`
}

// Launch starts an agent command without a shell. Environment values are
// passed to the process but never written to the session record.
type Launch struct {
	ParentID    string            `json:"parent_id,omitempty"`
	ExecutionID string            `json:"execution_id,omitempty"`
	StepID      string            `json:"step_id,omitempty"`
	Visit       uint              `json:"visit,omitempty"`
	Try         uint              `json:"try,omitempty"`
	Name        string            `json:"name"`
	Runner      string            `json:"runner"`
	InputMode   string            `json:"input_mode,omitempty"`
	Directory   string            `json:"directory"`
	Command     []string          `json:"command"`
	Environment map[string]string `json:"environment,omitempty"`
}

type MessageState string

const (
	MessageQueued    MessageState = "queued"
	MessageDelivered MessageState = "delivered"
	MessageRead      MessageState = "read"
	MessageFailed    MessageState = "failed"
)

// A message is a request to an agent, not evidence that it acted on the text.
type Message struct {
	ID          string       `json:"id"`
	SessionID   string       `json:"session_id"`
	Sender      string       `json:"sender,omitempty"`
	Body        string       `json:"body"`
	State       MessageState `json:"state"`
	CreatedAt   time.Time    `json:"created_at"`
	DeliveredAt *time.Time   `json:"delivered_at,omitempty"`
	ReadAt      *time.Time   `json:"read_at,omitempty"`
}

type Output struct {
	Text       string `json:"text"`
	NextOffset int64  `json:"next_offset"`
}
