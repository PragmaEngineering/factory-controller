package telemetry

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestProjectionSurvivesFramingRestartAndDuplicateEvents(t *testing.T) {
	line := `{"type":"factory.telemetry.v1","sequence":1,"kind":"usage","tokens":{"input":13,"cache_read":70,"cache_write":5,"output":12}}` + "\r\n"
	var p Projection
	p.Append([]byte(line[:40]), time.Now())
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var recovered Projection
	require.NoError(t, json.Unmarshal(raw, &recovered))
	recovered.Append([]byte(line[40:]+line), time.Now())
	require.EqualValues(t, 1, recovered.Summary.Samples)
	require.EqualValues(t, 100, recovered.Summary.Tokens.Total())
	require.Equal(t, "unknown", recovered.Summary.Cost())
	recovered.Append([]byte(`{"type":"factory.telemetry.v1","sequence":2,"kind":"usage","tokens":{"input":1},"cost_usd":0.01}`+"\n"), time.Now())
	require.Equal(t, "unknown", recovered.Summary.Cost(), "one priced response cannot price the unpriced one")
}
func TestToolsAndProgressAreIndependentOfOrdinaryOutput(t *testing.T) {
	var p Projection
	at := time.Now().UTC()
	p.Append([]byte(`{"type":"factory.telemetry.v1","sequence":1,"kind":"tool_started","tool_id":"a","command":"go test ./..."}`+"\n"), at)
	p.Append([]byte(`{"type":"factory.telemetry.v1","sequence":2,"kind":"tool_started","tool_id":"b","command":"go vet ./..."}`+"\n"), at.Add(time.Second))
	require.Equal(t, "go vet ./...", p.Summary.Command())
	p.Append([]byte("thinking and terminal chatter\n"), at.Add(time.Minute))
	require.Equal(t, at.Add(time.Second), *p.Summary.LastProgressAt)
	p.Append([]byte(`{"type":"factory.telemetry.v1","sequence":3,"kind":"tool_finished","tool_id":"b","failed":true}`+"\n"), at.Add(2*time.Second))
	require.Equal(t, "go test ./...", p.Summary.Command())
	require.EqualValues(t, 1, p.Summary.ToolFailures)
}
func TestProjectionRejectsBadAndOversizedLinesThenRecovers(t *testing.T) {
	var p Projection
	p.Append([]byte(strings.Repeat("x", MaxLine+1)), time.Now())
	require.Empty(t, p.Pending)
	require.True(t, p.Discarding)
	p.Append([]byte("\n"+`{"type":"factory.telemetry.v1","sequence":1,"kind":"usage","tokens":{"input":-1}}`+"\n"), time.Now())
	require.Zero(t, p.Summary.Samples)
	p.Append([]byte(`{"type":"factory.telemetry.v1","sequence":2,"kind":"usage","tokens":{"input":2},"cost_usd":0.02}`+"\n"), time.Now())
	require.EqualValues(t, 2, p.Summary.Tokens.Input)
	require.Equal(t, "$0.0200 (reported)", p.Summary.Cost())
	var total Summary
	total.Include(p.Summary)
	total.Include(Summary{})
	require.EqualValues(t, 1, total.UnreportedSessions)
	require.Equal(t, "unknown", total.Cost())
}
