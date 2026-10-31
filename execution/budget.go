package execution

import "fmt"

// Budget is per step attempt, including time waiting for peers. Zero limits
// are disabled. Token counts include all reported cache categories.
type Budget struct {
	Tokens         uint64 `json:"tokens,omitempty"`
	ElapsedSeconds uint64 `json:"elapsed_seconds,omitempty"`
	IdleSeconds    uint64 `json:"idle_seconds,omitempty"`
	WarnPercent    uint   `json:"warn_percent,omitempty"`
	Action         string `json:"action"` // warn, pause, stop
}

func (b *Budget) Validate() error {
	if b == nil {
		return nil
	}
	if b.Tokens == 0 && b.ElapsedSeconds == 0 && b.IdleSeconds == 0 {
		return fmt.Errorf("budget requires at least one positive limit")
	}
	if b.WarnPercent > 100 {
		return fmt.Errorf("budget warn_percent must be between 0 and 100")
	}
	if b.Action != "warn" && b.Action != "pause" && b.Action != "stop" {
		return fmt.Errorf("budget action must be warn, pause, or stop")
	}
	return nil
}

// Interruption is a host enforcement result, not an agent outcome or route.
// It crosses the scheduler serialization boundary without depending on an SDK.
type Interruption struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}
