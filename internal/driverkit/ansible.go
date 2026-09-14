package driverkit

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inframill/kit/internal/catalog"
	wire "github.com/sir-hassan/inframill/pkg/driver"
)

//go:embed kit_callback.py
var callback []byte

func (r runner) ansible(ctx context.Context, release catalog.Release, c Config, a Arguments, req wire.Request) (any, error) {
	playbook := c.Playbooks[a.Playbook]
	if _, e := readPrivate(req.WorkspaceDirectory, playbook, maxArtifact); e != nil {
		return nil, e
	}
	args := []string{"-i", "kit-inventory.json", playbook}
	if req.Command == "syntax-check" {
		// Syntax checks do not require an earlier infrastructure apply.
		if e := writePrivate(req.WorkspaceDirectory, "kit-syntax-inventory.json", map[string]any{"all": map[string]any{"hosts": map[string]any{"localhost": map[string]any{"ansible_connection": "local"}}}}); e != nil {
			return nil, e
		}
		args = []string{"-i", "kit-syntax-inventory.json", playbook, "--syntax-check"}
	} else {
		if _, e := inventory(c, req); e != nil {
			return nil, e
		}
		if len(a.Hosts) > 0 {
			ns, e := loadNodes(c, req)
			if e != nil {
				return nil, e
			}
			known := map[string]bool{}
			for _, n := range ns.Nodes {
				known[n.Name] = true
			}
			for _, h := range a.Hosts {
				if !known[h] {
					return nil, fmt.Errorf("unknown host %s", h)
				}
			}
			args = append(args, "--limit", strings.Join(a.Hosts, ","))
		}
		if len(a.Tags) > 0 {
			args = append(args, "--tags", strings.Join(a.Tags, ","))
		}
		if req.Command == "check" {
			args = append(args, "--check")
		}
	}
	tmp, e := os.MkdirTemp(req.WorkspaceDirectory, ".kit-callback-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(tmp)
	if e = writeBytes(tmp, "kit_callback.py", callback); e != nil {
		return nil, e
	}
	if e = writeBytes(tmp, "ansible.cfg", []byte("[defaults]\n")); e != nil {
		return nil, e
	}
	// Ignore user ansible.cfg overrides to the inventory, callbacks and authentication.
	r.env = append(r.env, "ANSIBLE_CONFIG="+filepath.Join(tmp, "ansible.cfg"), "ANSIBLE_CALLBACK_PLUGINS="+tmp, "ANSIBLE_STDOUT_CALLBACK=kit_callback", "ANSIBLE_HOST_KEY_CHECKING=True", "ANSIBLE_RETRY_FILES_ENABLED=False", "ANSIBLE_ROLES_PATH="+filepath.Join(req.WorkspaceDirectory, "roles"), "ANSIBLE_SSH_ARGS=-o BatchMode=yes -o StrictHostKeyChecking="+map[string]string{"strict": "yes", "accept-new": "accept-new"}[c.HostKeyPolicy])
	if c.Connection == "local" {
		r.env = append(r.env, "ANSIBLE_PYTHON_INTERPRETER="+filepath.Join(filepath.Dir(r.tool), "python3"))
	}
	if req.Command == "syntax-check" {
		_, code, e := r.call(ctx, false, args...)
		return map[string]bool{"syntax_valid": true}, toolSuccess(code, e)
	}
	b, code, e := r.call(ctx, true, args...)
	if e = toolSuccess(code, e); e != nil {
		return nil, e
	}
	var recap struct {
		Schema int `json:"schema"`
		Hosts  map[string]struct {
			OK          int `json:"ok"`
			Changed     int `json:"changed"`
			Failures    int `json:"failures"`
			Unreachable int `json:"unreachable"`
			Skipped     int `json:"skipped"`
			Rescued     int `json:"rescued"`
			Ignored     int `json:"ignored"`
		} `json:"hosts"`
	}
	if e = decode(b, &recap); e != nil {
		return nil, errors.New("missing or malformed Ansible recap")
	}
	if recap.Schema != 1 || len(recap.Hosts) == 0 {
		return nil, errors.New("empty Ansible selection or missing recap")
	}
	expected, e := loadNodes(c, req)
	if e != nil {
		return nil, e
	}
	known := map[string]bool{}
	for _, n := range expected.Nodes {
		known[n.Name] = true
	}
	if len(a.Hosts) > 0 {
		known = map[string]bool{}
		for _, h := range a.Hosts {
			known[h] = true
		}
	}
	if len(recap.Hosts) != len(known) {
		return nil, errors.New("Ansible recap does not cover the selected hosts")
	}
	for host, s := range recap.Hosts {
		if !known[host] {
			return nil, errors.New("unexpected host in Ansible recap")
		}
		if s.Failures > 0 || s.Unreachable > 0 || s.Ignored > 0 || s.Rescued > 0 || s.OK < 1 {
			return nil, errors.New("Ansible recap lacks successful verification")
		}
	}
	// Ensure unexpected callback additions do not escape the stable result shape.
	summary, _ := json.Marshal(recap.Hosts)
	var hosts any
	_ = json.Unmarshal(summary, &hosts)
	return map[string]any{"hosts": hosts, "prediction": req.Command == "check", "driver_version": release.Version}, nil
}
