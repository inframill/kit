package driverkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inframill/kit/internal/catalog"
	wire "github.com/sir-hassan/inframill/pkg/driver"
)

type ValueRef struct {
	Value     string `json:"value,omitempty"`
	FromValue string `json:"from_value,omitempty"`
}
type SecretRef struct {
	Secret          string `json:"secret,omitempty"`
	SecretFromValue string `json:"secret_from_value,omitempty"`
}
type Config struct {
	RequiredVersion string               `json:"required_driver_version"`
	ID              string               `json:"id"`
	Environment     map[string]SecretRef `json:"environment,omitempty"`
	Backend         string               `json:"backend,omitempty"`
	BackendConfig   map[string]ValueRef  `json:"backend_config,omitempty"`
	NodeOutput      string               `json:"node_output,omitempty"`
	Artifact        string               `json:"artifact"`
	ProducerID      string               `json:"producer_id,omitempty"`
	ProducerVersion string               `json:"producer_version,omitempty"`
	AddressMode     ValueRef             `json:"address_mode,omitempty"`
	SSHUser         ValueRef             `json:"ssh_user,omitempty"`
	Connection      string               `json:"connection,omitempty"`
	HostKeyPolicy   string               `json:"host_key_policy,omitempty"`
	Playbooks       map[string]string    `json:"playbooks,omitempty"`
	Roles           []string             `json:"roles,omitempty"`
}
type Arguments struct {
	PlanID      string   `json:"plan_id,omitempty"`
	Destroy     bool     `json:"destroy,omitempty"`
	RefreshOnly bool     `json:"refresh_only,omitempty"`
	Playbook    string   `json:"playbook,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Hosts       []string `json:"hosts,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var secretName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func valueAt(values map[string]any, path string) (string, error) {
	var v any = values
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("missing value %s", path)
		}
		v, ok = m[part]
		if !ok {
			return "", fmt.Errorf("missing value %s", path)
		}
	}
	switch v := v.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return string(v), nil
	}
	return "", fmt.Errorf("value %s must be scalar", path)
}
func (r ValueRef) resolve(values map[string]any) (string, error) {
	if r.FromValue != "" {
		return valueAt(values, r.FromValue)
	}
	return r.Value, nil
}
func validateRef(r ValueRef) error {
	if r.Value != "" && r.FromValue != "" {
		return errors.New("value and from_value are mutually exclusive")
	}
	return nil
}
func parse(r catalog.Release, req wire.Request) (Config, Arguments, error) {
	var c Config
	var a Arguments
	b, _ := json.Marshal(req.Config)
	if e := decode(b, &c); e != nil {
		return c, a, e
	}
	b, _ = json.Marshal(req.Arguments)
	if string(b) != "null" {
		if e := decode(b, &a); e != nil {
			return c, a, e
		}
	}
	if req.Protocol != wire.Protocol {
		return c, a, errors.New("unsupported protocol")
	}
	if c.RequiredVersion != r.Version {
		return c, a, errors.New("required_driver_version does not match selected implementation")
	}
	if !wire.ValidID(c.ID) || !safeRelative(c.Artifact) || filepath.Base(c.Artifact) != c.Artifact || !strings.HasPrefix(c.Artifact, c.ID+"-nodes-v1") && r.Name == "opentofu" {
		return c, a, errors.New("invalid driver id or namespaced artifact")
	}
	for k, s := range c.Environment {
		if !allowedSecretEnv(k) {
			return c, a, fmt.Errorf("environment variable %s is not an allowed credential", k)
		}
		if (s.Secret == "") == (s.SecretFromValue == "") {
			return c, a, errors.New("select exactly one secret reference source")
		}
		if s.Secret != "" && !secretName.MatchString(s.Secret) {
			return c, a, errors.New("invalid secret reference")
		}
	}
	for k, v := range c.BackendConfig {
		if !backendKey(k) {
			return c, a, fmt.Errorf("unsupported backend setting %s", k)
		}
		if e := validateRef(v); e != nil {
			return c, a, e
		}
	}
	if r.Name == "opentofu" {
		if c.Backend != "local" && c.Backend != "s3" {
			return c, a, errors.New("backend must be local or s3")
		}
		if !identifier.MatchString(c.NodeOutput) {
			return c, a, errors.New("invalid node output")
		}
		if len(c.Playbooks) > 0 || c.ProducerID != "" || len(a.Tags) > 0 || len(a.Hosts) > 0 || a.Playbook != "" {
			return c, a, errors.New("Ansible fields on OpenTofu request")
		}
	} else {
		if c.Backend != "" || len(c.BackendConfig) > 0 || len(c.Environment) > 0 || a.PlanID != "" || a.Destroy || a.RefreshOnly {
			return c, a, errors.New("OpenTofu fields on Ansible request")
		}
		if !wire.ValidID(c.ProducerID) || !catalog.ValidVersion(c.ProducerVersion) || !strings.HasPrefix(c.Artifact, c.ProducerID+"-nodes-v1") {
			return c, a, errors.New("invalid producer identity")
		}
		if c.Connection != "ssh" && c.Connection != "local" {
			return c, a, errors.New("connection must be ssh or local")
		}
		if c.HostKeyPolicy != "strict" && c.HostKeyPolicy != "accept-new" {
			return c, a, errors.New("explicit host key policy required")
		}
		if len(c.Playbooks) == 0 {
			return c, a, errors.New("playbooks are required")
		}
		for k, p := range c.Playbooks {
			if !identifier.MatchString(k) || !safeRelative(p) {
				return c, a, errors.New("invalid playbook")
			}
		}
		for _, role := range c.Roles {
			if !identifier.MatchString(role) || role == "all" || role == "ungrouped" {
				return c, a, errors.New("invalid role")
			}
		}
		if e := validateRef(c.AddressMode); e != nil {
			return c, a, e
		}
		if e := validateRef(c.SSHUser); e != nil {
			return c, a, e
		}
	}
	if req.Command != "" {
		if _, ok := Info(r).Commands[req.Command]; !ok {
			return c, a, errors.New("unknown command")
		}
		if (req.Command == "apply" || req.Command == "destroy") && r.Name == "opentofu" && !uuid.MatchString(a.PlanID) {
			return c, a, errors.New("saved plan_id required")
		}
		if a.PlanID != "" && req.Command != "apply" && req.Command != "destroy" {
			return c, a, errors.New("plan_id only accepted for apply/destroy")
		}
		if (a.Destroy || a.RefreshOnly) && req.Command != "plan" {
			return c, a, errors.New("plan mode only accepted for plan")
		}
		if a.Destroy && a.RefreshOnly {
			return c, a, errors.New("incompatible plan modes")
		}
		if r.Version == "0.1.0" && (a.RefreshOnly || len(a.Tags) > 0) {
			return c, a, errors.New("option requires driver 0.2.0")
		}
		if r.Name == "ansible" && req.Command != "inventory" {
			if _, ok := c.Playbooks[a.Playbook]; !ok {
				return c, a, errors.New("select a declared playbook")
			}
		}
		for _, s := range append(append([]string{}, a.Tags...), a.Hosts...) {
			if !identifier.MatchString(s) {
				return c, a, errors.New("tags and hosts must be literal identifiers")
			}
		}
	}
	if req.Binding != nil && !uuid.MatchString(req.Binding.InstanceID) {
		return c, a, errors.New("invalid Instance binding")
	}
	if req.Values != nil {
		for _, v := range c.BackendConfig {
			if _, e := v.resolve(req.Values); e != nil {
				return c, a, e
			}
		}
		for _, s := range c.Environment {
			if s.SecretFromValue != "" {
				v, e := valueAt(req.Values, s.SecretFromValue)
				if e != nil || !secretName.MatchString(v) {
					return c, a, errors.New("invalid secret value reference")
				}
			}
		}
		if r.Name == "ansible" {
			mode, e := c.AddressMode.resolve(req.Values)
			if e != nil {
				return c, a, e
			}
			if mode != "public_ipv4" && mode != "private_ipv4" && mode != "fqdn" {
				return c, a, errors.New("invalid address mode")
			}
			user, e := c.SSHUser.resolve(req.Values)
			if e != nil || !identifier.MatchString(user) {
				return c, a, errors.New("invalid SSH user")
			}
		}
	}
	if req.BlueprintDirectory != "" {
		dir := filepath.Join(req.BlueprintDirectory, "template", c.ID)
		if _, e := os.Stat(dir); e != nil {
			return c, a, e
		}
		if r.Name == "ansible" {
			for _, p := range c.Playbooks {
				if _, e := readPrivate(dir, p, maxArtifact); e != nil {
					return c, a, e
				}
			}
		}
	}
	return c, a, nil
}
func backendKey(s string) bool {
	switch s {
	case "bucket", "region", "use_path_style", "skip_credentials_validation", "skip_metadata_api_check", "skip_region_validation", "skip_requesting_account_id", "skip_s3_checksum", "use_lockfile":
		return true
	}
	return false
}
func allowedSecretEnv(s string) bool {
	return s == "HCLOUD_TOKEN" || s == "AWS_ACCESS_KEY_ID" || s == "AWS_SECRET_ACCESS_KEY" || s == "AWS_SESSION_TOKEN" || s == "TF_ENCRYPTION"
}
