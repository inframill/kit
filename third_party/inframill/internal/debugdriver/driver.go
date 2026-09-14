// Package debugdriver implements local, controllable commands for testing Inframill.
package debugdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/internal/platform/secretenv"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	failCommand   = "fail"
	blockMode     = "block"
	schemaType    = "type"
	messageKey    = "message"
	writeArtifact = "write-artifact"
	readArtifact  = "read-artifact"
	secretCommand = "secret"
)

type Driver struct{}

func (Driver) Name() string { return "debug" }
func (Driver) ValidateConfig(config drivers.Config) error {
	if len(config) != 0 {
		return errors.New("debug config must be an empty object")
	}
	return nil
}

func (d Driver) ValidateBlueprint(_ string, config drivers.Config) error {
	return d.ValidateConfig(config)
}

func (Driver) ValidateInstance(input InstanceValidation) []Violation {
	if value, ok := input.Values["mode"]; ok {
		mode, valid := value.(string)
		if !valid || !validMode(mode) {
			return []Violation{{Code: "DEBUG_MODE_INVALID", Path: input.Path + ".mode", Message: "mode must be success, fail or block"}}
		}
	}
	if value, ok := input.Values["secret_ref"]; ok {
		if _, valid := value.(string); !valid {
			return []Violation{{Code: "DEBUG_SECRET_REF_INVALID", Path: input.Path + ".secret_ref", Message: "secret_ref must be a string"}}
		}
	}
	return nil
}

func validMode(mode string) bool {
	return mode == "success" || mode == failCommand || mode == blockMode
}

