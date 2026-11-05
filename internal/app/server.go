package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"controls/adapters"
	"controls/adapters/pi"
	"controls/hatchetengine"
	"controls/internal/artifacts"
	"controls/internal/commandrunner"
	"controls/internal/comms"
	"controls/internal/control"
	"controls/internal/hooks"
	"controls/internal/localresources"
	"controls/internal/sessions"
	"controls/internal/state"

	_ "github.com/hatchet-dev/hatchet-embedded"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

// Serve hosts Factory's local controller and session supervisor on one socket.
// Embedded Hatchet is the default; an external self-hosted server is explicit.
func Serve(ctx context.Context, root string) error {
	serviceContext, cancelService := context.WithCancel(ctx)
	var stopWorker func() error
	var clientToClose *hatchet.Client
	var database *state.Store
	var supervisor *sessions.Manager
	defer func() {
		cancelService()
		if supervisor != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := supervisor.Shutdown(shutdown); err != nil {
				fmt.Fprintf(os.Stderr, "stop agent sessions: %v\n", err)
			}
			cancel()
		}
		if stopWorker != nil {
			_ = stopWorker()
		}
		if database != nil {
			database.Close()
		}
		if clientToClose != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := clientToClose.Close(shutdown); err != nil {
				fmt.Fprintf(os.Stderr, "stop execution engine: %v\n", err)
			}
		}
	}()
	// Persist dependency ownership before startup, including failed attempts.
	if os.Getenv("FACTORY_HATCHET_MODE") != "self-hosted" && os.Getenv(hatchet.EmbeddedDatabaseURLEnv) == "" {
		path, err := localresources.EmbeddedDatabasePath(root)
		if err != nil {
			return err
		}
		if err := (localresources.Registry{Root: root}).RecordPath("database", path); err != nil {
			return err
		}
	}
	client, err := newHatchetClient()
	if err != nil {
		return err
	}
	clientToClose = client
	database, err = state.Open(serviceContext)
	if err != nil {
		return err
	}
	store, err := control.Open(database)
	if err != nil {
		return err
	}
	return sessions.ServeWithHandler(serviceContext, root, database, func(manager *sessions.Manager) (http.Handler, error) {
		supervisor = manager
		communication, err := comms.New(serviceContext, database, artifacts.HostCollector{Root: root})
		if err != nil {
			return nil, err
		}
		communication.Capacity = hatchetengine.PackageSlots
		stopComms, err := communication.Listen(serviceContext)
		if err != nil {
			return nil, err
		}
		go func() { <-serviceContext.Done(); stopComms() }()
		registry := adapters.Registry{"command": adapters.Command{}, "pi": pi.Adapter{}}
		controller := &control.Controller{Adapters: registry, Store: store, Sessions: manager, Root: root, Comms: communication}
		runner := commandrunner.Runner{Adapters: registry, Hooks: hooks.Runner{Recorder: store, Sessions: manager}, Sessions: manager, Recorder: store, Root: root, Comms: communication}
		engine, err := hatchetengine.New(client, runner, control.ProgressObserver{Store: store, Sessions: manager, Comms: communication})
		if err != nil {
			return nil, err
		}
		worker, err := engine.NewWorker("factory-local")
		if err != nil {
			return nil, err
		}
		stop, err := worker.Start()
		if err != nil {
			return nil, fmt.Errorf("start Hatchet worker: %w", err)
		}
		stopWorker = stop
		controller.Engine = engine
		sessionHandler := sessions.Handler(manager)
		controlHandler := control.Handler(controller)
		return serviceLifecycleHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/sessions") {
				sessionHandler.ServeHTTP(w, r)
				return
			}
			controlHandler.ServeHTTP(w, r)
		}), cancelService), nil
	})
}

func newHatchetClient() (*hatchet.Client, error) {
	switch os.Getenv("FACTORY_HATCHET_MODE") {
	case "", "embedded":
		client, err := hatchet.NewClient(hatchet.WithEmbedded())
		if err != nil {
			return nil, fmt.Errorf("start embedded Hatchet: %w", err)
		}
		return client, nil
	case "self-hosted":
		if os.Getenv("HATCHET_CLIENT_TOKEN") == "" {
			return nil, errors.New("self-hosted Hatchet requires a service-issued client token")
		}
		client, err := hatchet.NewClient()
		if err != nil {
			return nil, fmt.Errorf("connect to self-hosted Hatchet: %w", err)
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported FACTORY_HATCHET_MODE %q", os.Getenv("FACTORY_HATCHET_MODE"))
	}
}

func Root() (string, error) {
	root, err := sessions.RuntimeRoot()
	if err != nil {
		return "", err
	}
	return filepath.Abs(root)
}

// Service lifecycle belongs to the host, not the workflow controller. The
// private socket is the authority; monitor's bridge does not expose this route.
func serviceLifecycleHandler(next http.Handler, stop context.CancelFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shutdown" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"stopping":true}`))
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		stop()
	})
}
