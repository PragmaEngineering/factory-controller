package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client speaks to the supervisor over its private Unix socket. It is also
// usable by the future Hatchet package launcher; no terminal host is assumed.
type Client struct {
	http *http.Client
}

func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
}

func (c *Client) Health(ctx context.Context) error {
	return c.call(ctx, http.MethodGet, "/health", nil, "", nil)
}

func (c *Client) List(ctx context.Context, executionID string) ([]Session, error) {
	path := "/sessions"
	if executionID != "" {
		path += "?execution_id=" + url.QueryEscape(executionID)
	}
	var sessions []Session
	err := c.call(ctx, http.MethodGet, path, nil, "", &sessions)
	return sessions, err
}

func (c *Client) Launch(ctx context.Context, request Launch) (Session, error) {
	var session Session
	err := c.call(ctx, http.MethodPost, "/sessions", request, "", &session)
	return session, err
}

func (c *Client) Output(ctx context.Context, id string, offset int64) (Output, error) {
	var output Output
	err := c.call(ctx, http.MethodGet, "/sessions/"+url.PathEscape(id)+"/output?offset="+strconv.FormatInt(offset, 10), nil, "", &output)
	return output, err
}

func (c *Client) Messages(ctx context.Context, id string) ([]Message, error) {
	var messages []Message
	err := c.call(ctx, http.MethodGet, "/sessions/"+url.PathEscape(id)+"/messages", nil, "", &messages)
	return messages, err
}

func (c *Client) Send(ctx context.Context, id, body string) (Message, error) {
	var message Message
	err := c.call(ctx, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/messages", map[string]string{"body": body}, "", &message)
	return message, err
}

func (c *Client) Inbox(ctx context.Context, id, token string) ([]Message, error) {
	var messages []Message
	err := c.call(ctx, http.MethodGet, "/sessions/"+url.PathEscape(id)+"/inbox", nil, token, &messages)
	return messages, err
}

func (c *Client) Acknowledge(ctx context.Context, id, token, messageID string) (Message, error) {
	var message Message
	err := c.call(ctx, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/ack", map[string]string{"message_id": messageID}, token, &message)
	return message, err
}

func (c *Client) Stop(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/stop", struct{}{}, "", nil)
}

func (c *Client) call(ctx context.Context, method, path string, input any, token string, output any) error {
	var body *bytes.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	} else {
		body = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://factory"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
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
			return fmt.Errorf("supervisor returned %s", response.Status)
		}
		return errors.New(failure.Error)
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}
