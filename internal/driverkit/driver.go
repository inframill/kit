// Package driverkit implements the protocol-1 tool drivers.
package driverkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/inframill/kit/internal/catalog"
	wire "github.com/sir-hassan/inframill/pkg/driver"
)

type Options struct {
	Tool string
}

func Info(r catalog.Release) wire.Info {
	commands := map[string]wire.CommandDefinition{}
	add := func(name string, safety wire.SafetyClass, effects, verification string, props map[string]any, required ...string) {
		if props == nil {
			props = map[string]any{}
		}
		schema := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		commands[name] = wire.CommandDefinition{Description: name + " using " + r.Name, Safety: safety, Effects: []string{effects}, Verification: verification, ArgumentsSchema: schema}
	}
	str := map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	boolean := map[string]any{"type": "boolean"}
	ids := map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": map[string]any{"type": "string", "pattern": identifier.String()}}
	if r.Name == "opentofu" {
		add("init", wire.Mutating, "writes provider cache and backend configuration; contacts registry and backend", "tool exit status", nil)
		add("validate", wire.ReadOnly, "reads prepared configuration/providers", "OpenTofu validation result", nil)
		props := map[string]any{"destroy": boolean}
		props["refresh_only"] = boolean
		add("plan", wire.Mutating, "reads infrastructure, acquires backend locks, saves a private plan", "detailed exit status and bound plan integrity", props)
		for _, n := range []string{"apply", "destroy"} {
			s := wire.Mutating
			if n == "destroy" {
				s = wire.Destructive
			}
			add(n, s, "changes infrastructure and state; invalidates inventory", "saved plan checks and normalized outputs or empty managed state", map[string]any{"plan_id": str}, "plan_id")
		}
		add("output", wire.Mutating, "reads state and refreshes shared node artifact", "allowlisted output and Instance binding", nil)
	} else {
		add("inventory", wire.Reversible, "writes private inventory", "node binding, schema, addresses and host uniqueness", nil)
		for _, n := range []string{"syntax-check", "check", "apply"} {
			s := wire.ReadOnly
			effect := "parses declared playbook"
			verification := "syntax check exit status"
			if n != "syntax-check" {
				s = wire.Mutating
				effect = "contacts guests and executes playbook (check mode is a prediction)"
				verification = "nonempty structured recap without failed or unreachable hosts"
			}
			p := map[string]any{"playbook": str, "hosts": ids}
			p["tags"] = ids
			add(n, s, effect, verification, p, "playbook")
		}
	}
	return wire.Info{Name: r.Name, Version: r.Version, Protocol: wire.Protocol, Commands: commands}
}

// invocation holds implementation inputs supplied by the public Driver callbacks.
// Protocol decoding and validation belong to driver.Serve.
type invocation struct {
	wire.ExecutionContext
	Command            string
	Arguments          map[string]any
	BlueprintDirectory string
}

type Driver struct {
	release catalog.Release
	options Options
}

var _ wire.Driver = (*Driver)(nil)

func New(release catalog.Release, options Options) *Driver {
	return &Driver{release: release, options: options}
}

func (d *Driver) Definition() (wire.Definition, error) {
	info := Info(d.release)
	return wire.Definition{Name: info.Name, Commands: info.Commands}, nil
}

func (d *Driver) ValidateConfig(ctx context.Context, config wire.Config) error {
	_, _, err := parse(d.release, invocation{ExecutionContext: wire.ExecutionContext{DriverConfig: config}})
	return errors.Join(ctx.Err(), err)
}

func (d *Driver) ValidateBlueprint(ctx context.Context, input wire.BlueprintValidation) error {
	_, _, err := parse(d.release, invocation{ExecutionContext: wire.ExecutionContext{DriverConfig: input.Config}, BlueprintDirectory: input.BlueprintDirectory})
	return errors.Join(ctx.Err(), err)
}

func (d *Driver) ValidateInstance(ctx context.Context, input wire.InstanceValidation) error {
	_, _, err := parse(d.release, invocation{ExecutionContext: wire.ExecutionContext{DriverConfig: input.Config, Binding: input.Binding, Values: input.Values}})
	return errors.Join(ctx.Err(), err)
}

