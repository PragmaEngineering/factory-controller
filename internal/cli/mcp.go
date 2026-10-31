package cli

import (
	"context"
	"encoding/json"
	"errors"

	"controls/internal/control"
	"controls/internal/reports"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The operator endpoint is a local stdio server, separate from the scoped
// agent messaging endpoint. Both CLI and MCP use the same controller report API.
func serveOperatorMCP(ctx context.Context, client *control.Client) error {
	return operatorMCPServer(client).Run(ctx, &mcp.StdioTransport{})
}

func operatorMCPServer(client *control.Client) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "factory-operator", Version: "0.1.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "get_report", Description: "Fetch a deterministic execution review or factory history report. History defaults to the last 90 days. Reuse report_id to retrieve an immutable snapshot. format: json (default), html, template. This never runs agents, tests or judges.", InputSchema: map[string]any{
		"type": "object", "additionalProperties": false, "properties": map[string]any{
			"factory": map[string]string{"type": "string"}, "execution_id": map[string]string{"type": "string"},
			"from": map[string]string{"type": "string", "format": "date-time"}, "to": map[string]string{"type": "string", "format": "date-time"},
			"report_id": map[string]string{"type": "string"}, "format": map[string]any{"type": "string", "enum": []string{"json", "html", "template"}},
		},
	}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			reports.Query
			Format string `json:"format"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return toolReportResult(nil, errors.New("invalid report query; times must be RFC3339"))
		}
		value, err := fetchReport(ctx, client, input.Query, input.Format)
		return toolReportResult(value, err)
	})
	return server
}
func fetchReport(ctx context.Context, client *control.Client, query reports.Query, format string) (any, error) {
	if format == "template" {
		source, err := client.ReportTemplate(ctx)
		return map[string]string{"template": source}, err
	}
	if format != "" && format != "json" && format != "html" {
		return nil, errors.New("invalid report format")
	}
	document, err := client.Report(ctx, query)
	if err != nil {
		return nil, err
	}
	if format == "html" {
		return map[string]string{"report_id": document.ID, "html": document.HTML}, nil
	}
	return document, nil
}
func toolReportResult(value any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: PublicError(err).Error()}}}, nil
	}
	visible, err := publicValue(value)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(visible)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: json.RawMessage(raw)}, nil
}
