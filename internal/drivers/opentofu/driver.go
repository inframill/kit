package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inframill/kit/internal/artifacts"
	"github.com/inframill/kit/internal/toolrun"
	driver "github.com/sir-hassan/inframill/pkg/driver"
)

type Driver struct{ Version, ToolVersion, Tool string }
type Manifest struct {
	Version      string `json:"required_driver_version"`
	ID           string `json:"id"`
	SettingsFile string `json:"settings_file"`
	Backend      string `json:"backend"`
	NodeOutput   string `json:"node_output"`
	Artifact     string `json:"artifact"`
}
type Settings struct {
	Schema      int               `json:"schema"`
	Backend     map[string]any    `json:"backend_config"`
	Environment map[string]string `json:"environment"`
}
type Arguments struct {
	PlanID      string `json:"plan_id,omitempty"`
	Destroy     bool   `json:"destroy,omitempty"`
	RefreshOnly bool   `json:"refresh_only,omitempty"`
}
type prepared struct {
	driver    Driver
	scope     driver.ExecutionContext
	manifest  Manifest
	settings  Settings
	arguments Arguments
	command   string
}

var _ driver.Driver = Driver{}

func (d Driver) Definition() (driver.Definition, error) {
	object := func(properties map[string]any, required ...string) map[string]any {
		if properties == nil {
			properties = map[string]any{}
		}
		s := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	commands := map[string]driver.CommandDefinition{}
	for _, name := range []string{"init", "validate", "output"} {
		commands[name] = driver.CommandDefinition{Description: name + " the OpenTofu workspace", ArgumentsSchema: object(nil)}
	}
	commands["plan"] = driver.CommandDefinition{Description: "Save an integrity-bound plan", ArgumentsSchema: object(map[string]any{"destroy": map[string]any{"type": "boolean"}, "refresh_only": map[string]any{"type": "boolean"}})}
	for _, name := range []string{"apply", "destroy"} {
		commands[name] = driver.CommandDefinition{Description: name + " using a saved plan and verify the result", ArgumentsSchema: object(map[string]any{"plan_id": map[string]any{"type": "string", "format": "uuid"}}, "plan_id")}
	}
	return driver.Definition{Name: "opentofu", Commands: commands}, nil
}
func (d Driver) manifest(config driver.Config) (Manifest, error) {
	var m Manifest
	b, err := json.Marshal(config)
	if err != nil {
		return m, errors.New("invalid manifest")
	}
	if err = artifacts.Decode(b, &m); err != nil {
		return m, err
	}
	if m.Version != d.Version || !driver.ValidID(m.ID) || !artifacts.Flat(m.SettingsFile) || !artifacts.Flat(m.Artifact) || !strings.HasPrefix(m.Artifact, m.ID+"-nodes-") || !artifacts.Identifier.MatchString(m.NodeOutput) || m.Backend != "local" && m.Backend != "s3" {
		return m, errors.New("invalid OpenTofu manifest or implementation version")
	}
	return m, nil
}
func (d Driver) ValidateManifest(ctx context.Context, config driver.Config) error {
	_, err := d.manifest(config)
	return errors.Join(ctx.Err(), err)
}

var secretName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

func readSettings(scope driver.ExecutionContext, m Manifest) (Settings, error) {
	var s Settings
	b, err := artifacts.Read(scope.WorkspaceDirectory, m.SettingsFile, artifacts.SettingsLimit)
	if err != nil {
		return s, err
	}
	if err = artifacts.Decode(b, &s); err != nil {
		return s, err
	}
	if s.Schema != 1 {
		return s, errors.New("unsupported settings schema")
	}
	for key, value := range s.Backend {
		switch key {
		case "bucket", "region":
			v, ok := value.(string)
			if !ok || v == "" {
				return s, errors.New("backend string setting required")
			}
		case "use_path_style", "skip_credentials_validation", "skip_metadata_api_check", "skip_region_validation", "skip_requesting_account_id", "skip_s3_checksum", "use_lockfile":
			if _, ok := value.(bool); !ok {
				return s, errors.New("backend boolean setting required")
			}
		default:
			return s, errors.New("unsupported backend setting")
		}
	}
	if m.Backend == "local" && len(s.Backend) > 0 {
		return s, errors.New("local backend does not accept S3 settings")
	}
	for key, ref := range s.Environment {
		switch key {
		case "HCLOUD_TOKEN", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "TF_ENCRYPTION":
		default:
			return s, errors.New("unsupported credential environment")
		}
		if !secretName.MatchString(ref) {
			return s, errors.New("invalid secret name")
		}
	}
	return s, nil
}
func (d Driver) Prepare(ctx context.Context, scope driver.ExecutionContext, command string, args map[string]any) (driver.PreparedCommand, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, err := d.manifest(scope.DriverConfig)
	if err != nil {
		return nil, err
	}
	if filepath.Base(scope.WorkspaceDirectory) != m.ID {
		return nil, errors.New("manifest id must match workspace driver id")
	}
	var a Arguments
	data, err := json.Marshal(args)
	if err != nil {
		return nil, errors.New("invalid arguments")
	}
	if string(data) == "null" {
		data = []byte("{}")
	}
	if err = artifacts.Decode(data, &a); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	switch command {
	case "init", "validate", "output":
	case "plan":
		allowed["destroy"] = true
		allowed["refresh_only"] = true
		if a.Destroy && a.RefreshOnly {
			return nil, errors.New("incompatible plan modes")
		}
	case "apply", "destroy":
		allowed["plan_id"] = true
		if !driver.ValidCommandID(a.PlanID) {
			return nil, errors.New("saved plan_id required")
		}
	default:
		return nil, errors.New("unknown OpenTofu command")
	}
	for key := range args {
		if !allowed[key] {
			return nil, errors.New("argument is not allowed for command")
		}
	}
	s, err := readSettings(scope, m)
	if err != nil {
		return nil, err
	}
	p := &prepared{d, scope, m, s, a, command}
	if command == "apply" || command == "destroy" {
		if _, _, err = p.savedPlan(); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func (p *prepared) credentials() map[string]string {
	out := map[string]string{}
	for key, ref := range p.settings.Environment {
		needed := false
		switch key {
		case "HCLOUD_TOKEN":
			needed = p.command == "plan" || p.command == "apply" || p.command == "destroy"
		case "TF_ENCRYPTION":
			needed = p.command != "validate"
		default:
			needed = p.manifest.Backend == "s3" && p.command != "validate"
		}
		if needed {
			out[key] = ref
		}
	}
	return out
}
func (p *prepared) Execute(ctx context.Context, secrets driver.Secrets) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env, err := toolrun.Credentials(p.credentials(), secrets)
	if err != nil {
		return nil, err
	}
	tool := p.driver.Tool
	if tool == "" {
		tool = "/opt/inframill/tools/opentofu/" + p.driver.ToolVersion + "/tofu"
	}
	run := toolrun.Runner{Tool: tool, Directory: p.scope.WorkspaceDirectory, Environment: env, Logs: p.scope.Logs, Secrets: secrets}
	if err = run.Verify(ctx, "opentofu", p.driver.ToolVersion); err != nil {
		return nil, err
	}
	if err = p.scope.Message("OpenTofu " + p.command); err != nil {
		return nil, err
	}
	result, err := p.execute(ctx, run)
	if err != nil {
		return nil, err
	}
	if err = toolrun.Public(result, secrets); err != nil {
		return nil, err
	}
	return result, nil
}
