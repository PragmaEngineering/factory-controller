package cli

import (
	"encoding/json"
	"regexp"
	"strings"
)

// PublicError keeps execution-engine terminology out of the CLI while retaining
// the underlying failure so an operator can still diagnose a failed step.
func PublicError(err error) error {
	if err == nil {
		return nil
	}
	return publicError{cause: err}
}

type publicError struct{ cause error }

func (e publicError) Error() string { return publicText(e.cause.Error()) }
func (e publicError) Unwrap() error { return e.cause }

var diagnosticToken = regexp.MustCompile(`\S+`)

func publicText(value string) string {
	value = strings.ReplaceAll(value, "failed to wait for durable child workflow", "step execution was interrupted")
	return diagnosticToken.ReplaceAllStringFunc(value, func(token string) string {
		// Paths, URLs and identifiers are diagnostic data, not display labels.
		name := strings.Trim(token, "\"':;.,()[]{}!?")
		if name == "Hatchet" || name == "hatchet" {
			return strings.Replace(token, name, "Factory execution service", 1)
		}
		return token
	})
}

// publicValue removes private engine identifiers from JSON inspection output.
// Execution records still retain them internally for cancellation and recovery.
func publicValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return cleanValue(decoded), nil
}

func cleanValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		delete(item, "workflow_run_id")
		for key, child := range item {
			if text, ok := child.(string); ok && (key == "error" || key == "message") {
				item[key] = publicText(text)
			} else {
				item[key] = cleanValue(child)
			}
		}
	case []any:
		for index, child := range item {
			item[index] = cleanValue(child)
		}
	}
	return value
}
