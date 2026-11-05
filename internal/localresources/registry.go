// Package localresources records host resources independently of the service
// database, so recovery remains possible when the service cannot start.
package localresources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

type Process struct {
	PID     int    `json:"pid"`
	Group   int    `json:"group"`
	UID     int    `json:"uid"`
	Started string `json:"started"`
}

type FileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type Resource struct {
	Kind    string        `json:"kind"` // service, agent, database, disposable, retained
	Path    string        `json:"path,omitempty"`
	File    *FileIdentity `json:"file,omitempty"`
	Process *Process      `json:"process,omitempty"`
}

type Registry struct{ Root string }

func (r Registry) directory() string { return filepath.Join(r.Root, "resources") }
func (r Registry) filename(item Resource) string {
	key := item.Kind + ":" + item.Path
	if item.Process != nil {
		key = fmt.Sprintf("%s:%d:%s", item.Kind, item.Process.PID, item.Process.Started)
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(r.directory(), hex.EncodeToString(sum[:])+".json")
}

func (r Registry) Record(item Resource) error {
	if err := os.MkdirAll(r.directory(), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(r.directory(), ".record-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), r.filename(item))
}

func (r Registry) RecordProcess(kind string, pid int) error {
	process, err := Inspect(pid)
	if err != nil {
		return err
	}
	if process == nil {
		return nil // Already exited: no live resource needs recovery.
	}
	if process.UID != os.Getuid() {
		return errors.New("managed process has a different owner")
	}
	return r.Record(Resource{Kind: kind, Process: process})
}

func (r Registry) RecordPath(kind, path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Refuse symlink ancestors: recursive cleanup must address the same directory
	// that was registered, rather than following a later redirect.
	if err := SafePath(absolute); err != nil {
		return err
	}
	identity, err := fileIdentity(absolute)
	if err != nil {
		return err
	}
	return r.Record(Resource{Kind: kind, Path: absolute, File: identity})
}

func (r Registry) List() ([]Resource, error) {
	entries, err := os.ReadDir(r.directory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []Resource
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("resource record is a symlink")
		}
		data, err := os.ReadFile(filepath.Join(r.directory(), entry.Name()))
		if err != nil {
			return nil, err
		}
		var item Resource
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("read resource %s: %w", entry.Name(), err)
		}
		switch item.Kind {
		case "service", "agent", "database", "disposable", "retained":
		default:
			return nil, fmt.Errorf("unknown resource kind %q", item.Kind)
		}
		if filepath.Base(r.filename(item)) != entry.Name() {
			return nil, errors.New("resource record identity mismatch")
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result, nil
}
func (r Registry) Forget(item Resource) error {
	err := os.Remove(r.filename(item))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func SafePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("unsafe resource path %q", path)
	}
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("resource path contains symlink: %s", current)
		}
	}
	return nil
}

func fileIdentity(path string) (*FileIdentity, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("unsupported resource filesystem")
	}
	return &FileIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}, nil
}
func validateFile(item Resource) error {
	if err := SafePath(item.Path); err != nil {
		return err
	}
	actual, err := fileIdentity(item.Path)
	if err != nil {
		return err
	}
	if actual != nil && (item.File == nil || *actual != *item.File) {
		return fmt.Errorf("managed path identity changed: %s", item.Path)
	}
	return nil
}
