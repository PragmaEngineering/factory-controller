package sessions

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"controls/internal/state"
	"controls/internal/telemetry"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Manager persists session records, messages, and output in PostgreSQL. Only
// live PTYs and bearer tokens are process-local; they cease to be useful when
// the owning worker exits.
type Manager struct {
	mu        sync.Mutex
	root      string
	owner     string
	socket    string
	environ   []string
	db        *state.Store
	terminals map[string]*os.File
}

func Open(root, socket string, db *state.Store) (*Manager, error) {
	if db == nil {
		return nil, errors.New("Factory database is required")
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("identify session worker: %w", err)
	}
	m := &Manager{root: root, owner: host + ":" + root, socket: socket, db: db,
		environ:   packageEnvironment(os.Environ()),
		terminals: make(map[string]*os.File)}
	// This supervisor is the sole owner of processes launched from this runtime
	// root. A restart cannot recover their PTYs, so record that explicitly.
	rows, err := db.List(context.Background(), "session")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var session Session
		if err := json.Unmarshal(row, &session); err != nil {
			return nil, err
		}
		if session.Owner != m.owner || (session.State != Running && session.State != Stopping) {
			continue
		}
		if err := m.changeSession(session.ID, func(current *Session) error {
			current.State = Interrupted
			now := time.Now().UTC()
			current.EndedAt = &now
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) Launch(request Launch) (Session, error) {
	if request.Name == "" || request.Runner == "" || len(request.Command) == 0 || request.Command[0] == "" {
		return Session{}, errors.New("name, runner, and command are required")
	}
	if request.InputMode == "" {
		request.InputMode = "queue"
	}
	if request.InputMode != "queue" && request.InputMode != "pty" && request.InputMode != "none" {
		return Session{}, errors.New("input mode must be queue, pty, or none")
	}
	if request.Directory == "" {
		return Session{}, errors.New("working directory is required")
	}
	directory, err := filepath.Abs(request.Directory)
	if err != nil {
		return Session{}, err
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return Session{}, fmt.Errorf("working directory %q is unavailable", directory)
	}
	if request.ParentID != "" {
		parent, err := m.Get(request.ParentID)
		if err != nil || parent.State != Running {
			return Session{}, errors.New("parent session is not running")
		}
		if request.ExecutionID == "" {
			request.ExecutionID = parent.ExecutionID
		}
	}
	for name := range request.Environment {
		if strings.ContainsAny(name, "=\x00") || name == "" || strings.HasPrefix(name, "FACTORY_SESSION_") || name == "FACTORY_CONTROL_SOCKET" || strings.HasPrefix(name, "HATCHET_CLIENT_") || name == "FACTORY_HATCHET_MODE" {
			return Session{}, fmt.Errorf("invalid environment name %q", name)
		}
	}
	if request.Runtime != nil && request.Try > 1 && request.ExecutionID != "" {
		previous, err := m.List(request.ExecutionID)
		if err != nil {
			return Session{}, err
		}
		if err := validateRetryRuntime(previous, request); err != nil {
			return Session{}, err
		}
	}
	id, token := uuid.NewString(), uuid.NewString()
	command := exec.Command(request.Command[0], request.Command[1:]...)
	command.Dir = directory
	command.Env = append(append([]string(nil), m.environ...),
		"FACTORY_SESSION_ID="+id, "FACTORY_SESSION_TOKEN="+token, "FACTORY_CONTROL_SOCKET="+m.socket)
	for name, value := range request.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	var terminal *os.File
	if request.InputMode == "none" {
		command.Stdin = strings.NewReader(request.Stdin)
		terminal, err = pty.StartWithAttrs(command, nil, &syscall.SysProcAttr{Setsid: true})
	} else {
		terminal, err = pty.Start(command)
	}
	if err != nil {
		return Session{}, fmt.Errorf("launch %s: %w", request.Name, err)
	}
	record := Session{ID: id, ParentID: request.ParentID, ExecutionID: request.ExecutionID,
		StepID: request.StepID, Visit: request.Visit, Try: request.Try,
		Name: request.Name, Runner: request.Runner, Runtime: request.Runtime, InputMode: request.InputMode,
		Owner: m.owner, State: Running, PID: command.Process.Pid, StartedAt: time.Now().UTC()}
	if err := m.db.CreateTogether(context.Background(),
		state.Record{Kind: "session", ID: id, Value: record},
		state.Record{Kind: "session_token", ID: id, Value: tokenDigest(token)}); err != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = terminal.Close()
		_, _ = command.Process.Wait()
		return Session{}, err
	}
	m.mu.Lock()
	m.terminals[id] = terminal
	m.mu.Unlock()
	outputDone := make(chan struct{})
	go m.captureOutput(record, terminal, outputDone)
	go m.waitForExit(id, command, terminal, outputDone)
	return record, nil
}

func packageEnvironment(source []string) []string {
	result := make([]string, 0, len(source))
	for _, entry := range source {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "HATCHET_CLIENT_") || name == "FACTORY_HATCHET_MODE" || name == "DATABASE_URL" || name == "FACTORY_DATABASE_URL" {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (m *Manager) captureOutput(session Session, terminal *os.File, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 8192)
	for {
		n, err := terminal.Read(buf)
		if n > 0 {
			// Never discard output during a temporary database outage: holding
			// this read also applies backpressure to the agent's PTY.
			for {
				source := "agent/" + session.Name + "/" + session.ID[:8]
				if session.Runner == "hook" {
					source = session.Name + "/" + session.ID[:8]
				}
				writeErr := m.db.AppendOutput(context.Background(), session.ID, buf[:n], state.Activity{ExecutionID: session.ExecutionID, SessionID: session.ID, Source: source})
				if writeErr == nil {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			_ = m.changeSession(session.ID, func(record *Session) error {
				now := time.Now().UTC()
				record.LastOutputAt = &now
				return nil
			})
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) waitForExit(id string, command *exec.Cmd, terminal *os.File, outputDone <-chan struct{}) {
	err := command.Wait()
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		_ = terminal.Close()
		<-outputDone
	}
	_ = terminal.Close()
	code := 0
	if err != nil {
		code = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
	}
	_ = m.changeSession(id, func(record *Session) error {
		if record.State == Stopping {
			record.State = Stopped
		} else {
			record.State = Exited
		}
		record.ExitCode = &code
		now := time.Now().UTC()
		record.EndedAt = &now
		return nil
	})
	if session, getErr := m.Get(id); getErr == nil {
		_ = m.db.AppendActivity(context.Background(), state.Activity{ExecutionID: session.ExecutionID, SessionID: id, Source: "session/" + session.Name, Data: []byte(fmt.Sprintf("%s (exit %d)\n", session.State, code))})
	}
	m.mu.Lock()
	delete(m.terminals, id)
	m.mu.Unlock()
}

func (m *Manager) List(executionID string) ([]Session, error) {
	rows, err := m.db.List(context.Background(), "session")
	if err != nil {
		return nil, err
	}
	result := make([]Session, 0, len(rows))
	for _, row := range rows {
		var session Session
		if err := json.Unmarshal(row, &session); err != nil {
			return nil, err
		}
		if executionID == "" || session.ExecutionID == executionID {
			if err := m.resources(&session); err != nil {
				return nil, err
			}
			result = append(result, session)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.Before(result[j].StartedAt) })
	return result, nil
}

func (m *Manager) Get(id string) (Session, error) {
	var session Session
	err := m.db.Get(context.Background(), "session", id, &session)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, os.ErrNotExist
	}
	if err == nil {
		err = m.resources(&session)
	}
	return session, err
}

func (m *Manager) resources(session *Session) error {
	var p telemetry.Projection
	err := m.db.Get(context.Background(), "telemetry", session.ID, &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	session.Resources = p.Summary
	return err
}

func (m *Manager) Output(id string, offset int64, limit int) (Output, error) {
	if _, err := m.Get(id); err != nil {
		return Output{}, err
	}
	if limit <= 0 || limit > 65536 {
		limit = 65536
	}
	content, err := m.db.Output(context.Background(), id)
	if err != nil {
		return Output{}, err
	}
	if offset < 0 {
		offset = max(0, int64(len(content))-int64(limit))
	}
	if offset > int64(len(content)) {
		offset = int64(len(content))
	}
	end := min(int64(len(content)), offset+int64(limit))
	return Output{Text: string(content[offset:end]), NextOffset: end}, nil
}

func (m *Manager) Send(id, body string) (Message, error) {
	if strings.TrimSpace(body) == "" || len(body) > 16384 {
		return Message{}, errors.New("message must contain 1 to 16384 bytes")
	}
	session, err := m.Get(id)
	if err != nil || session.State != Running || session.InputMode == "none" {
		return Message{}, errors.New("session is not running or does not accept interactive input")
	}
	message := Message{ID: uuid.NewString(), SessionID: id, Body: body, State: MessageQueued, CreatedAt: time.Now().UTC()}
	if err := m.db.Create(context.Background(), "message", message.ID, message); err != nil {
		return Message{}, err
	}
	_ = m.db.AppendActivity(context.Background(), state.Activity{ExecutionID: session.ExecutionID, SessionID: id, Source: "operator", Data: []byte(body + "\n")})
	if session.InputMode == "pty" {
		m.mu.Lock()
		terminal := m.terminals[id]
		m.mu.Unlock()
		go m.deliverToTerminal(message.ID, terminal, body)
	}
	return message, nil
}

func (m *Manager) deliverToTerminal(messageID string, terminal *os.File, body string) {
	var deliveryErr error
	if terminal == nil {
		deliveryErr = errors.New("agent terminal is unavailable on this worker")
	} else {
		_, deliveryErr = io.WriteString(terminal, body+"\n")
	}
	_ = m.changeMessage(messageID, func(message *Message) error {
		if deliveryErr != nil {
			message.State = MessageFailed
		} else {
			message.State = MessageDelivered
			now := time.Now().UTC()
			message.DeliveredAt = &now
		}
		return nil
	})
	var message Message
	if err := m.db.Get(context.Background(), "message", messageID, &message); err == nil {
		if session, err := m.Get(message.SessionID); err == nil {
			_ = m.db.AppendActivity(context.Background(), state.Activity{ExecutionID: session.ExecutionID, SessionID: session.ID, Source: "message", Data: []byte(message.ID + " " + string(message.State) + "\n")})
		}
	}
}

func (m *Manager) Messages(id string) ([]Message, error) {
	if _, err := m.Get(id); err != nil {
		return nil, err
	}
	rows, err := m.db.List(context.Background(), "message")
	if err != nil {
		return nil, err
	}
	var result []Message
	for _, row := range rows {
		var message Message
		if err := json.Unmarshal(row, &message); err != nil {
			return nil, err
		}
		if message.SessionID == id {
			result = append(result, message)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (m *Manager) Inbox(id, token string) ([]Message, error) {
	if !m.validToken(id, token) {
		return nil, errors.New("invalid session token")
	}
	session, err := m.Get(id)
	if err != nil || session.State != Running {
		return nil, errors.New("agent session is not running")
	}
	messages, err := m.Messages(id)
	if err != nil {
		return nil, err
	}
	var pending []Message
	for _, message := range messages {
		if message.State == MessageQueued {
			err := m.changeMessage(message.ID, func(item *Message) error {
				if item.State == MessageQueued {
					item.State = MessageDelivered
					now := time.Now().UTC()
					item.DeliveredAt = &now
				}
				message = *item
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if message.State == MessageDelivered {
			pending = append(pending, message)
		}
	}
	return pending, nil
}

func (m *Manager) Acknowledge(id, token, messageID string) (Message, error) {
	if !m.validToken(id, token) {
		return Message{}, errors.New("invalid session token")
	}
	var result Message
	err := m.changeMessage(messageID, func(message *Message) error {
		if message.SessionID != id {
			return os.ErrNotExist
		}
		if message.State != MessageDelivered && message.State != MessageRead {
			return errors.New("message was not delivered")
		}
		if message.State != MessageRead {
			message.State = MessageRead
			now := time.Now().UTC()
			message.ReadAt = &now
		}
		result = *message
		return nil
	})
	if err == nil {
		if session, getErr := m.Get(id); getErr == nil {
			_ = m.db.AppendActivity(context.Background(), state.Activity{ExecutionID: session.ExecutionID, SessionID: id, Source: "message", Data: []byte(result.ID + " " + string(result.State) + "\n")})
		}
	}
	return result, err
}

func (m *Manager) Stop(id string) error {
	session, err := m.Get(id)
	if err != nil || session.State != Running {
		return errors.New("agent session is not running")
	}
	if session.Owner != m.owner {
		return errors.New("agent session belongs to another worker")
	}
	if err := m.changeSession(id, func(record *Session) error {
		if record.State != Running {
			return errors.New("agent session is not running")
		}
		record.State = Stopping
		return nil
	}); err != nil {
		return err
	}
	if err := syscall.Kill(-session.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	go func() {
		time.Sleep(3 * time.Second)
		current, err := m.Get(id)
		if err == nil && current.State == Stopping {
			_ = syscall.Kill(-session.PID, syscall.SIGKILL)
		}
	}()
	return nil
}

func (m *Manager) changeSession(id string, change func(*Session) error) error {
	err := m.db.Change(context.Background(), "session", id, func(content []byte) ([]byte, error) {
		var session Session
		if err := json.Unmarshal(content, &session); err != nil {
			return nil, err
		}
		if err := change(&session); err != nil {
			return nil, err
		}
		return json.Marshal(session)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return os.ErrNotExist
	}
	return err
}

func (m *Manager) changeMessage(id string, change func(*Message) error) error {
	err := m.db.Change(context.Background(), "message", id, func(content []byte) ([]byte, error) {
		var message Message
		if err := json.Unmarshal(content, &message); err != nil {
			return nil, err
		}
		if err := change(&message); err != nil {
			return nil, err
		}
		return json.Marshal(message)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return os.ErrNotExist
	}
	return err
}

func tokenDigest(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func (m *Manager) validToken(id, token string) bool {
	if token == "" {
		return false
	}
	var stored [32]byte
	if err := m.db.Get(context.Background(), "session_token", id, &stored); err != nil {
		return false
	}
	digest := tokenDigest(token)
	return subtle.ConstantTimeCompare(stored[:], digest[:]) == 1
}
