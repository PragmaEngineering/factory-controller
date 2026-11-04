// A non-LLM reference worker. It uses ordinary MCP, no Pi files/events and no
// provider. It stays alive after completion so the supervisor must clean it up.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type credentials struct{ token string }

func (c credentials) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+c.token)
	return http.DefaultTransport.RoundTrip(r)
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("1.0.0")
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	var config struct {
		Mode    string `json:"mode"`
		Message string `json:"message"`
	}
	raw, err := os.ReadFile(os.Getenv("FIXTURE_CONFIG"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	prompt, err := os.ReadFile(os.Getenv("FACTORY_PROMPT_PATH"))
	if err != nil || len(prompt) == 0 {
		return errors.New("fixture assignment missing")
	}
	inputs, err := os.ReadFile(os.Getenv("FACTORY_INPUTS_PATH"))
	if err != nil || !json.Valid(inputs) {
		return errors.New("fixture bound inputs missing")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Println("fixture ready")
	if config.Mode == "fail" {
		return errors.New("fixture native failure")
	}
	if config.Mode == "wait" {
		for {
			time.Sleep(time.Second)
		}
	}
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "independent-fixture", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: os.Getenv("FACTORY_MCP_URL"), HTTPClient: &http.Client{Transport: credentials{os.Getenv("FACTORY_MCP_TOKEN")}}}, nil)
	if err != nil {
		return err
	}
	defer session.Close()
	call := func(name string, args any, target any) error {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("fixture MCP %s rejected", name)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, target)
	}
	thread := os.Getenv("FACTORY_THREAD_ID")
	self := os.Getenv("FACTORY_PARTICIPANT_ID")
	var listed struct {
		Participants []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"participants"`
	}
	if err := call("list_participants", map[string]any{"thread_id": thread}, &listed); err != nil {
		return err
	}
	controller := ""
	peers := []string{}
	for _, p := range listed.Participants {
		if p.Kind == "controller" {
			controller = p.ID
		}
		if p.Kind == "agent" && p.ID != self {
			peers = append(peers, p.ID)
		}
	}
	if controller == "" {
		return errors.New("fixture controller missing")
	}
	send := func(id, kind string, recipients []string, data any, target any) error {
		return call("send_message", map[string]any{"operation_id": id, "thread_id": thread, "recipients": recipients, "payload": map[string]any{"kind": kind, "version": 1, "parts": []any{map[string]any{"kind": "data", "data": data}}}}, target)
	}
	if config.Mode == "peer" {
		if len(peers) == 0 {
			return errors.New("fixture peer missing")
		}
		var receipt any
		if err := send(self+":peer", "progress", peers, map[string]any{"summary": "fixture peer ready"}, &receipt); err != nil {
			return err
		}
		cursor := ""
		received := false
		for !received {
			var batch struct {
				Cursor   string `json:"cursor"`
				Messages []struct {
					Sender  string `json:"sender"`
					Payload struct {
						Kind string `json:"kind"`
					} `json:"payload"`
				} `json:"messages"`
			}
			if err := call("wait_for_message", map[string]any{"thread_id": thread, "after": cursor, "wait_seconds": 1}, &batch); err != nil {
				return err
			}
			cursor = batch.Cursor
			for _, m := range batch.Messages {
				if m.Sender != self && m.Payload.Kind == "progress" {
					received = true
				}
			}
		}
	}
	var receipt struct {
		Effect struct {
			State string `json:"state"`
		} `json:"effect"`
	}
	if err := send(self+":result", "step_result", []string{controller}, map[string]any{"outcome": "ready", "summary": config.Message, "data": map[string]any{"invocation_dir": os.Getenv("FACTORY_INVOCATION_DIR"), "inputs": json.RawMessage(inputs), "working_directory": cwd}}, &receipt); err != nil {
		return err
	}
	if receipt.Effect.State != "accepted" {
		return errors.New("fixture result not accepted")
	}
	fmt.Println("fixture accepted")
	for {
		time.Sleep(time.Second)
	}
}
