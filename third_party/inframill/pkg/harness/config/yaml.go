// Package config provides reusable configuration loaders for Hummus applications.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sir-hassan/inframill/pkg/harness"
)

const (
	violationInvalid   = "invalid"
	violationDuplicate = "duplicate"
	fieldType          = "type"
	fieldSettings      = "settings"
	yamlStringTag      = "!!str"
)

var environmentReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// YAMLFileOption configures a YAML file loader. Implementations are provided by this package.
type YAMLFileOption interface {
	apply(options *yamlFileOptions)
}

type yamlFileOptionFunc func(*yamlFileOptions)

func (option yamlFileOptionFunc) apply(options *yamlFileOptions) { option(options) }

type yamlFileOptions struct {
	environment     map[string]string
	allowedSections []string
}

// WithEnvironment supplies the immutable environment snapshot used to resolve
// exact ${NAME} references in main.databaseUrl, component settings, and
// application sections.
func WithEnvironment(environ []string) YAMLFileOption {
	environment := snapshotEnvironment(environ)

	return yamlFileOptionFunc(func(options *yamlFileOptions) {
		options.environment = cloneEnvironment(environment)
	})
}

// WithAllowedSections permits the named application-owned top-level sections.
// Each section is optional but must be a string-keyed object when present.
// Names are exact, must be non-blank, and cannot be main, logger, handlers, or
// workers. Load reports invalid names as validation errors. Repeated names are
// harmless; the last WithAllowedSections option replaces earlier lists.
func WithAllowedSections(names ...string) YAMLFileOption {
	names = slices.Clone(names)

	return yamlFileOptionFunc(func(options *yamlFileOptions) {
		options.allowedSections = slices.Clone(names)
	})
}

// YAMLFile loads one Hummus configuration snapshot from a YAML file.
type YAMLFile struct {
	path            string
	environment     map[string]string
	allowedSections []string
}

// NewYAMLFile constructs an immutable YAML file loader. The file is read by Load.
func NewYAMLFile(path string, options ...YAMLFileOption) *YAMLFile {
	settings := yamlFileOptions{environment: make(map[string]string)}
	for _, option := range options {
		if option != nil {
			option.apply(&settings)
		}
	}

	return &YAMLFile{
		path: path, environment: cloneEnvironment(settings.environment),
		allowedSections: slices.Clone(settings.allowedSections),
	}
}

// Load reads, resolves, and decodes one complete configuration snapshot.
func (file *YAMLFile) Load(ctx context.Context) (harness.Config, error) {
	if err := ctx.Err(); err != nil {
		return harness.Config{}, err
	}
	if file == nil {
		return harness.Config{}, errors.New("read YAML configuration: loader is nil")
	}
	for index, name := range file.allowedSections {
		path := "allowedSections[" + strconv.Itoa(index) + "]"
		if strings.TrimSpace(name) == "" {
			return harness.Config{}, invalidConfiguration(path, "must be a non-empty string")
		}
		switch name {
		case "main", "logger", "handlers", "workers":
			return harness.Config{}, invalidConfiguration(path, "section is owned by Hummus")
		}
	}

	data, err := os.ReadFile(file.path)
	if err != nil {
		return harness.Config{}, fmt.Errorf("read YAML configuration %q: %w", file.path, err)
	}
	if err := ctx.Err(); err != nil {
		return harness.Config{}, err
	}

	document, err := decodeDocument(data)
	if err != nil {
		return harness.Config{}, err
	}
	decoded, violations := decodeConfig(document, file.environment, file.allowedSections)
	if len(violations) > 0 {
		return harness.Config{}, harness.NewValidationError(violations)
	}
	if err := ctx.Err(); err != nil {
		return harness.Config{}, err
	}

	return decoded, nil
}

func decodeDocument(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		message := "YAML syntax is invalid"
		if errors.Is(err, io.EOF) {
			message = "YAML document is empty"
		}

		return nil, invalidConfiguration("config", message)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		message := "YAML syntax is invalid"
		if err == nil {
			message = "only one YAML document is allowed"
		}

		return nil, invalidConfiguration("config", message)
	}
	if len(document.Content) != 1 {
		return nil, invalidConfiguration("config", "YAML document is empty")
	}

	return document.Content[0], nil
}

