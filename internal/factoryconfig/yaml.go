package factoryconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// decodeYAML keeps the authored YAML boundary separate from the JSON execution
// contract. Existing JSON tags, override values, and custom validators remain
// authoritative after YAML syntax has been checked.
func decodeYAML(data []byte, definition *Definition) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("expected one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("expected a YAML mapping")
	}
	if err := validateYAML(document.Content[0]); err != nil {
		return err
	}
	var value any
	// Decode detects duplicate keys and invalid/cyclic alias expansion.
	if err := document.Decode(&value); err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("configuration must contain JSON-compatible values: %w", err)
	}
	strict := json.NewDecoder(bytes.NewReader(canonical))
	strict.DisallowUnknownFields()
	return strict.Decode(definition)
}

// Require string keys and JSON-compatible scalar types. In particular, `on`
// remains a string key; YAML merge keys and implicit timestamps are not part of
// the configuration language. Aliases may reuse values, not merge definitions.
func validateYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("line %d: mapping keys must be strings; merge keys are not supported", key.Line)
			}
		}
	}
	if node.Tag != "" {
		switch node.Tag {
		case "!!map", "!!seq", "!!str", "!!bool", "!!int", "!!float", "!!null":
		default:
			return fmt.Errorf("line %d: unsupported YAML tag %q; quote the value to use a string", node.Line, node.Tag)
		}
	}
	for _, child := range node.Content {
		if err := validateYAML(child); err != nil {
			return err
		}
	}
	return nil
}
