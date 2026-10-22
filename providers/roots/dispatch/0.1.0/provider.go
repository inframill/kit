// Package dispatch pushes provider workspace directories and runs Make through Ansible.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

type Provider struct{ Version, ToolVersion, Tool string }

type StaticSettings struct {
	ID              string `json:"id"`
	SettingsFile    string `json:"settings_file"`
	ProducerID      string `json:"producer_id"`
	ProducerVersion string `json:"producer_version"`
	Artifact        string `json:"artifact"`
	Connection      string `json:"connection"`
	HostKeyPolicy   string `json:"host_key_policy"`
}

type Settings struct {
	Schema      int                  `json:"schema"`
	AddressMode string               `json:"address_mode"`
	SSHUser     string               `json:"ssh_user"`
	Directories map[string]Directory `json:"directories"`
}

type Directory struct {
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Select      Selector `json:"select"`
	Exclude     []string `json:"exclude,omitempty"`
	Become      bool     `json:"become,omitempty"`
}

// Lists match any entry; fields intersect. An empty selector matches all nodes.
type Selector struct {
	Names        []string `json:"names,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
}

type Arguments struct {
	Directory string            `json:"directory"`
	Select    Selector          `json:"select,omitempty"`
	Targets   []string          `json:"targets,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
}

type prepared struct {
	provider Provider
	scope    runmill.ExecutionContext
	static   StaticSettings
	settings Settings
	args     Arguments
	command  string
}

var _ runmill.Provider = Provider{}
var userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var targetName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_./-]{0,127}$`)
var variableName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func (d Provider) Definition() (runmill.ProviderDefinition, error) {
	list := map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}}
	selector := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"names": list, "roles": list, "architecture": map[string]any{"enum": []string{"arm64", "amd64"}},
	}}
	commands := map[string]runmill.CommandDefinition{}
	for _, name := range []string{"push", "make"} {
		targets := map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": map[string]any{"type": "string", "pattern": targetName.String()}}
		required := []string{"directory"}
		if name == "make" {
			required = append(required, "targets")
			targets["minItems"] = 1
		}
		commands[name] = runmill.CommandDefinition{AccessMode: runmill.ReadWrite, Description: map[string]string{
			"push": "Push a workspace directory through Ansible, optionally followed by Make targets",
			"make": "Run ordered Make targets in a configured remote directory through Ansible",
		}[name], ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": map[string]any{
			"directory": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}, "select": selector, "targets": targets,
			"variables": map[string]any{"type": "object", "maxProperties": 100, "propertyNames": map[string]any{"pattern": variableName.String()}, "additionalProperties": map[string]any{"type": "string", "maxLength": 4096}},
		}}}
	}
	return runmill.ProviderDefinition{Name: "dispatch", Commands: commands}, nil
}

func decode(value any, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errors.New("invalid JSON input")
	}
	if string(data) == "null" {
		data = []byte("{}")
	}
	return artifacts.Decode(data, target)
}

func (d Provider) parseSettings(config runmill.Settings) (StaticSettings, error) {
	var s StaticSettings
	if err := decode(config, &s); err != nil {
		return s, err
	}
	if s.Connection == "" {
		s.Connection = "ssh"
	}
	if s.HostKeyPolicy == "" {
		s.HostKeyPolicy = "strict"
	}
	if !runmill.ValidID(s.ID) || !runmill.ValidID(s.ProducerID) || s.ProducerVersion == "" || !artifacts.Flat(s.SettingsFile) || !artifacts.Flat(s.Artifact) || !strings.HasPrefix(s.Artifact, s.ProducerID+"-nodes-") || s.Connection != "ssh" && s.Connection != "local" || s.HostKeyPolicy != "strict" && s.HostKeyPolicy != "accept-new" {
		return s, errors.New("invalid Dispatch static settings")
	}
	return s, nil
}

func (d Provider) ValidateSettings(ctx context.Context, config runmill.Settings) error {
	_, err := d.parseSettings(config)
	return errors.Join(ctx.Err(), err)
}

func validList(values []string, pattern *regexp.Regexp) bool {
	if len(values) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !pattern.MatchString(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func (s Selector) validate() error {
	if !validList(s.Names, artifacts.Identifier) || !validList(s.Roles, artifacts.Identifier) || s.Architecture != "" && s.Architecture != "arm64" && s.Architecture != "amd64" {
		return errors.New("invalid node selector")
	}
	return nil
}

func (d Provider) Prepare(ctx context.Context, scope runmill.ExecutionContext, command string, arguments runmill.Arguments) (runmill.PreparedExecution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	static, err := d.parseSettings(scope.Settings)
	if err != nil {
		return nil, err
	}
	if command != "push" && command != "make" {
		return nil, errors.New("unknown Dispatch command")
	}
	data, err := artifacts.Read(scope.WorkspaceDir, static.SettingsFile, artifacts.SettingsLimit)
	if err != nil {
		return nil, err
	}
	var settings Settings
	if err = artifacts.Decode(data, &settings); err != nil {
		return nil, err
	}
	if settings.Schema != 1 || settings.AddressMode != "public_ipv4" && settings.AddressMode != "private_ipv4" && settings.AddressMode != "fqdn" || !userName.MatchString(settings.SSHUser) || len(settings.Directories) == 0 || len(settings.Directories) > 100 {
		return nil, errors.New("invalid Dispatch workspace settings")
	}
	for name, directory := range settings.Directories {
		if !artifacts.Identifier.MatchString(name) || !artifacts.Relative(directory.Source) || !path.IsAbs(directory.Destination) || path.Clean(directory.Destination) != directory.Destination || directory.Destination == "/" || strings.ContainsAny(directory.Destination, "\\\x00\r\n") {
			return nil, errors.New("invalid directory mapping")
		}
		if err = directory.Select.validate(); err != nil {
			return nil, err
		}
		if err = validateExcludes(directory.Exclude); err != nil {
			return nil, err
		}
	}
	var args Arguments
	if err = decode(arguments, &args); err != nil {
		return nil, err
	}
	directory, ok := settings.Directories[args.Directory]
	if !ok {
		return nil, errors.New("select a declared directory")
	}
	if err = args.Select.validate(); err != nil {
		return nil, err
	}
	if !validList(args.Targets, targetName) || command == "make" && len(args.Targets) == 0 || len(args.Variables) > 100 || len(args.Variables) > 0 && len(args.Targets) == 0 {
		return nil, errors.New("invalid Make targets or variables")
	}
	for key, value := range args.Variables {
		if !variableName.MatchString(key) || len(value) > 4096 || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("invalid Make variable")
		}
	}
	p := &prepared{d, scope, static, settings, args, command}
	if _, _, err = p.inventory(); err != nil {
		return nil, err
	}
	if command == "push" {
		if err = walkSource(ctx, scope.WorkspaceDir, directory, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}