func decodeConfig(node *yaml.Node, environment map[string]string, allowedSections []string) (harness.Config, harness.Violations) {
	if node.Kind != yaml.MappingNode {
		return harness.Config{}, harness.Violations{invalidViolation("config", "must be a mapping")}
	}

	var decoded harness.Config
	var violations harness.Violations
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		name, violation := mappingKey("config", key)
		if violation != nil {
			violations = append(violations, *violation)
			continue
		}
		if duplicate := duplicateField(seen, name, name); duplicate != nil {
			violations = append(violations, *duplicate)
			continue
		}

		switch name {
		case "main":
			var current harness.Violations
			decoded.Main, current = decodeMain(value, environment)
			violations = append(violations, current...)
		case "logger":
			var current harness.Violations
			decoded.Logger, current = decodeLogger(value, environment)
			violations = append(violations, current...)
		case "handlers":
			var current harness.Violations
			decoded.Handlers, current = decodeHandlers(value, environment)
			violations = append(violations, current...)
		case "workers":
			var current harness.Violations
			decoded.Workers, current = decodeWorkers(value, environment)
			violations = append(violations, current...)
		default:
			if !slices.Contains(allowedSections, name) {
				violations = append(violations, invalidViolation(name, "field is not supported"))
				continue
			}
			section, current := decodeObject(value, name, environment)
			violations = append(violations, current...)
			if decoded.Sections == nil {
				decoded.Sections = make(map[string]any)
			}
			decoded.Sections[name] = section
		}
	}

	return decoded, violations
}

func decodeMain(node *yaml.Node, environment map[string]string) (harness.MainConfig, harness.Violations) {
	if node.Kind != yaml.MappingNode {
		return harness.MainConfig{}, harness.Violations{invalidViolation("main", "must be a mapping")}
	}

	var decoded harness.MainConfig
	var violations harness.Violations
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		name, violation := mappingKey("main", key)
		if violation != nil {
			violations = append(violations, *violation)
			continue
		}
		path := "main." + name
		if duplicate := duplicateField(seen, name, path); duplicate != nil {
			violations = append(violations, *duplicate)
			continue
		}

		switch name {
		case "port":
			if err := value.Decode(&decoded.Port); err != nil {
				violations = append(violations, invalidViolation(path, "must be an integer"))
			}
		case "databaseUrl":
			current := resolveSettings(value, path, environment)
			violations = append(violations, current...)
			if len(current) == 0 {
				databaseURL, scalarViolation := scalarString(value, path)
				if scalarViolation != nil {
					violations = append(violations, *scalarViolation)
				} else {
					decoded.DatabaseURL = databaseURL
				}
			}
		case "shutdownTimeout":
			violations = append(violations, decodeDuration(value, path, &decoded.ShutdownTimeout)...)
		case "readHeaderTimeout":
			violations = append(violations, decodeDuration(value, path, &decoded.ReadHeaderTimeout)...)
		case "readTimeout":
			violations = append(violations, decodeDuration(value, path, &decoded.ReadTimeout)...)
		case "writeTimeout":
			violations = append(violations, decodeDuration(value, path, &decoded.WriteTimeout)...)
		case "idleTimeout":
			violations = append(violations, decodeDuration(value, path, &decoded.IdleTimeout)...)
		default:
			violations = append(violations, invalidViolation(path, "field is not supported"))
		}
	}

	return decoded, violations
}

func decodeDuration(node *yaml.Node, path string, destination *time.Duration) harness.Violations {
	value, violation := scalarString(node, path)
	if violation != nil {
		return harness.Violations{*violation}
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return harness.Violations{invalidViolation(path, "must be a duration string")}
	}
	*destination = parsed

	return nil
}

