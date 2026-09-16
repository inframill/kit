// Blueprint authoring validation follows Inframill SPEC-0007. The upstream parser
// is internal; keep this boundary aligned and verify packages through the real engine.
package packaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sir-hassan/inframill/pkg/driver"
)

const (
	Filename   = "blueprint.yaml"
	APIVersion = "inframill.dev/blueprint/v1"
	MaxBytes   = 1 << 20
)

// blueprintDefinition is the complete static Blueprint authoring document.
type blueprintDefinition struct {
	APIVersion     string                `yaml:"api_version"`
	Description    string                `yaml:"description"`
	ValuesSchema   map[string]any        `yaml:"values_schema"`
	DriverSettings driver.DriverSettings `yaml:"driver_settings"`
}

// parseDefinition validates document structure before offline schema compilation.
func parseDefinition(data []byte) (blueprintDefinition, error) {
	var result blueprintDefinition
	if len(data) > MaxBytes {
		return result, errors.New("blueprint.yaml exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return result, fmt.Errorf("invalid blueprint.yaml: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return result, errors.New("blueprint.yaml must contain exactly one document")
	}
	if err := jsonNodes(&node); err != nil {
		return result, fmt.Errorf("invalid blueprint.yaml: %w", err)
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return result, errors.New("blueprint.yaml must be an object")
	}
	fields := node.Content[0].Content
	for i := 0; i < len(fields); i += 2 {
		if (fields[i].Value == "api_version" || fields[i].Value == "description") && fields[i+1].Tag != "!!str" {
			return result, fmt.Errorf("%s: must be a string", fields[i].Value)
		}
	}
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("invalid blueprint.yaml: %w", err)
	}
	if result.APIVersion != APIVersion {
		return result, errors.New("api_version: unsupported Blueprint definition version")
	}
	if strings.TrimSpace(result.Description) == "" {
		return result, errors.New("description: must be nonempty")
	}
	if result.ValuesSchema == nil {
		return result, errors.New("values_schema: must be an object")
	}
	if err := result.DriverSettings.Validate(); err != nil {
		return result, err
	}
	// Normalize YAML scalar numbers into the same JSON representation used by
	// schemas, Values and Driver requests. Marshal rejects non-finite floats.
	if err := normalize(&result.ValuesSchema); err != nil {
		return result, errors.New("values_schema: must contain JSON data")
	}
	for id, entry := range result.DriverSettings {
		if err := normalize(&entry); err != nil {
			return result, fmt.Errorf("driver_settings.%s: must contain JSON data", id)
		}
		result.DriverSettings[id] = entry
	}
	return result, nil
}

func normalize[T any](value *T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// Check before typed decoding so map keys cannot be silently coerced to strings,
// duplicate keys cannot be overwritten, and YAML-only values cannot reach JSON.
func jsonNodes(node *yaml.Node) error {
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return errors.New("mapping keys must be strings")
			}
			if seen[key.Value] {
				return errors.New("duplicate mapping key")
			}
			seen[key.Value] = true
		}
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!null", "!!bool", "!!int", "!!float":
		default:
			return errors.New("unsupported YAML scalar tag")
		}
	default:
		return errors.New("unsupported YAML node (aliases are not allowed)")
	}
	if (node.Kind == yaml.MappingNode && node.Tag != "!!map") || (node.Kind == yaml.SequenceNode && node.Tag != "!!seq") {
		return errors.New("unsupported YAML collection tag")
	}
	for _, child := range node.Content {
		if err := jsonNodes(child); err != nil {
			return err
		}
	}
	return nil
}