type arguments struct {
	Message    string `json:"message,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content,omitempty"`
	Mode       string `json:"mode,omitempty"`
	DurationMS *int   `json:"durationMs,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Reference  string `json:"reference,omitempty"`
}

type Record struct {
	Protocol  int                 `json:"protocol"`
	CommandID string              `json:"commandID"`
	Binding   drivers.Binding     `json:"binding"`
	Values    map[string]any      `json:"values"`
	Arguments inframill.Arguments `json:"arguments"`
}

type ArtifactResult struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type EchoResult struct {
	Message string          `json:"message"`
	Values  map[string]any  `json:"values"`
	Binding drivers.Binding `json:"binding"`
}

type RecordResult struct {
	Recorded  bool   `json:"recorded"`
	Artifact  string `json:"artifact"`
	CommandID string `json:"commandID"`
}

func (Driver) Definition() (Definition, error) {
	stringField := func() map[string]any { return map[string]any{schemaType: "string"} }
	command := func(safety inframill.SafetyClass, description, verification string, properties map[string]any, required ...string) inframill.CommandDefinition {
		schema := map[string]any{schemaType: "object", "properties": properties, "additionalProperties": false}
		if len(required) != 0 {
			schema["required"] = required
		}
		return inframill.CommandDefinition{Description: description, Safety: safety, Effects: []string{description}, Verification: verification, ArgumentsSchema: schema}
	}
	return Definition{Name: "debug", Commands: map[string]inframill.CommandDefinition{
		"echo":        command(inframill.ReadOnly, "Returns arguments, Values and Instance binding", "Returned message equals the argument", map[string]any{messageKey: stringField()}),
		writeArtifact: command(inframill.Mutating, "Writes one private local artifact", "Reads back the exact written content", map[string]any{"name": stringField(), "content": stringField()}, "name", "content"),
		readArtifact:  command(inframill.ReadOnly, "Reads one local artifact", "Artifact is a regular file within size limits", map[string]any{"name": stringField()}, "name"),
		"record":      command(inframill.Mutating, "Records invocation before optional failure or waiting", "Reads back the CommandID record before succeeding", map[string]any{messageKey: stringField(), "mode": map[string]any{schemaType: "string", "enum": []string{"success", failCommand, blockMode}}}),
		"logs":        command(inframill.ReadOnly, "Emits messages on the merged log stream", "Both requested writes succeed", map[string]any{"stdout": stringField(), "stderr": stringField()}),
		failCommand:   command(inframill.ReadOnly, "Returns a deliberate command failure", "Always fails without writing artifacts", map[string]any{messageKey: stringField()}),
		"wait":        command(inframill.ReadOnly, "Waits locally for a duration or workspace release file", "Duration elapsed or release file observed", map[string]any{"durationMs": map[string]any{schemaType: "integer", "minimum": 0, "maximum": 60000}}),
		secretCommand: command(inframill.ReadOnly, "Exercises host secret redaction in result and logs", "Secret reference resolves at runtime", map[string]any{"reference": stringField()}),
	}}, nil
}

func (d Driver) Prepare(ctx context.Context, scope ExecutionContext, name string, args inframill.Arguments) (drivers.PreparedCommand, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.ValidateConfig(scope.DriverConfig); err != nil {
		return nil, err
	}
	definition, err := d.Definition()
	if err != nil {
		return nil, err
	}
	op, ok := definition.Commands[name]
	if !ok {
		return nil, errors.New("unknown debug command")
	}
	properties, ok := op.ArgumentsSchema["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid debug argument schema")
	}
	for key, value := range args {
		if _, found := properties[key]; !found || value == nil {
			return nil, errors.New("invalid debug argument")
		}
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, errors.New("invalid debug arguments")
	}
	var parsed arguments
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, errors.New("invalid debug argument types")
	}
	if required, ok := op.ArgumentsSchema["required"].([]string); ok {
		for _, key := range required {
			if _, present := args[key]; !present {
				return nil, errors.New("missing required debug argument")
			}
		}
	}
	if name == readArtifact || name == writeArtifact {
		if parsed.Name == "" || parsed.Name == "." || parsed.Name == ".." || strings.ContainsAny(parsed.Name, "/\\\x00") {
			return nil, errors.New("artifact name must be a single filename")
		}
		if len(parsed.Content) > 1<<20 {
			return nil, errors.New("debug artifact content exceeds 1 MiB")
		}
	}
	if _, supplied := args["mode"]; supplied && !validMode(parsed.Mode) {
		return nil, errors.New("mode must be success, fail or block")
	}
	if parsed.DurationMS != nil && (*parsed.DurationMS < 0 || *parsed.DurationMS > 60000) {
		return nil, errors.New("durationMs must be between 0 and 60000")
	}
	return execution{scope: scope, name: name, args: parsed, raw: args}, nil
}

type execution struct {
	scope ExecutionContext
	name  string
	args  arguments
	raw   inframill.Arguments
}

func (e execution) Execute(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch e.name {
	case "echo":
		return EchoResult{Message: e.args.Message, Values: e.scope.Values, Binding: e.scope.Binding}, nil
	case writeArtifact:
		if err := e.write(ctx, e.args.Name, []byte(e.args.Content)); err != nil {
			return nil, err
		}
		return ArtifactResult{Name: e.args.Name, Content: e.args.Content}, nil
	case readArtifact:
		if e.scope.Artifacts == nil {
			return nil, errors.New("artifact access is required")
		}
		data, err := e.scope.Artifacts.ReadFile(ctx, e.args.Name)
		if err != nil {
			return nil, err
		}
		if len(data) > 1<<20 {
			return nil, errors.New("debug artifact content exceeds 1 MiB")
		}
		return ArtifactResult{Name: e.args.Name, Content: string(data)}, nil
	case "record":
		return e.record(ctx)
	case "logs":
		if err := writeLog(e.scope.Stderr, e.args.Stdout); err != nil {
			return nil, err
		}
		if err := writeLog(e.scope.Stderr, e.args.Stderr); err != nil {
			return nil, err
		}
		return map[string]bool{"logged": true}, nil
	case failCommand:
		message := e.args.Message
		if message == "" {
			message = "deliberate debug failure"
		}
		return nil, errors.New(message)
	case "wait":
		if err := e.wait(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"waited": true}, nil
	case secretCommand:
		reference := e.args.Reference
		if reference == "" {
			reference = stringValue(e.scope.Values["secret_ref"])
		}
		secret, err := (secretenv.Resolver{}).Resolve(ctx, inframill.SecretRef{Name: reference})
		if err != nil {
			return nil, err
		}
		if err := writeLog(e.scope.Stderr, secret.Reveal()+"\n"); err != nil {
			return nil, err
		}
		return map[string]string{secretCommand: secret.Reveal()}, nil
	}
	return nil, errors.New("unknown debug command")
}

func writeLog(writer io.Writer, message string) error {
	if writer == nil {
		return nil
	}
	_, err := io.WriteString(writer, message)
	return err
}

func (e execution) write(ctx context.Context, name string, data []byte) error {
	if e.scope.Artifacts == nil {
		return errors.New("artifact access is required")
	}
	// Debug artifacts must never persist a known runtime secret, even when supplied
	// accidentally through Values or Args. Check plain and JSON-escaped forms.
	for _, entry := range os.Environ() {
		key, secret, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "INFRAMILL_SECRET_") || secret == "" {
			continue
		}
		encoded, err := json.Marshal(secret)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, encoded[1:len(encoded)-1]) {
			return errors.New("artifact contains a known secret")
		}
	}
	if err := e.scope.Artifacts.WriteFile(ctx, name, data); err != nil {
		return err
	}
	saved, err := e.scope.Artifacts.ReadFile(ctx, name)
	if err != nil {
		return err
	}
	if !bytes.Equal(saved, data) {
		return errors.New("artifact verification failed")
	}
	return nil
}

func (e execution) record(ctx context.Context) (any, error) {
	if !inframill.ValidUUID(e.scope.CommandID) {
		return nil, errors.New("record requires a CommandID")
	}
	if e.scope.Artifacts == nil {
		return nil, errors.New("artifact access is required")
	}
	name := "record-" + e.scope.CommandID + ".json"
	if _, err := e.scope.Artifacts.ReadFile(ctx, name); err == nil {
		return nil, errors.New("command is already recorded")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	data, err := json.Marshal(Record{Protocol: drivers.Protocol, CommandID: e.scope.CommandID, Binding: e.scope.Binding, Values: e.scope.Values, Arguments: e.raw})
	if err != nil {
		return nil, err
	}
	if err := e.write(ctx, name, data); err != nil {
		return nil, err
	}
	if err := writeLog(e.scope.Stderr, "recorded\n"); err != nil {
		return nil, err
	}
	mode := e.args.Mode
	if mode == "" {
		mode = stringValue(e.scope.Values["mode"])
	}
	if mode == failCommand {
		return nil, errors.New("deliberate debug failure after recording")
	}
	if mode == blockMode {
		if err := e.wait(ctx); err != nil {
			return nil, err
		}
	}
	return RecordResult{Recorded: true, Artifact: name, CommandID: e.scope.CommandID}, nil
}

func (e execution) wait(ctx context.Context) error {
	duration := 60 * time.Second
	if e.args.DurationMS != nil {
		duration = time.Duration(*e.args.DurationMS) * time.Millisecond
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	if e.args.DurationMS != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	if err := writeLog(e.scope.Stderr, "waiting for release\n"); err != nil {
		return err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(filepath.Join(e.scope.WorkspaceDirectory, "release"))
		if err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("release must be a regular file")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("read release: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("debug release wait timed out")
		case <-ticker.C:
		}
	}
}

func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}