func decodeLogger(node *yaml.Node, environment map[string]string) (harness.LoggerConfig, harness.Violations) {
	if node.Kind != yaml.MappingNode {
		return harness.LoggerConfig{}, harness.Violations{invalidViolation("logger", "must be a mapping")}
	}

	var decoded harness.LoggerConfig
	var violations harness.Violations
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		name, violation := mappingKey("logger", key)
		if violation != nil {
			violations = append(violations, *violation)
			continue
		}
		path := "logger." + name
		if duplicate := duplicateField(seen, name, path); duplicate != nil {
			violations = append(violations, *duplicate)
			continue
		}

		switch name {
		case fieldType:
			decoded.Type, violation = scalarString(value, path)
			if violation != nil {
				violations = append(violations, *violation)
			}
		case fieldSettings:
			var current harness.Violations
			decoded.Settings, current = decodeSettings(value, path, environment)
			violations = append(violations, current...)
		default:
			violations = append(violations, invalidViolation(path, "field is not supported"))
		}
	}

	return decoded, violations
}

func decodeHandlers(node *yaml.Node, environment map[string]string) ([]harness.HandlerConfig, harness.Violations) {
	if node.Kind != yaml.SequenceNode {
		return nil, harness.Violations{invalidViolation("handlers", "must be a sequence")}
	}
	decoded := make([]harness.HandlerConfig, 0, len(node.Content))
	var violations harness.Violations
	for index, item := range node.Content {
		current, itemViolations := decodeHandler(item, "handlers["+strconv.Itoa(index)+"]", environment)
		decoded = append(decoded, current)
		violations = append(violations, itemViolations...)
	}

	return decoded, violations
}

func decodeHandler(node *yaml.Node, path string, environment map[string]string) (harness.HandlerConfig, harness.Violations) {
	componentType, settings, violations := decodeComponent(node, path, environment)

	return harness.HandlerConfig{Type: componentType, Settings: settings}, violations
}

func decodeWorkers(node *yaml.Node, environment map[string]string) ([]harness.WorkerConfig, harness.Violations) {
	if node.Kind != yaml.SequenceNode {
		return nil, harness.Violations{invalidViolation("workers", "must be a sequence")}
	}
	decoded := make([]harness.WorkerConfig, 0, len(node.Content))
	var violations harness.Violations
	for index, item := range node.Content {
		current, itemViolations := decodeWorker(item, "workers["+strconv.Itoa(index)+"]", environment)
		decoded = append(decoded, current)
		violations = append(violations, itemViolations...)
	}

	return decoded, violations
}

func decodeWorker(node *yaml.Node, path string, environment map[string]string) (harness.WorkerConfig, harness.Violations) {
	componentType, settings, violations := decodeComponent(node, path, environment)

	return harness.WorkerConfig{Type: componentType, Settings: settings}, violations
}

func decodeComponent(node *yaml.Node, path string, environment map[string]string) (string, harness.Settings, harness.Violations) {
	if node.Kind != yaml.MappingNode {
		return "", harness.Settings{}, harness.Violations{invalidViolation(path, "must be a mapping")}
	}

	var componentType string
	var settings harness.Settings
	var violations harness.Violations
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		name, violation := mappingKey(path, key)
		if violation != nil {
			violations = append(violations, *violation)
			continue
		}
		fieldPath := path + "." + name
		if duplicate := duplicateField(seen, name, fieldPath); duplicate != nil {
			violations = append(violations, *duplicate)
			continue
		}

		switch name {
		case fieldType:
			componentType, violation = scalarString(value, fieldPath)
		case fieldSettings:
			var current harness.Violations
			settings, current = decodeSettings(value, fieldPath, environment)
			violations = append(violations, current...)
		default:
			violations = append(violations, invalidViolation(fieldPath, "field is not supported"))
		}
		if violation != nil {
			violations = append(violations, *violation)
		}
	}

	return componentType, settings, violations
}

func decodeSettings(node *yaml.Node, path string, environment map[string]string) (harness.Settings, harness.Violations) {
	value, violations := decodeObject(node, path, environment)
	if len(violations) > 0 {
		return harness.Settings{}, violations
	}
	settings, err := harness.NewSettings(value)
	if err != nil {
		return harness.Settings{}, harness.Violations{invalidViolation(path, "contains a value that cannot be normalized")}
	}

	return settings, nil
}