func (d *Driver) Prepare(ctx context.Context, scope wire.ExecutionContext, command string, arguments map[string]any) (wire.PreparedCommand, error) {
	req := invocation{ExecutionContext: scope, Command: command, Arguments: arguments}
	c, a, err := parse(d.release, req)
	if err = errors.Join(ctx.Err(), err); err != nil {
		return nil, err
	}
	return &prepared{driver: d, request: req, config: c, arguments: a}, nil
}

type prepared struct {
	driver    *Driver
	request   invocation
	config    Config
	arguments Arguments
}

func (p *prepared) Execute(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, req, c, a := p.driver.release, p.request, p.config, p.arguments
	env, err := environment(c, req.Values)
	if err != nil {
		return nil, err
	}
	tool := p.driver.options.Tool
	if tool == "" {
		if r.Name == "opentofu" {
			tool = "/opt/inframill/tools/opentofu/" + r.ToolVersion + "/tofu"
		} else {
			tool = "/opt/inframill/tools/ansible/" + r.ToolVersion + "/bin/ansible-playbook"
		}
	}
	if !filepath.IsAbs(tool) {
		return nil, errors.New("tool path must be absolute")
	}
	logs := req.Logs
	if logs == nil {
		logs = io.Discard
	}
	run := runner{tool: tool, dir: req.WorkspaceDirectory, env: env, stderr: logs}
	if r.Name == "ansible" && req.Command == "inventory" {
		return inventory(c, req)
	}
	if err := run.verify(ctx, r); err != nil {
		return nil, err
	}
	if r.Name == "opentofu" {
		return run.tofu(ctx, r, c, a, req)
	}
	return run.ansible(ctx, r, c, a, req)
}
func environment(c Config, values map[string]any) ([]string, error) {
	env := []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "ANSIBLE_NOCOLOR=1"}
	for _, k := range []string{"HOME", "PATH", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "SSH_AUTH_SOCK"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for k, ref := range c.Environment {
		name := ref.Secret
		if ref.SecretFromValue != "" {
			var e error
			name, e = valueAt(values, ref.SecretFromValue)
			if e != nil {
				return nil, e
			}
		}
		v, ok := os.LookupEnv("INFRAMILL_SECRET_" + strings.ToUpper(name))
		if !ok || v == "" {
			return nil, fmt.Errorf("required secret %s unavailable", name)
		}
		env = append(env, k+"="+v)
	}
	return env, nil
}

type runner struct {
	tool, dir string
	env       []string
	stderr    io.Writer
}
type bounded struct {
	bytes.Buffer
	max int
}

func (b *bounded) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("tool output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (r runner) call(ctx context.Context, capture bool, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, r.tool, args...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	cmd.Stdin = nil
	cmd.Stderr = r.stderr
	cmd.WaitDelay = 2 * time.Second
	// Inherit Inframill's process group so its cancellation kills providers and SSH.
	var b bounded
	b.max = maxArtifact
	if capture {
		cmd.Stdout = &b
	} else {
		cmd.Stdout = r.stderr
	}
	e := cmd.Run()
	if e == nil {
		return b.Bytes(), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(e, &ee) {
		return b.Bytes(), ee.ExitCode(), nil
	}
	return nil, -1, e
}
func (r runner) verify(ctx context.Context, release catalog.Release) error {
	if release.Name == "opentofu" {
		b, code, e := r.call(ctx, true, "version", "-json")
		if e != nil {
			return e
		}
		var v struct {
			Version string `json:"terraform_version"`
		}
		if code != 0 || json.Unmarshal(b, &v) != nil || v.Version != release.ToolVersion {
			return errors.New("OpenTofu tool version differs from driver lock")
		}
		return nil
	}
	b, code, e := r.call(ctx, true, "--version")
	if e != nil {
		return e
	}
	if code != 0 || !strings.Contains(string(b), "[core "+release.ToolVersion+"]") {
		return errors.New("Ansible Core version differs from driver lock")
	}
	return nil
}
func toolSuccess(code int, e error) error {
	if e != nil {
		return e
	}
	if code != 0 {
		return fmt.Errorf("tool exited with status %d; see stderr", code)
	}
	return nil
}
