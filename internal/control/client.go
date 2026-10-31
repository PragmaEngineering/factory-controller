package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"controls/execution"
	"controls/internal/comms"
	"controls/internal/reports"
	"controls/internal/state"
)

type Client struct{ http *http.Client }

// NewClientWithHTTPClient permits a caller to supply a transport, including
// an in-process transport for tests or a future non-Unix local host.
func NewClientWithHTTPClient(client *http.Client) *Client { return &Client{http: client} }

func NewClient(socket string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 45 * time.Second}}
}

func (c *Client) Version(ctx context.Context) error {
	var value struct {
		API string `json:"api"`
	}
	if err := c.call(ctx, http.MethodGet, "/version", nil, &value); err != nil {
		return err
	}
	if value.API != "factory-control-v3" {
		return fmt.Errorf("unsupported Factory controller API %q", value.API)
	}
	return nil
}

func (c *Client) GetContext(ctx context.Context) (struct {
	ID      string   `json:"id"`
	Context *Context `json:"context"`
}, error) {
	var value struct {
		ID      string   `json:"id"`
		Context *Context `json:"context"`
	}
	err := c.call(ctx, http.MethodGet, "/context", nil, &value)
	return value, err
}

func (c *Client) SetContext(ctx context.Context, id string) (struct {
	ID      string   `json:"id"`
	Context *Context `json:"context"`
}, error) {
	var value struct {
		ID      string   `json:"id"`
		Context *Context `json:"context"`
	}
	err := c.call(ctx, http.MethodPut, "/context", map[string]string{"id": id}, &value)
	return value, err
}

func (c *Client) Contexts(ctx context.Context) ([]Context, error) {
	var value []Context
	err := c.call(ctx, http.MethodGet, "/contexts", nil, &value)
	return value, err
}

func (c *Client) Exec(ctx context.Context, request ExecRequest) (Execution, error) {
	var value Execution
	err := c.call(ctx, http.MethodPost, "/executions", request, &value)
	return value, err
}

func (c *Client) List(ctx context.Context, contextID string) ([]Execution, error) {
	var value []Execution
	path := "/executions"
	if contextID != "" {
		path += "?context=" + url.QueryEscape(contextID)
	}
	err := c.call(ctx, http.MethodGet, path, nil, &value)
	return value, err
}

func (c *Client) Resource(ctx context.Context, id string) (Resource, error) {
	var value Resource
	err := c.call(ctx, http.MethodGet, "/resources/"+url.PathEscape(id), nil, &value)
	return value, err
}

func (c *Client) Logs(ctx context.Context, id string) (string, error) {
	var value struct {
		Text string `json:"text"`
	}
	err := c.call(ctx, http.MethodGet, "/resources/"+url.PathEscape(id)+"/logs", nil, &value)
	return value.Text, err
}

func (c *Client) Events(ctx context.Context, id string) ([]Event, error) {
	var value []Event
	err := c.call(ctx, http.MethodGet, "/resources/"+url.PathEscape(id)+"/events", nil, &value)
	return value, err
}

func (c *Client) Stream(ctx context.Context, id string, after int64) ([]state.Activity, error) {
	var value []state.Activity
	path := fmt.Sprintf("/resources/%s/stream?after=%d", url.PathEscape(id), after)
	err := c.call(ctx, http.MethodGet, path, nil, &value)
	return value, err
}

func (c *Client) Inbox(ctx context.Context, contextID string) ([]Request, error) {
	var value []Request
	path := "/inbox"
	if contextID != "" {
		path += "?context=" + url.QueryEscape(contextID)
	}
	err := c.call(ctx, http.MethodGet, path, nil, &value)
	return value, err
}

func (c *Client) Answer(ctx context.Context, id string, outcome execution.Outcome, reason, actor string) (Execution, error) {
	var value Execution
	err := c.call(ctx, http.MethodPost, "/requests/"+url.PathEscape(id)+"/answer", map[string]any{
		"outcome": outcome, "reason": reason, "actor": actor,
	}, &value)
	return value, err
}

func (c *Client) Retry(ctx context.Context, id, reason, actor string) (Execution, error) {
	var value Execution
	err := c.call(ctx, http.MethodPost, "/steps/"+url.PathEscape(id)+"/retry", map[string]string{
		"reason": reason, "actor": actor,
	}, &value)
	return value, err
}

func (c *Client) Cancel(ctx context.Context, id string) (Execution, error) {
	var value Execution
	err := c.call(ctx, http.MethodPost, "/executions/"+url.PathEscape(id)+"/cancel", struct{}{}, &value)
	return value, err
}

func (c *Client) call(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://factory"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) != nil || failure.Error == "" {
			return fmt.Errorf("Factory returned %s", response.Status)
		}
		return errors.New(failure.Error)
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}

func (c *Client) Messages(ctx context.Context, id string) ([]comms.Message, error) {
	var messages []comms.Message
	err := c.call(ctx, http.MethodGet, "/executions/"+url.PathEscape(id)+"/messages", nil, &messages)
	return messages, err
}

func (c *Client) Report(ctx context.Context, query reports.Query) (reports.Document, error) {
	var document reports.Document
	err := c.call(ctx, http.MethodPost, "/reports", query, &document)
	return document, err
}
func (c *Client) ReportTemplate(ctx context.Context) (string, error) {
	var value struct {
		Template string `json:"template"`
	}
	err := c.call(ctx, http.MethodGet, "/report-template", nil, &value)
	return value.Template, err
}
