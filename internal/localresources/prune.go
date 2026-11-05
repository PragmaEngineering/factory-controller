package localresources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Action is a preflighted removal. An empty Path removes only a stale process
// record. Retained resources are excluded unless explicitly requested.
type Action struct {
	Resource Resource
	Path     string
}

func (r Registry) PlanPrune(all bool) ([]Action, error) {
	items, err := r.List()
	if err != nil {
		return nil, err
	}
	var plan []Action
	for _, item := range items {
		switch item.Kind {
		case "service", "agent":
			live, err := Matches(item.Process)
			if err != nil {
				return nil, err
			}
			if live {
				return nil, fmt.Errorf("managed %s process %d is running; run factory stop --all", item.Kind, item.Process.PID)
			}
			if item.Kind == "agent" && item.Process.Group == item.Process.PID {
				running, err := orphanGroupRunning(item.Process)
				if err != nil {
					return nil, err
				}
				if running {
					return nil, fmt.Errorf("agent process group %d is still running", item.Process.Group)
				}
			}
			plan = append(plan, Action{Resource: item})
		case "database":
			process, err := DatabaseProcess(item.Path)
			if err != nil {
				return nil, err
			}
			if process != nil {
				return nil, fmt.Errorf("managed database process %d is running; run factory stop --all", process.PID)
			}
			if all {
				plan = append(plan, Action{Resource: item, Path: item.Path})
			}
		case "disposable":
			plan = append(plan, Action{Resource: item, Path: item.Path})
		case "retained":
			if all {
				plan = append(plan, Action{Resource: item, Path: item.Path})
			}
		}
	}
	// Validate every target before removing anything, including symlink ancestors.
	for _, action := range plan {
		if action.Path != "" {
			if err := validateFile(action.Resource); err != nil {
				return nil, err
			}
		}
	}
	return plan, nil
}

func (r Registry) ApplyPrune(plan []Action) error {
	for _, action := range plan {
		if action.Path != "" {
			if err := validateFile(action.Resource); err != nil {
				return err
			}
			// OpenRoot constrains recursive removal to the selected parent even if a
			// descendant is a symlink. It never follows links out to user source files.
			parent, err := os.OpenRoot(filepath.Dir(action.Path))
			if errors.Is(err, os.ErrNotExist) {
				if err := r.Forget(action.Resource); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			err = parent.RemoveAll(filepath.Base(action.Path))
			parent.Close()
			if err != nil {
				return err
			}
		}
		if err := r.Forget(action.Resource); err != nil {
			return err
		}
	}
	return nil
}
