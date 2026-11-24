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
	Reason               string   `json:"reason,omitempty"`
	Aborted              bool     `json:"aborted,omitempty"`
	TokensBefore         *uint64  `json:"tokens_before,omitempty"`
	EstimatedTokensAfter *uint64  `json:"estimated_tokens_after,omitempty"`
	Turn                 uint64   `json:"turn,omitempty"`
	Type                 string   `json:"type"`
	Sequence             uint64   `json:"sequence"`
	Kind                 string   `json:"kind"`
	Tokens               Tokens   `json:"tokens"`
	CostUSD              *float64 `json:"cost_usd,omitempty"`
	Provider             string   `json:"provider,omitempty"`
	Model                string   `json:"model,omitempty"`
	ToolID               string   `json:"tool_id,omitempty"`
	Command              string   `json:"command,omitempty"`
	Failed               bool     `json:"failed,omitempty"`
	ToolName             string   `json:"tool_name,omitempty"`
	Arguments            string   `json:"arguments,omitempty"`
	Output               string   `json:"output,omitempty"`
	Error                string   `json:"error,omitempty"`
	Truncated            bool     `json:"truncated,omitempty"`
	ExitCode             *int     `json:"exit_code,omitempty"`
	ContextWindow        uint64   `json:"context_window,omitempty"`
	StopReason           string   `json:"stop_reason,omitempty"`
}
type Response struct {
	Turn          uint64    `json:"turn,omitempty"`
	Number        uint64    `json:"number"`
	At            time.Time `json:"at"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	Tokens        Tokens    `json:"tokens"`
	CostUSD       *float64  `json:"cost_usd,omitempty"`
	ContextWindow uint64    `json:"context_window,omitempty"`
	StopReason    string    `json:"stop_reason,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// Occupancy is the completed response footprint, not cumulative session spend.
func (r Response) Occupancy() string {
	if r.ContextWindow == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(r.Tokens.Total())/float64(r.ContextWindow))
}

type Failure struct {
	ToolID string `json:"tool_id"`
	Tool
	FinishedAt time.Time `json:"finished_at"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	Truncated  bool      `json:"truncated,omitempty"`
}
type Boundary struct {
	Reason               string    `json:"reason,omitempty"`
	Aborted              bool      `json:"aborted,omitempty"`
	TokensBefore         *uint64   `json:"tokens_before,omitempty"`
	EstimatedTokensAfter *uint64   `json:"estimated_tokens_after,omitempty"`
	Turn                 uint64    `json:"turn,omitempty"`
	Kind                 string    `json:"kind"`
	At                   time.Time `json:"at"`
	Error                string    `json:"error,omitempty"`
}
type Diagnostics struct {
	Responses  []Response `json:"responses,omitempty"`
	Failures   []Failure  `json:"failures,omitempty"`
	Boundaries []Boundary `json:"boundaries,omitempty"`
}
type Tool struct {
	Turn      uint64    `json:"turn,omitempty"`
	Name      string    `json:"name,omitempty"`
	Arguments string    `json:"arguments,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
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
	Summary     Summary     `json:"summary"`
	Diagnostics Diagnostics `json:"diagnostics"`
	Sequence    uint64      `json:"sequence"`
	Pending     []byte      `json:"pending,omitempty"`
	Discarding  bool        `json:"discarding,omitempty"`
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
		p.Diagnostics.Responses = append(p.Diagnostics.Responses, Response{Turn: e.Turn, Number: p.Summary.Samples, At: at, Provider: e.Provider, Model: e.Model, Tokens: e.Tokens, CostUSD: e.CostUSD, ContextWindow: e.ContextWindow, StopReason: e.StopReason, Error: truncate(e.Error, 16384)})
	case "tool_started":
		if e.ToolID == "" {
			return
		}
		if p.Summary.Active == nil {
			p.Summary.Active = map[string]Tool{}
		}
		p.Summary.Active[e.ToolID] = Tool{Turn: e.Turn, Name: truncate(e.ToolName, 256), Arguments: truncate(e.Arguments, 16384), Truncated: e.Truncated || len([]rune(e.Arguments)) > 16384 || len([]rune(e.Command)) > 2048, Command: truncate(e.Command, 2048), StartedAt: at}
	case "tool_finished":
		if e.ToolID == "" {
			return
		}
		active := p.Summary.Active[e.ToolID]
		delete(p.Summary.Active, e.ToolID)
		if e.Failed {
			p.Summary.ToolFailures++
			if active.Name == "" {
				active.Name = truncate(e.ToolName, 256)
			}
			p.Diagnostics.Failures = append(p.Diagnostics.Failures, Failure{ToolID: e.ToolID, Tool: active, FinishedAt: at, Output: truncate(e.Output, 16384), Error: truncate(e.Error, 16384), ExitCode: e.ExitCode, Truncated: active.Truncated || e.Truncated || len([]rune(e.Output)) > 16384 || len([]rune(e.Error)) > 16384})
		}
	case "turn_started", "turn_finished", "compaction_started", "compaction_finished":
		p.Diagnostics.Boundaries = append(p.Diagnostics.Boundaries, Boundary{Reason: e.Reason, Aborted: e.Aborted, TokensBefore: e.TokensBefore, EstimatedTokensAfter: e.EstimatedTokensAfter, Turn: e.Turn, Kind: e.Kind, At: at, Error: truncate(e.Error, 16384)})
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

func (d Diagnostics) count(kind string) uint64 {
	var n uint64
	for _, b := range d.Boundaries {
		if b.Kind == kind {
			n++
		}
	}
	return n
}
func (d Diagnostics) TurnsStarted() uint64        { return d.count("turn_started") }
func (d Diagnostics) TurnsFinished() uint64       { return d.count("turn_finished") }
func (d Diagnostics) CompactionsStarted() uint64  { return d.count("compaction_started") }
func (d Diagnostics) CompactionsFinished() uint64 { return d.count("compaction_finished") }
func (d Diagnostics) PeakFootprint() string {
	if len(d.Responses) == 0 {
		return "unknown"
	}
	var n uint64
	for _, r := range d.Responses {
		if r.Tokens.Total() > n {
			n = r.Tokens.Total()
		}
	}
	return fmt.Sprint(n)
}
func (d Diagnostics) PeakOccupancy() string {
	var peak float64
	known := false
	for _, r := range d.Responses {
		if r.ContextWindow > 0 {
			known = true
			v := 100 * float64(r.Tokens.Total()) / float64(r.ContextWindow)
			if v > peak {
				peak = v
			}
		}
	}
	if !known {
		return "unknown"
	}
	return fmt.Sprintf("%.1f%%", peak)
}

func (f Failure) Duration() string {
	if f.StartedAt.IsZero() {
		return "unknown"
	}
	return f.FinishedAt.Sub(f.StartedAt).Round(time.Millisecond).String()
}
