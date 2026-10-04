package ansible

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

type Provider struct{ Version, ToolVersion, Tool string }
type StaticSettings struct {
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
	provider Provider
	scope    runmill.ExecutionContext
	static   StaticSettings
	settings Settings
	args     Arguments
	command  string
}

var _ runmill.Provider = Provider{}
var userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (d Provider) Definition() (runmill.ProviderDefinition, error) {
	commands := map[string]runmill.CommandDefinition{"inventory": {Description: "Write inventory from a verified node artifact", ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}}}
	list := map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}}
	commands["ping"] = runmill.CommandDefinition{Description: "Verify SSH/local connectivity and Python on every selected host", ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"hosts": list}}}
	for _, name := range []string{"syntax-check", "check", "apply"} {
		commands[name] = runmill.CommandDefinition{Description: name + " the selected playbook and verify the outcome", ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"playbook"}, "properties": map[string]any{"playbook": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}, "hosts": list, "tags": list}}}
	}
	syntax := commands["syntax-check"]
	syntax.ArgumentsSchema["properties"] = map[string]any{"playbook": map[string]any{"type": "string", "pattern": artifacts.Identifier.String()}}
	commands["syntax-check"] = syntax
	return runmill.ProviderDefinition{Name: "ansible", Commands: commands}, nil
}
func (d Provider) parseSettings(config runmill.Settings) (StaticSettings, error) {
	var m StaticSettings
	data, e := json.Marshal(config)
	if e != nil {
		return m, errors.New("invalid static settings")
	}
	if e = artifacts.Decode(data, &m); e != nil {
		return m, e
	}
	if !runmill.ValidID(m.ID) || !runmill.ValidID(m.ProducerID) || m.ProducerVersion == "" || !artifacts.Flat(m.SettingsFile) || !artifacts.Flat(m.Artifact) || !strings.HasPrefix(m.Artifact, m.ProducerID+"-nodes-") || m.Connection != "local" && m.Connection != "ssh" || m.HostKeyPolicy != "strict" && m.HostKeyPolicy != "accept-new" || len(m.Playbooks) == 0 {
		return m, errors.New("invalid Ansible static settings")
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
func (d Provider) ValidateSettings(ctx context.Context, config runmill.Settings) error {
	_, err := d.parseSettings(config)
	return errors.Join(ctx.Err(), err)
}
func (d Provider) Prepare(ctx context.Context, scope runmill.ExecutionContext, command string, args runmill.Arguments) (runmill.PreparedExecution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, err := d.parseSettings(scope.Settings)
	if err != nil {
		return nil, err
	}
	var s Settings
	data, err := artifacts.Read(scope.WorkspaceDir, m.SettingsFile, artifacts.SettingsLimit)
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
	case "ping":
		for key := range args {
			if key != "hosts" {
				return nil, errors.New("ping only accepts hosts")
			}
		}
	case "syntax-check", "check", "apply":
		path, ok := m.Playbooks[a.Playbook]
		if !ok {
			return nil, errors.New("select a declared playbook")
		}
		if _, err = artifacts.Read(scope.WorkspaceDir, path, artifacts.Limit); err != nil {
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
