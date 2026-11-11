// Package runtimehost serves communication and process supervision without a
// workflow scheduler. PostgreSQL is supplied explicitly by the host.
package runtimehost

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"controls/internal/comms"
	"controls/internal/sessions"
	"controls/internal/state"
)

func Serve(ctx context.Context, root string) error {
	if os.Getenv("FACTORY_DATABASE_URL") == "" {
		return errors.New("standalone runtime requires FACTORY_DATABASE_URL")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	lease, err := os.OpenFile(filepath.Join(root, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("runtime directory is already in use")
	}
	defer syscall.Flock(int(lease.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	db, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	communication, err := comms.New(ctx, db, nil)
	if err != nil {
		return err
	}
	stop, err := communication.Listen(ctx)
	if err != nil {
		return err
	}
	defer stop()
	var manager *sessions.Manager
	defer func() {
		if manager != nil {
			shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			_ = manager.Shutdown(shutdown)
		}
	}()
	return sessions.ServeWithHandler(ctx, root, db, func(m *sessions.Manager) (http.Handler, error) {
		manager = m
		host := communication.HostHandler()
		session := sessions.Handler(m)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/shutdown" {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				cancel()
				return
			}
			if r.URL.Path == "/comms" || strings.HasPrefix(r.URL.Path, "/comms/") {
				host.ServeHTTP(w, r)
				return
			}
			session.ServeHTTP(w, r)
		}), nil
	})
}
