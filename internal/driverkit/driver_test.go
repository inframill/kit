package driverkit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inframill/kit/internal/catalog"
	wire "github.com/sir-hassan/inframill/pkg/driver"
)

func release(t *testing.T, name, version string) catalog.Release {
	t.Helper()
	r, e := catalog.Get(name, "0.1.0")
	r.Version = version
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func request(t *testing.T) (Config, invocation) {
	t.Helper()
	c := Config{RequiredVersion: "0.1.0", ID: "opentofu-0-1-0", Backend: "local", NodeOutput: "inframill_nodes", Artifact: "opentofu-0-1-0-nodes-v1.json"}
	b, _ := json.Marshal(c)
	var raw wire.Config
	json.Unmarshal(b, &raw)
	return c, invocation{ExecutionContext: wire.ExecutionContext{DriverConfig: raw, WorkspaceDirectory: t.TempDir(), ArtifactDirectory: t.TempDir(), Binding: wire.Binding{InstanceID: "11111111-1111-4111-8111-111111111111", Name: "test/example", Blueprint: "example", BlueprintReference: "ghcr.io/test/example:1", Digest: strings.Repeat("a", 64)}, CommandID: "22222222-2222-4222-8222-222222222222", Values: map[string]any{}}}
}
func TestInfoAndValidationHaveNoToolEffects(t *testing.T) {
	for _, name := range []string{"opentofu", "ansible"} {
		for _, version := range []string{"0.1.0", "0.2.0"} {
			r := release(t, name, version)
			var out, logs bytes.Buffer
			code := wire.Serve(t.Context(), New(r, Options{Tool: "/nonexistent"}), wire.ServeOptions{Version: r.Version, Args: []string{"info"}, Stdout: &out, Stderr: &logs})
			if code != 0 {
				t.Fatal(logs.String())
			}
			var info wire.Info
			if err := json.Unmarshal(out.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if err := info.Validate(); err != nil {
				t.Fatal(err)
			}
			if info.Version != version {
				t.Fatal(info)
			}
		}
	}
	_, req := request(t)
	// Validation must neither resolve this unavailable secret nor start a tool.
	req.DriverConfig["environment"] = map[string]any{"HCLOUD_TOKEN": map[string]any{"secret": "missing_validation_test"}}
	blueprint := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blueprint, "template", "opentofu-0-1-0"), 0700); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]map[string]any{
		"config":    {"protocol": 1, "config": req.DriverConfig},
		"blueprint": {"protocol": 1, "config": req.DriverConfig, "blueprintDirectory": blueprint},
		"instance":  {"protocol": 1, "config": req.DriverConfig, "binding": req.Binding, "values": req.Values},
		"command":   {"protocol": 1, "config": req.DriverConfig, "binding": req.Binding, "values": req.Values, "command": "plan", "arguments": map[string]any{}, "workspaceDirectory": req.WorkspaceDirectory, "artifactDirectory": req.ArtifactDirectory},
	}
	serve := func(version, phase string, data []byte) (int, string, string) {
		var out, logs bytes.Buffer
		code := wire.Serve(t.Context(), New(release(t, "opentofu", version), Options{Tool: "/nonexistent"}), wire.ServeOptions{Version: version, Args: []string{"validate", phase}, Stdin: bytes.NewReader(data), Stdout: &out, Stderr: &logs})
		return code, out.String(), logs.String()
	}
	for phase, input := range inputs {
		b, _ := json.Marshal(input)
		if code, out, logs := serve("0.1.0", phase, b); code != 0 || out != "{\"valid\":true}\n" {
			t.Fatalf("%s: %d %s %s", phase, code, out, logs)
		}
		if code, out, _ := serve("0.2.0", phase, b); code == 0 || out != "" {
			t.Fatal("version mismatch accepted", phase)
		}
	}
	// Each callback must consume its own phase-specific inputs.
	inputs["blueprint"]["blueprintDirectory"] = t.TempDir()
	req.DriverConfig["backend_config"] = map[string]any{"region": map[string]any{"from_value": "region"}}
	for _, phase := range []string{"blueprint", "instance", "command"} {
		b, _ := json.Marshal(inputs[phase])
		if code, out, _ := serve("0.1.0", phase, b); code == 0 || out != "" {
			t.Fatal("phase validation omitted", phase)
		}
	}
	// Config validation accepts unresolved value references.
	b, _ := json.Marshal(inputs["config"])
	if code, _, logs := serve("0.1.0", "config", b); code != 0 {
		t.Fatal(logs)
	}
	for _, bad := range [][]byte{append(b, []byte("{}")...), bytes.Repeat([]byte(" "), wire.MaxRequest+1), []byte(`{"protocol":1,"unknown":true}`)} {
		if code, out, _ := serve("0.1.0", "config", bad); code == 0 || out != "" {
			t.Fatal("accepted invalid request")
		}
	}
}
func TestEnvironmentDoesNotForwardUnrelatedSecrets(t *testing.T) {
	t.Setenv("INFRAMILL_SECRET_CLOUD", "private-cloud")
	t.Setenv("INFRAMILL_SECRET_OTHER", "unrelated")
	t.Setenv("TF_CLI_ARGS", "-destroy")
	c := Config{Environment: map[string]SecretRef{"HCLOUD_TOKEN": {Secret: "cloud"}}}
	env, e := environment(c, nil)
	if e != nil {
		t.Fatal(e)
	}
	s := strings.Join(env, "\n")
	if !strings.Contains(s, "HCLOUD_TOKEN=private-cloud") || strings.Contains(s, "unrelated") || strings.Contains(s, "TF_CLI_ARGS") || strings.Contains(s, "INFRAMILL_SECRET") {
		t.Fatal("environment boundary failed")
	}
}
func TestPlanBindingAndFailureInvalidation(t *testing.T) {
	c, req := request(t)
	tool := filepath.Join(t.TempDir(), "tofu")
	script := `#!/bin/sh
case "$1" in
plan) for a in "$@"; do case "$a" in -out=*) printf 'saved private plan' > "${a#-out=}";; esac; done; exit 2;;
apply) exit 1;;
esac
`
	if e := os.WriteFile(tool, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	run := runner{tool: tool, dir: req.WorkspaceDirectory, env: []string{"PATH=/usr/bin:/bin"}, stderr: &bytes.Buffer{}}
	r := release(t, "opentofu", "0.1.0")
	req.Command = "plan"
	result, e := run.tofu(context.Background(), r, c, Arguments{}, req)
	if e != nil {
		t.Fatal(e)
	}
	if !result.(map[string]any)["changes"].(bool) {
		t.Fatal(result)
	}
	req.Command = "apply"
	a := Arguments{PlanID: req.CommandID}
	req.CommandID = "33333333-3333-4333-8333-333333333333"
	writePrivate(req.ArtifactDirectory, c.Artifact, map[string]bool{"valid": true})
	bad := req
	binding := req.Binding
	binding.InstanceID = "44444444-4444-4444-8444-444444444444"
	bad.Binding = binding
	if _, e = run.tofu(context.Background(), r, c, a, bad); e == nil {
		t.Fatal("cross-instance plan accepted")
	}
	b, _ := readPrivate(req.ArtifactDirectory, c.Artifact, maxArtifact)
	if !bytes.Contains(b, []byte("true")) {
		t.Fatal("invalid plan caused effects")
	}
	if _, e = run.tofu(context.Background(), release(t, "opentofu", "0.2.0"), c, a, req); e == nil {
		t.Fatal("cross-version plan accepted")
	}
	if _, e = run.tofu(context.Background(), r, c, a, req); e == nil {
		t.Fatal("tool failure reported success")
	}
	b, _ = readPrivate(req.ArtifactDirectory, c.Artifact, maxArtifact)
	if bytes.Contains(b, []byte("true")) {
		t.Fatal("failed apply retained valid inventory")
	}
}
func TestInventoryRejectsWrongBindingSchemaAndAddress(t *testing.T) {
	_, req := request(t)
	c := Config{Artifact: "opentofu-0-1-0-nodes-v1.json", ProducerID: "opentofu-0-1-0", ProducerVersion: "0.1.0", AddressMode: ValueRef{Value: "private_ipv4"}, SSHUser: ValueRef{Value: "deploy"}, Connection: "ssh", Roles: []string{"web"}}
	ns := Nodes{Schema: 1, Valid: true, Binding: req.Binding, ProducerID: c.ProducerID, ProducerName: "opentofu", ProducerVersion: c.ProducerVersion, CommandID: req.CommandID, Nodes: []Node{{Name: "web", Roles: []string{"web"}, Architecture: "arm64", PublicIPv4: "192.0.2.1"}}}
	writePrivate(req.ArtifactDirectory, c.Artifact, ns)
	if _, e := inventory(c, req); e == nil {
		t.Fatal("private address silently fell back")
	}
	c.AddressMode.Value = "public_ipv4"
	if _, e := inventory(c, req); e != nil {
		t.Fatal(e)
	}
	ns.Schema = 2
	writePrivate(req.ArtifactDirectory, c.Artifact, ns)
	if _, e := inventory(c, req); e == nil {
		t.Fatal("unsupported schema accepted")
	}
	ns.Schema = 1
	ns.Binding.Digest = "different"
	writePrivate(req.ArtifactDirectory, c.Artifact, ns)
	if _, e := inventory(c, req); e == nil {
		t.Fatal("wrong blueprint digest accepted")
	}
}
func TestArtifactsRefuseLinksAndTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(dir, "link"))
	if _, e := readPrivate(dir, "link", 100); e == nil {
		t.Fatal("link accepted")
	}
	if e := writeBytes(dir, "link", []byte("new")); e == nil {
		t.Fatal("link overwritten")
	}
	if e := writeBytes(dir, "../escape", nil); e == nil {
		t.Fatal("traversal accepted")
	}
}

