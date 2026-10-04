package ansible

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/inframill/kit/providers/roots/internal/toolrun"
)

//go:embed callback.py
var callback []byte

type Stats struct {
	OK          int `json:"ok"`
	Changed     int `json:"changed"`
	Failures    int `json:"failures"`
	Unreachable int `json:"unreachable"`
	Skipped     int `json:"skipped"`
	Rescued     int `json:"rescued"`
	Ignored     int `json:"ignored"`
}
type Recap struct {
	Schema int              `json:"schema"`
	Hosts  map[string]Stats `json:"hosts"`
}
type Result struct {
	Hosts           map[string]Stats `json:"hosts"`
	Prediction      bool             `json:"prediction"`
	ProviderVersion string           `json:"provider_version"`
}

func (p *prepared) Execute(ctx context.Context, secrets map[string]string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var nodes artifacts.Nodes
	var inventory inventoryDocument
	var err error
	if p.command != "syntax-check" {
		inventory, nodes, err = p.inventory()
		if err != nil {
			return nil, err
		}
		if err = toolrun.Public(inventory, secrets); err != nil {
			return nil, err
		}
	}
	if p.command == "inventory" {
		if err = artifacts.Write(p.scope.WorkspaceDir, "kit-inventory.json", inventory); err != nil {
			return nil, err
		}
		return struct {
			Inventory           string `json:"inventory"`
			Hosts               int    `json:"hosts"`
			ProducerExecutionID string `json:"producer_execution_id"`
		}{"kit-inventory.json", len(nodes.Nodes), nodes.ExecutionID}, nil
	}
	tool := p.provider.Tool
	if tool == "" {
		tool = "/opt/inframill/tools/ansible/" + p.provider.ToolVersion + "/bin/ansible-playbook"
	}
	run := toolrun.Runner{Tool: tool, Directory: p.scope.WorkspaceDir, Environment: toolrun.Environment(), Logs: p.scope.Diagnostics, Secrets: secrets}
	tmp, err := os.MkdirTemp(p.scope.WorkspaceDir, ".kit-ansible-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for name, data := range map[string][]byte{"callback.py": callback, "ansible.cfg": []byte("[defaults]\n")} {
		if err = artifacts.WriteBytes(tmp, name, data); err != nil {
			return nil, err
		}
	}
	if p.command == "syntax-check" {
		inventory = inventoryDocument{All: inventoryGroup{Hosts: map[string]host{"localhost": {Connection: "local"}}}}
	}
	if err = artifacts.Write(tmp, "inventory.json", inventory); err != nil {
		return nil, err
	}
	playbook := p.static.Playbooks[p.args.Playbook]
	if p.command == "ping" {
		// Use the same interpreter, inventory and callback as apply, without
		// running a user playbook or waiting for a guest to finish booting.
		if err = artifacts.WriteBytes(tmp, "ping.yml", []byte("- hosts: all\n  gather_facts: false\n  tasks:\n    - name: Verify connectivity and Python\n      ansible.builtin.ping:\n      timeout: 30\n")); err != nil {
			return nil, err
		}
		playbook = filepath.Join(tmp, "ping.yml")
	}
	args := []string{"-i", filepath.Join(tmp, "inventory.json"), playbook}
	if p.command == "syntax-check" {
		args = append(args, "--syntax-check")
	}
	if p.command == "check" {
		args = append(args, "--check")
	}
	if len(p.args.Hosts) > 0 {
		args = append(args, "--limit", strings.Join(p.args.Hosts, ","))
	}
	if len(p.args.Tags) > 0 {
		args = append(args, "--tags", strings.Join(p.args.Tags, ","))
	}
	policy := "yes"
	if p.static.HostKeyPolicy == "accept-new" {
		policy = "accept-new"
	}
	run.Environment = append(run.Environment, "ANSIBLE_CONFIG="+filepath.Join(tmp, "ansible.cfg"), "ANSIBLE_CALLBACK_PLUGINS="+tmp, "ANSIBLE_STDOUT_CALLBACK=callback", "ANSIBLE_HOST_KEY_CHECKING=True", "ANSIBLE_RETRY_FILES_ENABLED=False", "ANSIBLE_ROLES_PATH="+filepath.Join(p.scope.WorkspaceDir, "roles"), "ANSIBLE_SSH_ARGS=-o BatchMode=yes -o StrictHostKeyChecking="+policy)
	if p.static.Connection == "local" {
		run.Environment = append(run.Environment, "ANSIBLE_PYTHON_INTERPRETER="+filepath.Join(filepath.Dir(tool), "python3"))
	}
	if err = run.Verify(ctx, "ansible", p.provider.ToolVersion); err != nil {
		return nil, err
	}
	if err = p.scope.Message("Ansible " + p.command); err != nil {
		return nil, err
	}
	data, code, err := run.Call(ctx, p.command != "syntax-check", args...)
	if err = toolrun.Success(code, err); err != nil {
		return nil, err
	}
	if p.command == "syntax-check" {
		return struct {
			Valid bool `json:"syntax_valid"`
		}{true}, nil
	}
	var recap Recap
	if err = artifacts.Decode(data, &recap); err != nil {
		return nil, errors.New("missing or malformed Ansible recap")
	}
	expected := p.expected(nodes)
	if recap.Schema != 1 || len(recap.Hosts) != len(expected) || len(expected) == 0 {
		return nil, errors.New("incomplete Ansible recap")
	}
	for _, name := range expected {
		stats, ok := recap.Hosts[name]
		if !ok || stats.OK < 1 || stats.Changed < 0 || stats.Skipped < 0 || stats.Failures != 0 || stats.Unreachable != 0 || stats.Rescued != 0 || stats.Ignored != 0 {
			return nil, errors.New("Ansible recap did not verify every selected host")
		}
	}
	result := Result{recap.Hosts, p.command == "check", p.provider.Version}
	if err = toolrun.Public(result, secrets); err != nil {
		return nil, err
	}
	return result, nil
}
