// Package reports renders immutable, runner-neutral control-plane snapshots.
// Rendering performs no database, filesystem, network, clock or model calls.
package reports

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"time"

	"controls/internal/telemetry"
)

const Version = 2

//go:embed assets/*
var assets embed.FS

type Query struct {
	Factory     string     `json:"factory,omitempty"`
	ExecutionID string     `json:"execution_id,omitempty"`
	From        *time.Time `json:"from,omitempty"`
	To          *time.Time `json:"to,omitempty"`
	ReportID    string     `json:"report_id,omitempty"`
}
type Phase struct {
	Name       string     `json:"name"`
	Visit      uint       `json:"visit"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
type Attempt struct {
	Try        uint       `json:"try"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}
type Step struct {
	Phase
	ID        string            `json:"id"`
	Stage     string            `json:"stage,omitempty"`
	Attempts  []Attempt         `json:"attempts,omitempty"`
	Resources telemetry.Summary `json:"resources"`
	Error     string            `json:"error,omitempty"`
}
type Assessment struct {
	Source  string    `json:"source"`
	Step    string    `json:"step"`
	Actor   string    `json:"actor"`
	Outcome string    `json:"outcome"`
	Summary string    `json:"summary,omitempty"`
	At      time.Time `json:"at"`
}
type Agent struct {
	Resources   telemetry.Summary     `json:"resources,omitzero"`
	Diagnostics telemetry.Diagnostics `json:"diagnostics,omitzero"`
	Name        string                `json:"name"`
	SessionID   string                `json:"session_id"`
	StepID      string                `json:"step_id"`
	Runner      string                `json:"runner"`
	Provider    string                `json:"provider,omitempty"`
	Model       string                `json:"model,omitempty"`
}
type Hook struct {
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	Boundary   string     `json:"boundary"`
	State      string     `json:"state"`
	Try        uint       `json:"try"`
	Error      string     `json:"error,omitempty"`
	SessionID  string     `json:"session_id,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
type Execution struct {
	Error        string            `json:"error,omitempty"`
	Hooks        []Hook            `json:"hooks,omitempty"`
	ID           string            `json:"id"`
	Factory      string            `json:"factory"`
	ConfigDigest string            `json:"config_digest"`
	State        string            `json:"state"`
	CreatedAt    time.Time         `json:"created_at"`
	FinishedAt   *time.Time        `json:"finished_at,omitempty"`
	Resources    telemetry.Summary `json:"resources"`
	Stages       []Phase           `json:"stages,omitempty"`
	Steps        []Step            `json:"steps,omitempty"`
	Agents       []Agent           `json:"agents,omitempty"`
	Assessments  []Assessment      `json:"assessments,omitempty"`
}
type Statistics struct {
	Executions           int      `json:"executions"`
	Completed            int      `json:"completed"`
	UsageN               int      `json:"usage_n"`
	MeanTokens           *float64 `json:"mean_tokens"`
	CompletedTimeN       int      `json:"completed_time_n"`
	MeanCompletedSeconds *float64 `json:"mean_completed_seconds"`
}
type Snapshot struct {
	TemplateDigest string      `json:"template_digest"`
	Version        int         `json:"version"`
	Accounting     string      `json:"accounting"`
	AsOf           time.Time   `json:"as_of"`
	From           time.Time   `json:"from"`
	To             time.Time   `json:"to"`
	Factory        string      `json:"factory"`
	Selected       *Execution  `json:"selected,omitempty"`
	History        []Execution `json:"history"`
	Statistics     Statistics  `json:"statistics"`
}
type Document struct {
	ID       string   `json:"id"`
	Snapshot Snapshot `json:"snapshot"`
	HTML     string   `json:"html"`
}

func (q Query) Window(now time.Time) (time.Time, time.Time, error) {
	to := now.UTC()
	if q.To != nil {
		to = q.To.UTC()
	}
	from := to.AddDate(0, 0, -90)
	if q.From != nil {
		from = q.From.UTC()
	}
	if !from.Before(to) {
		return from, to, fmt.Errorf("report from must precede to")
	}
	if to.After(now) {
		return from, to, fmt.Errorf("report to must not be in the future")
	}
	return from, to, nil
}

func Summarize(history []Execution) Statistics {
	s := Statistics{Executions: len(history)}
	var tokens, seconds float64
	for _, r := range history {
		if r.Resources.Samples > 0 {
			s.UsageN++
			tokens += float64(r.Resources.Tokens.Total())
		}
		if r.State == "completed" {
			s.Completed++
			if r.FinishedAt != nil {
				s.CompletedTimeN++
				seconds += r.FinishedAt.Sub(r.CreatedAt).Seconds()
			}
		}
	}
	if s.UsageN > 0 {
		v := tokens / float64(s.UsageN)
		s.MeanTokens = &v
	}
	if s.CompletedTimeN > 0 {
		v := seconds / float64(s.CompletedTimeN)
		s.MeanCompletedSeconds = &v
	}
	return s
}

func Template() (string, error) {
	raw, err := assets.ReadFile("assets/report.html")
	if err != nil {
		return "", err
	}
	css, err := assets.ReadFile("assets/document.css")
	if err != nil {
		return "", err
	}
	extra, err := assets.ReadFile("assets/report.css")
	if err != nil {
		return "", err
	}
	raw = bytes.ReplaceAll(raw, []byte("/*DOCUMENT_STYLE*/"), css)
	return string(bytes.ReplaceAll(raw, []byte("/*REPORT_STYLE*/"), extra)), nil
}
func Build(snapshot Snapshot) (Document, error) {
	// Stable ordering is part of the serialized report contract.
	sort.Slice(snapshot.History, func(i, j int) bool {
		a, b := snapshot.History[i], snapshot.History[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	snapshot.Version = Version
	snapshot.Accounting = "factory.telemetry.v1: input + cache_read + cache_write + output"
	snapshot.Statistics = Summarize(snapshot.History)
	source, err := Template()
	if err != nil {
		return Document{}, err
	}
	templateSum := sha256.Sum256([]byte(source))
	snapshot.TemplateDigest = hex.EncodeToString(templateSum[:])
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return Document{}, err
	}
	sum := sha256.Sum256(raw)
	id := hex.EncodeToString(sum[:])
	max := uint64(1)
	for _, r := range snapshot.History {
		if r.Resources.Tokens.Total() > max {
			max = r.Resources.Tokens.Total()
		}
	}
	funcs := template.FuncMap{
		"tokens": func(s telemetry.Summary) string {
			if s.Samples == 0 {
				return "unknown"
			}
			return fmt.Sprintf("%d", s.Tokens.Total())
		},
		"duration": func(start time.Time, end *time.Time) string {
			if end == nil {
				return "end not recorded"
			}
			return end.Sub(start).Round(time.Second).String()
		},
		"number": func(v *float64) string {
			if v == nil {
				return "unknown"
			}
			return fmt.Sprintf("%.1f", *v)
		},
		"time": func(v time.Time) string { return v.UTC().Format("2006-01-02 15:04:05Z") },
		"width": func(s telemetry.Summary) string {
			return fmt.Sprintf("%.3f%%", float64(s.Tokens.Total())/float64(max)*100)
		},
	}
	tmpl, err := template.New("report").Funcs(funcs).Parse(source)
	if err != nil {
		return Document{}, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		Snapshot
		ID string
	}{snapshot, id}); err != nil {
		return Document{}, err
	}
	return Document{ID: id, Snapshot: snapshot, HTML: out.String()}, nil
}
