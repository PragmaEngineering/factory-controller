package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"controls/internal/control"
	"controls/internal/reports"
	"controls/internal/testdb"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestReportCLIAndMCPFetchTheSameSavedSnapshot(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	store, err := control.Open(db)
	require.NoError(t, err)
	_, err = store.Create(control.NewExecution{ID: "report-run", Workflow: "sample"})
	require.NoError(t, err)
	document, err := store.Report(ctx, reports.Query{Factory: "sample"})
	require.NoError(t, err)
	client := control.NewClientWithHTTPClient(&http.Client{Transport: handlerTransport{handler: control.Handler(&control.Controller{Store: store})}})
	var out bytes.Buffer
	require.NoError(t, reportCommand(ctx, []string{"--snapshot", document.ID}, client, &out))
	require.Equal(t, document.HTML, out.String())
	server := operatorMCPServer(client)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "report-test", Version: "1"}, nil)
	session, err := mcpClient.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_report", Arguments: map[string]any{"report_id": document.ID, "format": "html"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	var payload struct {
		ReportID string `json:"report_id"`
		HTML     string `json:"html"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &payload))
	require.Equal(t, document.ID, payload.ReportID)
	require.Equal(t, document.HTML, payload.HTML)
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_report", Arguments: map[string]any{"format": "template"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "{{.Factory}}")
	out.Reset()
	require.NoError(t, reportCommand(ctx, []string{"-o", "template"}, client, &out))
	require.Contains(t, out.String(), "{{.Factory}}")
	require.ErrorContains(t, reportCommand(ctx, []string{"--factory", "sample", "--from", "invalid"}, client, &out), "UTC date")
}
