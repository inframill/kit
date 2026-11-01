package runmill

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ProtocolVersion         = 1
	MaxRequestBytes         = 4 << 20
	MaxValidationReplyBytes = 1 << 20
	MaxExecutionReplyBytes  = 16 << 20
)

type ProviderInfo struct {
	Name            string                       `json:"name"`
	Version         string                       `json:"version"`
	ProtocolVersion int                          `json:"protocolVersion"`
	Commands        map[string]CommandDefinition `json:"commands"`
}

type CommandDefinition struct {
	AccessMode  AccessMode `json:"accessMode"`
	Description string     `json:"description"`
}

// AccessMode declares whole-Instance workspace access to scheduling hosts.
type AccessMode string

const (
	ReadOnly  AccessMode = "read_only"
	ReadWrite AccessMode = "read_write"
)

func decode(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid Provider JSON document")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("provider output must contain exactly one JSON document")
	}
	return nil
}

func (info ProviderInfo) Validate() error {
	if info.ProtocolVersion != ProtocolVersion {
		return errors.New("unsupported Provider protocol")
	}
	if !ValidID(info.Name) {
		return errors.New("invalid Provider name")
	}
	if info.Version == "" || len(info.Version) > 128 || strings.IndexFunc(info.Version, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("provider version must be nonempty and contain no whitespace or control characters")
	}
	if len(info.Commands) == 0 {
		return errors.New("provider must declare commands")
	}
	for name, command := range info.Commands {
		if command.AccessMode != ReadOnly && command.AccessMode != ReadWrite {
			return errors.New("command accessMode must be read_only or read_write")
		}
		if !ValidID(name) || command.Description == "" {
			return errors.New("incomplete Provider command metadata")
		}
	}
	return nil
}

// copyJSON normalizes dynamic JSON and isolates maps/slices at ownership boundaries.
func copyJSON[T any](input T) (T, error) {
	var output T
	data, err := json.Marshal(input)
	if err != nil {
		return output, errors.New("invalid Provider JSON input")
	}
	if err := decode(data, &output); err != nil {
		return output, err
	}
	return output, nil
}

// Transport types are deliberately separate from author callback inputs.
type settingsParams struct {
	Settings Settings `json:"settings"`
}

type invocationParams struct {
	settingsParams
	Command      string    `json:"command"`
	Arguments    Arguments `json:"arguments,omitempty"`
	WorkspaceDir string    `json:"workspaceDir"`
	ArtifactDir  string    `json:"artifactDir"`
	ExecutionID  string    `json:"executionId,omitempty"`
}

func (r settingsParams) validate() error {
	if r.Settings == nil {
		return errors.New("settings must be an object")
	}
	return nil
}

func (r invocationParams) validate() error {
	if err := r.settingsParams.validate(); err != nil {
		return err
	}
	if !ValidID(r.Command) || r.WorkspaceDir == "" || r.ArtifactDir == "" {
		return errors.New("command, workspaceDir and artifactDir are required")
	}
	if r.ExecutionID == "" {
		return errors.New("executionId is required")
	}
	return nil
}

func objectOrEmpty(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

// runParams carries execution-only secrets.
type runParams struct {
	invocationParams
	Secrets json.RawMessage `json:"secrets"`
}

const maxSecretBytes = 1 << 20

var secretNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

// encodeSecrets validates caller maps before any subprocess receives them.
func encodeSecrets(secrets map[string]string) ([]byte, error) {
	if secrets == nil {
		return []byte("{}"), nil
	}
	size := 2
	for name, value := range secrets {
		if !secretNamePattern.MatchString(name) {
			return nil, errors.New("invalid secret name")
		}
		if !utf8.ValidString(value) {
			return nil, errors.New("secret values must be UTF-8 strings")
		}
		size += len(name) + len(value)
		if size > maxSecretBytes {
			return nil, errors.New("secrets exceed 1 MiB")
		}
	}
	data, err := json.Marshal(secrets)
	if err != nil || len(data) > maxSecretBytes {
		return nil, errors.New("secrets exceed 1 MiB")
	}
	return data, nil
}

// decodeSecrets rejects duplicate names and non-string values, including null.
// A normal map unmarshal would silently accept duplicates and null strings.
func decodeSecrets(data []byte) (map[string]string, error) {
	if len(data) > maxSecretBytes {
		return nil, errors.New("secrets exceed 1 MiB")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("secrets must be UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("secrets must be a JSON object of strings")
	}
	secrets := map[string]string{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, errors.New("invalid secret name")
		}
		if _, exists := secrets[name]; exists {
			return nil, errors.New("duplicate secret name")
		}
		token, err = d.Token()
		value, ok := token.(string)
		if err != nil || !ok {
			return nil, errors.New("secret values must be strings")
		}
		secrets[name] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid secrets JSON")
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("secrets must contain exactly one JSON object")
	}
	if _, err := encodeSecrets(secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}
