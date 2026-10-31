// Package telemetry projects runner-neutral resource events. Native runner
// output is retained separately and never interpreted as resource accounting.
package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

const Protocol = "factory.telemetry.v1"
const MaxLine = 256 * 1024

type Tokens struct {
	Input      uint64 `json:"input"`
	CacheRead  uint64 `json:"cache_read"`
	CacheWrite uint64 `json:"cache_write"`
	Output     uint64 `json:"output"`
}

func (t Tokens) Total() uint64 { return t.Input + t.CacheRead + t.CacheWrite + t.Output }
func (t *Tokens) Add(v Tokens) {
	t.Input += v.Input
	t.CacheRead += v.CacheRead
	t.CacheWrite += v.CacheWrite
	t.Output += v.Output
}

type Event struct {
	Type     string   `json:"type"`
	Sequence uint64   `json:"sequence"`
	Kind     string   `json:"kind"`
	Tokens   Tokens   `json:"tokens"`
	CostUSD  *float64 `json:"cost_usd,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Model    string   `json:"model,omitempty"`
	ToolID   string   `json:"tool_id,omitempty"`
	Command  string   `json:"command,omitempty"`
	Failed   bool     `json:"failed,omitempty"`
}
type Tool struct {
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
}

// Summary is a lower bound from reported, completed model responses. Cost is
// complete only when CostSamples equals Samples and Samples is nonzero.
type Summary struct {
	Events             uint64          `json:"events"`
	UnreportedSessions uint64          `json:"unreported_sessions"`
	Tokens             Tokens          `json:"tokens"`
	Samples            uint64          `json:"samples"`
	CostSamples        uint64          `json:"cost_samples"`
	CostUSD            float64         `json:"cost_usd"`
	Provider           string          `json:"provider,omitempty"`
	Model              string          `json:"model,omitempty"`
	ToolFailures       uint64          `json:"tool_failures"`
	LastProgressAt     *time.Time      `json:"last_progress_at,omitempty"`
	Active             map[string]Tool `json:"active_tools,omitempty"`
}

func (s Summary) Cost() string {
	if s.Samples == 0 || s.CostSamples != s.Samples || s.UnreportedSessions > 0 {
		return "unknown"
	}
	return fmt.Sprintf("$%.4f (reported)", s.CostUSD)
}
func (s Summary) Command() string {
	var latest time.Time
	command := ""
	for _, t := range s.Active {
		if t.StartedAt.After(latest) || (t.StartedAt.Equal(latest) && t.Command > command) {
			latest = t.StartedAt
			command = t.Command
		}
	}
	return command
}

// Projection includes the framing cursor so a process restart cannot lose a
// partial event or count an already committed event twice.
type Projection struct {
	Summary    Summary `json:"summary"`
	Sequence   uint64  `json:"sequence"`
	Pending    []byte  `json:"pending,omitempty"`
	Discarding bool    `json:"discarding,omitempty"`
}

func (p *Projection) Append(chunk []byte, at time.Time) {
	for len(chunk) > 0 {
		end := bytes.IndexByte(chunk, '\n')
		part := chunk
		if end >= 0 {
			part = chunk[:end]
		}
		if !p.Discarding {
			if len(p.Pending)+len(part) > MaxLine {
				p.Pending = nil
				p.Discarding = true
			} else {
				p.Pending = append(p.Pending, part...)
			}
		}
		if end < 0 {
			return
		}
		if !p.Discarding {
			p.line(bytes.TrimSpace(p.Pending), at)
		}
		p.Pending = nil
		p.Discarding = false
		chunk = chunk[end+1:]
	}
}
func (p *Projection) line(line []byte, at time.Time) {
	var e Event
	if json.Unmarshal(line, &e) != nil || e.Type != Protocol || e.Sequence <= p.Sequence {
		return
	}
	switch e.Kind {
	case "usage":
		if e.CostUSD != nil && *e.CostUSD < 0 {
			return
		}
		p.Summary.Tokens.Add(e.Tokens)
		p.Summary.Samples++
		if e.CostUSD != nil {
			p.Summary.CostSamples++
			p.Summary.CostUSD += *e.CostUSD
		}
		p.Summary.Provider = e.Provider
		p.Summary.Model = e.Model
	case "tool_started":
		if e.ToolID == "" {
			return
		}
		if p.Summary.Active == nil {
			p.Summary.Active = map[string]Tool{}
		}
		p.Summary.Active[e.ToolID] = Tool{Command: truncate(e.Command, 2048), StartedAt: at}
	case "tool_finished":
		if e.ToolID == "" {
			return
		}
		delete(p.Summary.Active, e.ToolID)
		if e.Failed {
			p.Summary.ToolFailures++
		}
	case "progress":
	default:
		return
	}
	p.Summary.Events++
	p.Sequence = e.Sequence
	p.Summary.LastProgressAt = &at
}
func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// Include aggregates a session without implying that absent usage equals zero.
func (s *Summary) Include(v Summary) {
	s.Events += v.Events
	s.Tokens.Add(v.Tokens)
	s.Samples += v.Samples
	s.CostSamples += v.CostSamples
	s.CostUSD += v.CostUSD
	s.ToolFailures += v.ToolFailures
	s.UnreportedSessions += v.UnreportedSessions
	if v.Samples == 0 {
		s.UnreportedSessions++
	}
	if v.LastProgressAt != nil && (s.LastProgressAt == nil || v.LastProgressAt.After(*s.LastProgressAt)) {
		s.LastProgressAt = v.LastProgressAt
	}
}
func (s Summary) Usage() string {
	if s.Samples == 0 {
		return "Tokens unknown"
	}
	text := fmt.Sprintf("Tokens %d · in %d · cache read %d / write %d · out %d", s.Tokens.Total(), s.Tokens.Input, s.Tokens.CacheRead, s.Tokens.CacheWrite, s.Tokens.Output)
	if s.UnreportedSessions > 0 {
		text += fmt.Sprintf(" · %d sessions unreported", s.UnreportedSessions)
	}
	return text
}

func (s Summary) Failures() string {
	if s.Events == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d reported", s.ToolFailures)
}
