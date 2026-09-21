package ansible

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inframill/kit/drivers/roots/internal/artifacts"
	driver "github.com/sir-hassan/inframill/pkg/driver"
)

type Driver struct{ Version, ToolVersion, Tool string }
type StaticSettings struct {
	Version         string            `json:"required_driver_version"`
	ID              string            `json:"id"`
	SettingsFile    string            `json:"settings_file"`
	ProducerID      string            `json:"producer_id"`
	ProducerVersion string            `json:"producer_version"`
	Artifact        string            `json:"artifact"`
	Connection      string            `json:"connection"`
	HostKeyPolicy   string            `json:"host_key_policy"`
	Roles           []string          `json:"roles"`
	Playbooks       map[string]string `json:"playbooks"`
}
type Settings struct {
	Schema      int    `json:"schema"`
	AddressMode string `json:"address_mode"`
	SSHUser     string `json:"ssh_user"`
}
type Arguments struct {
	Playbook string   `json:"playbook,omitempty"`
	Hosts    []string `json:"hosts,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}
type prepared struct {
	driver   Driver
	scope    driver.ExecutionContext
	static   StaticSettings
	settings Settings
	args     Arguments
	command  string
}

var _ driver.Driver = Driver{}
var userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (d Driver) Definition() (driver.Definition, error) {
	commands := map[string]driver.CommandDefinition{"inventory": {Description: "Write inventory from a verified node artifact", ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}}}
	list := map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}}
	for _, name := range []string{"syntax-check", "check", "apply"} {
		commands[name] = driver.CommandDefinition{Description: name + " the selected playbook and verify the outcome", ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"playbook"}, "properties": map[string]any{"playbook": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}, "hosts": list, "tags": list}}}
	}
	syntax := commands["syntax-check"]
	syntax.ArgumentsSchema["properties"] = map[string]any{"playbook": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}}
	commands["syntax-check"] = syntax
	return driver.Definition{Name: "ansible", Commands: commands}, nil
}
func (d Driver) parseSettings(config driver.Settings) (StaticSettings, error) {
	var m StaticSettings
	data, e := json.Marshal(config)
	if e != nil {
		return m, errors.New("invalid static settings")
	}
	if e = artifacts.Decode(data, &m); e != nil {
		return m, e
	}
	if m.Version != d.Version || !driver.ValidID(m.ID) || !driver.ValidID(m.ProducerID) || m.ProducerVersion == "" || !artifacts.Flat(m.SettingsFile) || !artifacts.Flat(m.Artifact) || !strings.HasPrefix(m.Artifact, m.ProducerID+"-nodes-") || m.Connection != "local" && m.Connection != "ssh" || m.HostKeyPolicy != "strict" && m.HostKeyPolicy != "accept-new" || len(m.Playbooks) == 0 {
		return m, errors.New("invalid Ansible static or version")
	}
	for name, path := range m.Playbooks {
		if !artifacts.Identifier.MatchString(name) || !artifacts.Relative(path) {
			return m, errors.New("invalid playbook selection")
		}
	}
	seen := map[string]bool{}
	for _, role := range m.Roles {
		if !artifacts.Identifier.MatchString(role) || role == "all" || role == "ungrouped" || seen[role] {
			return m, errors.New("invalid role")
		}
		seen[role] = true
	}
	return m, nil
}
func (d Driver) ValidateSettings(ctx context.Context, config driver.Settings) error {
	_, err := d.parseSettings(config)
	return errors.Join(ctx.Err(), err)
}
func (d Driver) Prepare(ctx context.Context, scope driver.ExecutionContext, command string, args map[string]any) (driver.PreparedCommand, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, err := d.parseSettings(scope.DriverSettings)
	if err != nil {
		return nil, err
	}
	if filepath.Base(scope.WorkspaceDirectory) != m.ID {
		return nil, errors.New("static id must match workspace driver id")
	}
	var s Settings
	data, err := artifacts.Read(scope.WorkspaceDirectory, m.SettingsFile, artifacts.SettingsLimit)
	if err != nil {
		return nil, err
	}
	if err = artifacts.Decode(data, &s); err != nil {
		return nil, err
	}
	if s.Schema != 1 || s.AddressMode != "public_ipv4" && s.AddressMode != "private_ipv4" && s.AddressMode != "fqdn" || !userName.MatchString(s.SSHUser) {
		return nil, errors.New("invalid Ansible workspace settings")
	}
	var a Arguments
	data, err = json.Marshal(args)
	if err != nil {
		return nil, errors.New("invalid arguments")
	}
	if string(data) == "null" {
		data = []byte("{}")
	}
	if err = artifacts.Decode(data, &a); err != nil {
		return nil, err
	}
	switch command {
	case "inventory":
		if len(args) > 0 {
			return nil, errors.New("inventory takes no arguments")
		}
	case "syntax-check", "check", "apply":
		path, ok := m.Playbooks[a.Playbook]
		if !ok {
			return nil, errors.New("select a declared playbook")
		}
		if _, err = artifacts.Read(scope.WorkspaceDirectory, path, artifacts.Limit); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown Ansible command")
	}
	for _, values := range [][]string{a.Hosts, a.Tags} {
		if len(values) > 100 {
			return nil, errors.New("too many selection entries")
		}
		seen := map[string]bool{}
		for _, v := range values {
			if !artifacts.Identifier.MatchString(v) || seen[v] {
				return nil, errors.New("invalid or duplicate selection")
			}
			seen[v] = true
		}
	}
	if command == "syntax-check" && (len(a.Hosts) > 0 || len(a.Tags) > 0) {
		return nil, errors.New("syntax-check does not select hosts or tags")
	}
	p := &prepared{d, scope, m, s, a, command}
	if command != "syntax-check" {
		if _, _, err = p.inventory(); err != nil {
			return nil, err
		}
	}
	return p, nil
}
