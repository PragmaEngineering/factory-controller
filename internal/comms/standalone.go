package comms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Scope is a conversation boundary, not a workflow execution. Its ID uses a
// separate namespace while reusing the existing execution_id storage column.
type Scope struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) CreateScope(ctx context.Context, name string) (Scope, error) {
	scope := Scope{ID: "scope:" + identity(name), Name: name}
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return scope, fault("invalid_argument", "scope name must contain 1..200 characters")
	}
	raw, _ := json.Marshal(scope)
	_, err := s.DB.Pool.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES('comms_scope',$1,$2) ON CONFLICT DO NOTHING`, scope.ID, raw)
	return scope, err
}

// RegisterPeer is a trusted host operation. Peers receive no workflow authority.
// A stable key makes uncertain retries recover the same credential; retirement
// is permanent for that key, so a replacement needs a new key.
func (s *Service) RegisterPeer(ctx context.Context, scope, key, name string) (Connection, error) {
	if key == "" || len(key) > 200 || strings.TrimSpace(name) == "" || len(name) > 200 {
		return Connection{}, fault("invalid_argument", "key and name must contain 1..200 characters")
	}
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockExecution(ctx, tx, scope); err != nil {
		return Connection{}, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.records WHERE kind='comms_scope' AND id=$1)`, scope).Scan(&exists); err != nil {
		return Connection{}, err
	}
	if !exists {
		return Connection{}, fault("not_authorized", "unknown communication scope")
	}
	id := identity(scope + "/peer/" + key)
	token := s.token(id)
	_, err = tx.Exec(ctx, `INSERT INTO factory.comms_participants(id,execution_id,name,kind,token_hash) VALUES($1,$2,$3,'peer',$4) ON CONFLICT DO NOTHING`, id, scope, name, tokenHash(token))
	if err != nil {
		return Connection{}, err
	}
	var recorded string
	var active bool
	if err = tx.QueryRow(ctx, `SELECT name,active FROM factory.comms_participants WHERE id=$1`, id).Scan(&recorded, &active); err != nil {
		return Connection{}, err
	}
	if recorded != name {
		return Connection{}, fault("operation_conflict", "registration key already has another name")
	}
	if !active {
		return Connection{}, fault("attempt_inactive", "registration is retired; use a new key")
	}
	return Connection{URL: s.URL, ParticipantID: id, Token: token}, tx.Commit(ctx)
}

func (s *Service) RetirePeer(ctx context.Context, id string) error {
	var kind string
	if err := s.DB.Pool.QueryRow(ctx, `SELECT kind FROM factory.comms_participants WHERE id=$1`, id).Scan(&kind); err != nil {
		return err
	}
	if kind != "peer" {
		return fault("not_authorized", "not a standalone participant")
	}
	return s.End(ctx, id)
}

// HostHandler requires owner-only Unix access or authenticated host authority.
// Never mount provisioning behind participant-only MCP authentication.
func (s *Service) HostHandler() http.Handler {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, v any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			status := http.StatusBadRequest
			var f *Fault
			if !errors.As(err, &f) {
				status = http.StatusInternalServerError
				err = errors.New("communication operation failed")
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) error {
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	mux.HandleFunc("POST /comms/scopes", func(w http.ResponseWriter, r *http.Request) {
		var a struct {
			Name string `json:"name"`
		}
		if err := decode(w, r, &a); err != nil {
			respond(w, nil, fault("invalid_argument", "invalid scope request"))
			return
		}
		v, err := s.CreateScope(r.Context(), a.Name)
		respond(w, v, err)
	})
	mux.HandleFunc("POST /comms/scopes/{scope}/participants", func(w http.ResponseWriter, r *http.Request) {
		var a struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		}
		if err := decode(w, r, &a); err != nil {
			respond(w, nil, fault("invalid_argument", "invalid registration request"))
			return
		}
		c, err := s.RegisterPeer(r.Context(), r.PathValue("scope"), a.Key, a.Name)
		respond(w, map[string]string{"url": c.URL, "participant_id": c.ParticipantID, "token": c.Token}, err)
	})
	mux.HandleFunc("POST /comms/authenticate", func(w http.ResponseWriter, r *http.Request) {
		var a struct {
			Token string `json:"token"`
		}
		if err := decode(w, r, &a); err != nil {
			respond(w, nil, fault("invalid_argument", "invalid authentication request"))
			return
		}
		p, err := s.Authenticate(r.Context(), a.Token)
		if err == nil && (!p.Active || p.Kind != "peer") {
			err = fault("not_authorized", "active standalone participant required")
		}
		respond(w, p, err)
	})
	mux.HandleFunc("GET /comms/participants/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p Participant
		err := s.DB.Pool.QueryRow(r.Context(), `SELECT id,execution_id,name,kind,active FROM factory.comms_participants WHERE id=$1 AND kind='peer'`, r.PathValue("id")).Scan(&p.ID, &p.ExecutionID, &p.Name, &p.Kind, &p.Active)
		respond(w, p, err)
	})
	mux.HandleFunc("DELETE /comms/participants/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := s.RetirePeer(r.Context(), r.PathValue("id"))
		respond(w, map[string]bool{"retired": err == nil}, err)
	})
	// A restarted listener may have a new URL; recover it without minting identities.
	mux.HandleFunc("GET /comms", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]string{"url": s.URL, "mode": "standalone"}, nil)
	})
	return mux
}
