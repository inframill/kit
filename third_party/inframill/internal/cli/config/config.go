// Package config reads user CLI preferences without initializing server resources.
package config

import (
	"errors"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Settings struct {
	Endpoint *string
	Timeout  *string
	Output   *string
	Color    *string
}

// Decode validates section shape; scalar values are validated after precedence.
func Decode(section any) (Settings, error) {
	var result Settings
	fields, ok := section.(map[string]any)
	if !ok {
		return result, errors.New("cli must be an object")
	}
	for key, value := range fields {
		text, ok := value.(string)
		if !ok {
			return result, errors.New("cli settings must be strings")
		}
		switch key {
		case "endpoint":
			result.Endpoint = &text
		case "timeout":
			result.Timeout = &text
		case "output":
			result.Output = &text
		case "color":
			result.Color = &text
		default:
			return result, errors.New("unknown cli setting")
		}
	}
	return result, nil
}

// Load inspects cli only. Other sections are never resolved or decoded as server settings.
func Load(path string, explicit bool) (Settings, error) {
	if strings.TrimSpace(path) == "" {
		return Settings{}, errors.New("--config requires a nonempty path")
	}
	data, err := os.ReadFile(path) //nolint:gosec // Explicit application configuration path.
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, errors.New("cannot read CLI configuration")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var doc, extra yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return Settings{}, errors.New("cannot decode CLI configuration")
	}
	if !errors.Is(decoder.Decode(&extra), io.EOF) || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return Settings{}, errors.New("configuration must contain one YAML object")
	}
	root := doc.Content[0]
	seen := map[string]bool{}
	var section *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != yamlString || seen[key.Value] {
			return Settings{}, errors.New("invalid or duplicate configuration key")
		}
		seen[key.Value] = true
		if key.Value == "cli" {
			section = root.Content[i+1]
		}
	}
	if section == nil {
		return Settings{}, nil
	}
	if section.Kind != yaml.MappingNode {
		return Settings{}, errors.New("cli must be an object")
	}
	fields := map[string]any{}
	for i := 0; i < len(section.Content); i += 2 {
		key, value := section.Content[i], section.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != yamlString || value.Kind != yaml.ScalarNode || value.Tag != yamlString {
			return Settings{}, errors.New("cli settings must be strings")
		}
		if _, duplicate := fields[key.Value]; duplicate {
			return Settings{}, errors.New("duplicate cli setting")
		}
		fields[key.Value] = value.Value
	}
	return Decode(fields)
}

const (
	yamlString = "!!str"
)
