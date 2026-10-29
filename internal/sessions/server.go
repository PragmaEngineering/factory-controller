package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"controls/internal/state"
)

// RuntimeRoot locates the single local service for this user. Project paths and
// inherited shell variables must not select a different controller/database.
func RuntimeRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".factory", "runtime"), nil
}

func SocketPath(root string) string { return filepath.Join(root, "factory.sock") }

// Serve listens only on a private local Unix socket. The caller controls its
// lifetime; a separate CLI process can connect while agents continue running.
func Serve(ctx context.Context, root string, db *state.Store) error {
	return ServeWithHandler(ctx, root, db, func(manager *Manager) (http.Handler, error) {
		return Handler(manager), nil
	})
}

// ServeWithHandler lets the Factory controller share the supervisor's socket
// and process lifecycle while keeping session handling independently usable.
func ServeWithHandler(ctx context.Context, root string, db *state.Store, build func(*Manager) (http.Handler, error)) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	socket := SocketPath(root)
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("socket path %s exists and is not a socket", socket)
		}
		connection, dialErr := net.DialTimeout("unix", socket, 250*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return errors.New("Factory supervisor is already running")
		}
		if err := os.Remove(socket); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	manager, err := Open(filepath.Join(root, "sessions"), socket, db)
	if err != nil {
		return err
	}
	handler, err := build(manager)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func Handler(manager *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(w, http.StatusOK, map[string]bool{"ready": true})
	})
	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, r *http.Request) {
		items, err := manager.List(r.URL.Query().Get("execution_id"))
		writeResult(w, http.StatusOK, items, err)
	})
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		var request Launch
		if !decodeRequest(w, r, &request) {
			return
		}
		result, err := manager.Launch(request)
		writeResult(w, http.StatusCreated, result, err)
	})
	mux.HandleFunc("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := manager.Get(r.PathValue("id"))
		writeResult(w, http.StatusOK, result, err)
	})
	mux.HandleFunc("GET /sessions/{id}/output", func(w http.ResponseWriter, r *http.Request) {
		offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "offset must be an integer")
			return
		}
		result, err := manager.Output(r.PathValue("id"), offset, 65536)
		writeResult(w, http.StatusOK, result, err)
	})
	mux.HandleFunc("GET /sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		result, err := manager.Messages(r.PathValue("id"))
		writeResult(w, http.StatusOK, result, err)
	})
	mux.HandleFunc("POST /sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Body string `json:"body"`
		}
		if !decodeRequest(w, r, &request) {
			return
		}
		result, err := manager.Send(r.PathValue("id"), request.Body)
		writeResult(w, http.StatusCreated, result, err)
	})
	mux.HandleFunc("GET /sessions/{id}/inbox", func(w http.ResponseWriter, r *http.Request) {
		result, err := manager.Inbox(r.PathValue("id"), bearer(r))
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		writeJSONResponse(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /sessions/{id}/ack", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MessageID string `json:"message_id"`
		}
		if !decodeRequest(w, r, &request) {
			return
		}
		result, err := manager.Acknowledge(r.PathValue("id"), bearer(r), request.MessageID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		writeJSONResponse(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /sessions/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Stop(r.PathValue("id")); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]bool{"stopping": true})
	})
	return mux
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func decodeRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, success int, result any, err error) {
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSONResponse(w, success, result)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSONResponse(w, status, map[string]string{"error": message})
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
