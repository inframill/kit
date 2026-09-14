package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

const (
	Protocol   = 1
	MaxRequest = 4 << 20
	MaxInfo    = 1 << 20
	MaxResult  = 16 << 20
)

type Info struct {
	Name     string                       `json:"name"`
	Version  string                       `json:"version"`
	Protocol int                          `json:"protocol"`
	Commands map[string]CommandDefinition `json:"commands"`
}

type CommandDefinition struct {
	Description     string         `json:"description"`
	ArgumentsSchema map[string]any `json:"argumentsSchema"`
	Safety          SafetyClass    `json:"safety"`
	Effects         []string       `json:"effects"`
	Verification    string         `json:"verification"`
}

type Request struct {
	Protocol           int            `json:"protocol"`
	BlueprintDirectory string         `json:"blueprintDirectory,omitempty"`
	WorkspaceDirectory string         `json:"workspaceDirectory,omitempty"`
	ArtifactDirectory  string         `json:"artifactDirectory,omitempty"`
	Binding            *Binding       `json:"binding,omitempty"`
	CommandID          string         `json:"commandID,omitempty"`
	Values             map[string]any `json:"values,omitempty"`
	Config             Config         `json:"config"`
	Command            string         `json:"command,omitempty"`
	Arguments          map[string]any `json:"arguments,omitempty"`
}

func decode(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid Driver JSON document")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("driver output must contain exactly one JSON document")
	}
	return nil
}

func (info Info) Validate() error {
	if info.Protocol != Protocol {
		return errors.New("unsupported Driver protocol")
	}
	if !ValidID(info.Name) || info.Name == "builtin" {
		return errors.New("invalid or reserved Driver name")
	}
	if info.Version == "" || len(info.Version) > 128 || strings.IndexFunc(info.Version, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("driver version must be nonempty and contain no whitespace or control characters")
	}
	if len(info.Commands) == 0 {
		return errors.New("driver must declare commands")
	}
	for name, command := range info.Commands {
		if !validName.MatchString(name) || command.Description == "" || command.ArgumentsSchema == nil || len(command.Effects) == 0 || command.Verification == "" {
			return errors.New("incomplete Driver command metadata")
		}
		switch command.Safety {
		case ReadOnly, Reversible, Mutating, Destructive:
		default:
			return errors.New("invalid Driver command safety")
		}
	}
	return nil
}

// DecodeRequest decodes one bounded, strict protocol request for executable authors.
func DecodeRequest(data []byte, destination *Request) error {
	if len(data) > MaxRequest {
		return errors.New("driver request exceeds size limit")
	}
	return decode(data, destination)
}
