package runtimehost

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"controls/adapters"
	"controls/adapters/pi"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/sessions"
	"github.com/google/uuid"
)

// The listener is a loopback origin; HTTPS termination may live in front of it.
func serveNetwork(ctx context.Context, root string, communication *comms.Service, host http.Handler) (func(), error) {
	address := os.Getenv("FACTORY_RUNTIME_LISTEN")
	if address == "" {
		return func() {}, nil
	}
	token, err := os.ReadFile(os.Getenv("FACTORY_RUNTIME_TOKEN_FILE"))
	if err != nil || len(strings.TrimSpace(string(token))) < 32 {
		return nil, fmt.Errorf("network runtime requires a token file containing at least 32 characters")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	if !listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		listener.Close()
		return nil, fmt.Errorf("bind the origin to loopback and expose it through an HTTPS reverse proxy")
	}
	public := strings.TrimRight(os.Getenv("FACTORY_RUNTIME_PUBLIC_URL"), "/")
	if public == "" {
		public = "http://" + listener.Addr().String()
	}
	endpoint, err := url.Parse(public)
	if err != nil || endpoint.Host == "" || endpoint.Path != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "https" && public != "http://"+listener.Addr().String()) {
		listener.Close()
		return nil, fmt.Errorf("public URL must be HTTPS, or the local listener's HTTP URL")
	}
	communication.URL = public + "/mcp"
	peer := communication.MCPHandlerForHost(endpoint.Host)
	expected := sha256.Sum256([]byte(strings.TrimSpace(string(token))))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" || (r.Host != endpoint.Host && r.Host != listener.Addr().String()) {
			http.Error(w, "invalid origin or host", 403)
			return
		}
		if r.URL.Path == "/mcp" {
			peer.ServeHTTP(w, r)
			return
		}
		actual := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			http.Error(w, "credential required", 401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		host.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	raw, _ := json.Marshal(map[string]any{"url": public, "pid": os.Getpid()})
	if err = os.WriteFile(filepath.Join(root, "endpoint.json"), raw, 0600); err != nil {
		server.Close()
		return nil, err
	}
	return func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}, nil
}

// Invocation material is staged on the execution host, never via shared paths.
func workspaceHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/workspaces/")
		if _, err := uuid.Parse(id); err != nil || strings.ContainsAny(id, "/\\") {
			http.Error(w, "invalid workspace id", 400)
			return
		}
		var request struct {
			Files map[string]string `json:"files"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&request) != nil {
			http.Error(w, "invalid files", 400)
			return
		}
		directory := filepath.Join(root, "workspaces", id)
		if err := os.MkdirAll(directory, 0700); err != nil {
			http.Error(w, "workspace creation failed", 500)
			return
		}
		allowed := map[string]bool{"input.json": true, "config.json": true, "prompt.txt": true, "context.json": true, "skills/fabric/SKILL.md": true}
		for name := range request.Files {
			if !allowed[name] {
				http.Error(w, "unsupported workspace file", 400)
				return
			}
		}
		for name, content := range request.Files {
			path := filepath.Join(directory, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				http.Error(w, "workspace write failed", 500)
				return
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				http.Error(w, "workspace file already exists or cannot be created", 409)
				return
			}
			_, err = file.WriteString(content)
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				http.Error(w, "workspace write failed", 500)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"directory": directory})
	})
}

func launchHandler(manager *sessions.Manager, root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var request struct {
			Session       sessions.Launch            `json:"session"`
			References    map[string]string          `json:"environment_references"`
			Runtime       *execution.NativeRuntime   `json:"runtime,omitempty"`
			PackagePath   string                     `json:"package_path,omitempty"`
			Configuration map[string]json.RawMessage `json:"configuration,omitempty"`
			AssignmentID  string                     `json:"assignment_id,omitempty"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&request) != nil {
			http.Error(w, "invalid launch", 400)
			return
		}
		if request.Session.Environment == nil {
			request.Session.Environment = map[string]string{}
		}
		for target, source := range request.References {
			if strings.HasPrefix(target, "AGENT_MCP_") || strings.HasPrefix(target, "FABRIC_") || strings.HasPrefix(target, "FACTORY_") {
				http.Error(w, "reserved environment target", 400)
				return
			}
			value, ok := os.LookupEnv(source)
			if !ok {
				http.Error(w, "missing execution-host environment reference", 400)
				return
			}
			request.Session.Environment[target] = value
		}
		if request.Runtime != nil {
			if len(request.Session.Command) == 0 {
				http.Error(w, "native launch requires a command", 400)
				return
			}
			if _, err := uuid.Parse(request.AssignmentID); err != nil {
				http.Error(w, "native launch requires assignment id", 400)
				return
			}
			settings, _ := json.Marshal(map[string]any{"overrides": request.Configuration, "args": request.Session.Command[1:]})
			adapter, err := (adapters.Registry{"command": adapters.Command{}, "pi": pi.Adapter{}}).Select(request.Runtime, false)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			prepared, err := adapter.Prepare(r.Context(), adapters.Request{Package: execution.PackageSnapshot{Name: request.Session.Name, Source: request.PackagePath, Runtime: request.Runtime, Settings: settings}, Command: request.Session.Command, Project: request.Session.Directory, InvocationDir: filepath.Join(root, "native", request.AssignmentID)})
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			request.Session.Command = prepared.Command
			request.Session.InputMode = prepared.InputMode
			request.Session.Runtime = &prepared.Runtime
			for name, value := range prepared.Environment {
				request.Session.Environment[name] = value
			}
			request.Session.Environment["FACTORY_RUNTIME_NAME"] = prepared.Runtime.Name
		}
		result, err := manager.Launch(request.Session)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(result)
	})
}