func decodeObject(node *yaml.Node, path string, environment map[string]string) (map[string]any, harness.Violations) {
	if node.Kind != yaml.MappingNode {
		return nil, harness.Violations{invalidViolation(path, "must be a mapping")}
	}
	if violations := resolveSettings(node, path, environment); len(violations) > 0 {
		return nil, violations
	}

	var value map[string]any
	if err := node.Decode(&value); err != nil {
		return nil, harness.Violations{invalidViolation(path, "contains an unsupported YAML value")}
	}

	return value, nil
}

func resolveSettings(node *yaml.Node, path string, environment map[string]string) harness.Violations {
	switch node.Kind {
	case yaml.MappingNode:
		var violations harness.Violations
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			name, violation := mappingKey(path, key)
			if violation != nil {
				violations = append(violations, *violation)
				continue
			}
			fieldPath := path + "." + name
			if duplicate := duplicateField(seen, name, fieldPath); duplicate != nil {
				violations = append(violations, *duplicate)
				continue
			}
			violations = append(violations, resolveSettings(value, fieldPath, environment)...)
		}

		return violations
	case yaml.SequenceNode:
		var violations harness.Violations
		for index, item := range node.Content {
			itemPath := path + "[" + strconv.Itoa(index) + "]"
			violations = append(violations, resolveSettings(item, itemPath, environment)...)
		}

		return violations
	case yaml.ScalarNode:
		if node.Tag != yamlStringTag {
			return nil
		}
		if match := environmentReference.FindStringSubmatch(node.Value); match != nil {
			value, ok := environment[match[1]]
			if !ok {
				return harness.Violations{invalidViolation(path, "referenced environment variable is not set")}
			}
			node.Value = value

			return nil
		}
		if strings.HasPrefix(node.Value, "$${") && strings.HasSuffix(node.Value, "}") {
			literal := node.Value[1:]
			if environmentReference.MatchString(literal) {
				node.Value = literal

				return nil
			}
		}
		if strings.Contains(node.Value, "${") {
			return harness.Violations{invalidViolation(path, "environment reference must occupy the complete scalar")}
		}

		return nil
	case yaml.AliasNode:
		return harness.Violations{invalidViolation(path, "YAML aliases are not supported in settings")}
	case yaml.DocumentNode:
		return harness.Violations{invalidViolation(path, "YAML documents are not supported in settings")}
	default:
		return harness.Violations{invalidViolation(path, "contains an unsupported YAML value")}
	}
}

func mappingKey(path string, node *yaml.Node) (string, *harness.Violation) {
	if node.Kind != yaml.ScalarNode || node.Tag != yamlStringTag || strings.TrimSpace(node.Value) == "" {
		violation := invalidViolation(path, "mapping keys must be non-empty strings")

		return "", &violation
	}

	return node.Value, nil
}

func scalarString(node *yaml.Node, path string) (string, *harness.Violation) {
	if node.Kind != yaml.ScalarNode || node.Tag != yamlStringTag {
		violation := invalidViolation(path, "must be a string")

		return "", &violation
	}

	return node.Value, nil
}

func duplicateField(seen map[string]struct{}, name, path string) *harness.Violation {
	if _, ok := seen[name]; ok {
		violation := harness.Violation{Path: path, Code: violationDuplicate, Message: "field is duplicated"}

		return &violation
	}
	seen[name] = struct{}{}

	return nil
}

func invalidConfiguration(path, message string) error {
	return harness.NewValidationError(harness.Violations{invalidViolation(path, message)})
}

func invalidViolation(path, message string) harness.Violation {
	return harness.Violation{Path: path, Code: violationInvalid, Message: message}
}

func snapshotEnvironment(environ []string) map[string]string {
	values := make(map[string]string, len(environ))
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name != "" {
			values[name] = value
		}
	}

	return values
}

func cloneEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	maps.Copy(cloned, environment)

	return cloned
}