func TestAnsibleRequiresCompleteSuccessfulRecap(t *testing.T) {
	_, req := request(t)
	req.Command = "apply"
	c := Config{Artifact: "opentofu-0-1-0-nodes-v1.json", ProducerID: "opentofu-0-1-0", ProducerVersion: "0.1.0", AddressMode: ValueRef{Value: "public_ipv4"}, SSHUser: ValueRef{Value: "deploy"}, Connection: "ssh", HostKeyPolicy: "strict", Roles: []string{"web"}, Playbooks: map[string]string{"site": "site.yml"}}
	ns := Nodes{Schema: 1, Valid: true, Binding: req.Binding, ProducerID: c.ProducerID, ProducerName: "opentofu", ProducerVersion: c.ProducerVersion, CommandID: req.CommandID, Nodes: []Node{{Name: "web", Roles: []string{"web"}, Architecture: "arm64", PublicIPv4: "192.0.2.1"}}}
	if e := writePrivate(req.ArtifactDirectory, c.Artifact, ns); e != nil {
		t.Fatal(e)
	}
	if e := writeBytes(req.WorkspaceDirectory, "site.yml", []byte("[]")); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name, output string
		success      bool
	}{
		{"verified", `{"schema":1,"hosts":{"web":{"ok":1,"changed":1}}}`, true},
		{"failed", `{"schema":1,"hosts":{"web":{"ok":1,"failures":1}}}`, false},
		{"unreachable", `{"schema":1,"hosts":{"web":{"ok":1,"unreachable":1}}}`, false},
		{"ignored", `{"schema":1,"hosts":{"web":{"ok":1,"ignored":1}}}`, false},
		{"rescued", `{"schema":1,"hosts":{"web":{"ok":1,"rescued":1}}}`, false},
		{"skipped", `{"schema":1,"hosts":{"web":{"ok":0,"skipped":1}}}`, false},
		{"empty", `{"schema":1,"hosts":{}}`, false},
		{"wrong host", `{"schema":1,"hosts":{"other":{"ok":1}}}`, false},
		{"missing recap", `tool exited zero without verification`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := filepath.Join(t.TempDir(), "ansible-playbook")
			if e := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s' '"+tc.output+"'\n"), 0700); e != nil {
				t.Fatal(e)
			}
			r := runner{tool: tool, dir: req.WorkspaceDirectory, env: []string{"PATH=/usr/bin:/bin"}, stderr: &bytes.Buffer{}}
			_, e := r.ansible(t.Context(), release(t, "ansible", "0.1.0"), c, Arguments{Playbook: "site"}, req)
			if (e == nil) != tc.success {
				t.Fatalf("success=%v, error=%v", tc.success, e)
			}
		})
	}
}

func TestInitialReleaseIncludesImplementedOptions(t *testing.T) {
	for _, tc := range []struct{ name, command, option string }{{"opentofu", "plan", "refresh_only"}, {"ansible", "apply", "tags"}} {
		info := Info(release(t, tc.name, "0.1.0"))
		properties := info.Commands[tc.command].ArgumentsSchema["properties"].(map[string]any)
		if _, ok := properties[tc.option]; !ok {
			t.Fatalf("%s missing %s", tc.name, tc.option)
		}
	}
}
