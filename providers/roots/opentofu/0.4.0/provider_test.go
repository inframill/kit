package opentofu

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/inframill/kit/providers/roots/internal/toolrun"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

func fixture(t *testing.T) (Provider, runmill.ExecutionContext) {
	t.Helper()
	shared := t.TempDir()
	work := filepath.Join(shared, "opentofu")
	os.Mkdir(work, 0700)
	artifacts.Write(work, "kit-provider.json", Settings{Schema: 1, Backend: map[string]any{}, Environment: map[string]string{}})
	return Provider{Version: "0.4.0", ToolVersion: "1.11.5", Tool: "/missing"}, runmill.ExecutionContext{WorkspaceDir: work, ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Settings: runmill.Settings{"id": "tofu", "settings_file": "kit-provider.json", "backend": "local", "node_output": "nodes", "artifact": "tofu-nodes-v1.json"}}
}

func TestStandaloneOutputIsUnavailable(t *testing.T) {
	d, scope := fixture(t)
	definition, err := d.Definition()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := definition.Commands["output"]; ok {
		t.Fatal("standalone output is still advertised")
	}
	if err := artifacts.Write(scope.ArtifactDir, "tofu-nodes-v1.json", map[string]bool{"valid": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Execute(t.Context(), scope, "output", nil, nil); err == nil || err.Error() != "unknown OpenTofu command" {
		t.Fatal("standalone output did not reject before tool execution", err)
	}
	data, err := artifacts.Read(scope.ArtifactDir, "tofu-nodes-v1.json", artifacts.Limit)
	if err != nil || !bytes.Contains(data, []byte(`"valid":true`)) {
		t.Fatal("rejected command invalidated the node artifact", err)
	}
}

func TestPrerequisitesAndSecretsBeforeEffects(t *testing.T) {
	d, s := fixture(t)
	artifacts.Write(s.WorkspaceDir, "kit-provider.json", Settings{Schema: 1, Environment: map[string]string{"HCLOUD_TOKEN": "cloud-token"}})
	if e := d.ValidateSettings(t.Context(), s.Settings); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"plan", "apply", "destroy"} {
		_, e := d.Execute(t.Context(), s, name, nil, map[string]string{})
		var detail *runmill.ProviderError
		if !errors.As(e, &detail) || detail.Code != "MISSING_SECRET" {
			t.Fatal(name, e)
		}
	}
	entries, _ := os.ReadDir(s.ArtifactDir)
	if len(entries) != 0 {
		t.Fatal("effects before secrets")
	}
	entries, _ = os.ReadDir(s.WorkspaceDir)
	if len(entries) != 1 {
		t.Fatal("input validation wrote files")
	}
	for _, args := range []map[string]any{{"destroy": true, "refresh_only": true}, {"destroy": nil}, {"refresh_only": nil}, {"destroy": "true"}, {"plan_id": "x"}, {"unknown": false}} {
		if _, e := d.Execute(t.Context(), s, "plan", args, nil); e == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	for _, method := range []string{"info", "validate_settings", "validate_command"} {
		params := map[string]any{}
		if method != "info" {
			params["settings"] = s.Settings
		}
		if method == "validate_command" {
			params["workspaceDir"] = s.WorkspaceDir
			params["artifactDir"] = s.ArtifactDir
			params["command"] = "plan"
		}
		data, _ := json.Marshal(map[string]any{"protocol": 1, "method": method, "params": params})
		var out bytes.Buffer
		code := runmill.Serve(t.Context(), d, runmill.ServeOptions{Version: d.Version, Stdin: bytes.NewReader(data), Stdout: &out})
		if (code == 0) != (method != "validate_command") {
			t.Fatal(out.String())
		}
	}
}

func TestOptionalPlanIDRejectsInvalidSuppliedValues(t *testing.T) {
	for _, name := range []string{"apply", "destroy"} {
		t.Run(name, func(t *testing.T) {
			d, scope := fixture(t)
			for _, id := range []any{"", nil, 42, "bad", "../../outside", "00000000000000000000000000"} {
				if _, err := d.Execute(t.Context(), scope, name, runmill.Arguments{"plan_id": id}, nil); err == nil {
					t.Fatalf("accepted invalid plan_id %v", id)
				}
			}
			if _, err := d.Execute(t.Context(), scope, name, runmill.Arguments{"plan_id": scope.ExecutionID}, nil); err == nil {
				t.Fatal("missing saved plan fell back to direct execution")
			}
		})
	}
}

func TestApplyAndDestroyWithoutSavedPlan(t *testing.T) {
	for _, tc := range []struct {
		name, command, outcome string
		wantError              bool
	}{
		{"apply", "apply", "success", false},
		{"failed-apply", "apply", "tool-failure", true},
		{"invalid-output", "apply", "invalid-output", true},
		{"empty-apply-output", "apply", "empty-output", true},
		{"failed-apply-output", "apply", "output-failure", true},
		{"destroy", "destroy", "success", false},
		{"empty-destroy-output", "destroy", "empty-output", false},
		{"failed-destroy", "destroy", "tool-failure", true},
		{"remaining-resources", "destroy", "remaining", true},
		{"invalid-destroy-output", "destroy", "invalid-output", true},
		{"failed-destroy-output", "destroy", "output-failure", true},
		{"remaining-node-output", "destroy", "remaining-output", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, scope := fixture(t)
			write := func(directory, name, data string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, name), []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			write(scope.WorkspaceDir, "outcome", tc.outcome, 0600)
			write(scope.WorkspaceDir, "command", tc.command, 0600)
			write(scope.WorkspaceDir, "inventory-path", filepath.Join(scope.ArtifactDir, "tofu-nodes-v1.json"), 0600)
			write(scope.ArtifactDir, "tofu-nodes-v1.json", `{"valid":true}`, 0600)
			// Omission must use the current configuration, without selecting or
			// consuming any previously saved plan.
			plan := "tofu-plan-" + scope.ExecutionID + ".json"
			write(scope.ArtifactDir, plan, "unrelated saved plan", 0600)
			d.Tool = filepath.Join(t.TempDir(), "tofu")
			script := `#!/bin/sh
set -eu
if [ "$1" = version ]; then printf '{"terraform_version":"1.11.5"}'; exit 0; fi
printf '%s\n' "$@" >> calls.txt
case "$1" in
apply|destroy)
    grep -q '"valid":false' "$(cat inventory-path)"
    if [ "$(cat outcome)" = tool-failure ]; then exit 1; fi;;
output)
    if [ "$(cat outcome)" = output-failure ]; then exit 1; fi
    nodes='{"local":{"name":"local","roles":["local"],"architecture":"amd64","public_ipv4":"127.0.0.1","private_ipv4":""}}'
    if [ "$(cat outcome)" = invalid-output ]; then printf '[]';
    elif [ "$(cat outcome)" = empty-output ]; then
        if [ "$(cat command)" = destroy ]; then printf '{"nodes":{"sensitive":false,"type":["map","string"],"value":{}}}';
        else printf '{}'; fi
    elif [ "$(cat command)" = destroy ]; then
        if [ "$(cat outcome)" = remaining-output ]; then printf '{"nodes":{"value":%s}}' "$nodes";
        else printf '{}'; fi
    else printf '%s' "$nodes"
    fi;;
show)
    if [ "$(cat outcome)" = remaining ]; then
        printf '{"values":{"root_module":{"child_modules":[{"resources":[{"mode":"managed"}]}]}}}'
    else printf '{"values":{"root_module":{}}}'; fi;;
*) exit 2;;
esac
`
			write(filepath.Dir(d.Tool), "tofu", script, 0700)
			p, err := d.loadExecution(t.Context(), scope, tc.command, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(scope.WorkspaceDir, "calls.txt")); !os.IsNotExist(err) {
				t.Fatal("input validation invoked the tool", err)
			}
			result, err := p.run(t.Context(), nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("result %v, error %v", result, err)
			}
			calls, err := os.ReadFile(filepath.Join(scope.WorkspaceDir, "calls.txt"))
			if err != nil {
				t.Fatal(err)
			}
			args := tc.command + "\n-input=false\n-no-color\n-lock-timeout=60s\n-auto-approve\n"
			if !strings.HasPrefix(string(calls), args) {
				t.Fatalf("expected noninteractive direct execution: %s", calls)
			}
			var nodes artifacts.Nodes
			data, err := artifacts.Read(scope.ArtifactDir, "tofu-nodes-v1.json", artifacts.Limit)
			if err != nil || artifacts.Decode(data, &nodes) != nil {
				t.Fatal("missing node inventory", err)
			}
			if nodes.Valid != (tc.command == "apply" && !tc.wantError) {
				t.Fatalf("unexpected inventory validity after %s: %s", tc.name, data)
			}
			if tc.command == "apply" && !tc.wantError && result != (ApplyResult{NodeOutputResult{"tofu-nodes-v1.json", 1, true}}) {
				t.Fatal("apply did not publish verified nodes", result)
			}
			if tc.command == "destroy" && !tc.wantError {
				if result != (DestroyResult{NodeOutputResult{"tofu-nodes-v1.json", 0, false}, true, true}) || len(nodes.Nodes) != 0 {
					t.Fatal("destroy did not publish empty outputs", result, nodes)
				}
				if string(calls) != args+"show\n-json\noutput\n-json\n" {
					t.Fatalf("destroy did not collect outputs after verifying state: %s", calls)
				}
			}
			data, err = artifacts.Read(scope.ArtifactDir, plan, artifacts.SettingsLimit)
			if err != nil || string(data) != "unrelated saved plan" {
				t.Fatal("direct execution consumed a saved plan", err)
			}
		})
	}
}

func TestCloudflareSecretScopeAndAdmission(t *testing.T) {
	d, scope := fixture(t)
	settings := Settings{Schema: 1, Environment: map[string]string{"CLOUDFLARE_API_TOKEN": "cloudflare"}}
	if err := artifacts.Write(scope.WorkspaceDir, "kit-provider.json", settings); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Execute(t.Context(), scope, "plan", nil, nil); err == nil {
		t.Fatal("missing Cloudflare secret was accepted")
	} else {
		var detail *runmill.ProviderError
		if !errors.As(err, &detail) || detail.Code != "MISSING_SECRET" {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(scope.ArtifactDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("effects before credentials", entries, err)
	}
	command, err := d.loadExecution(t.Context(), scope, "plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"init", "validate", "plan", "apply", "destroy"} {
		command.command = name
		credentials := command.credentials()
		needed := name == "plan" || name == "apply" || name == "destroy"
		if (credentials["CLOUDFLARE_API_TOKEN"] == "cloudflare") != needed {
			t.Fatalf("Cloudflare credential scope for %s: %v", name, credentials)
		}
		env, err := toolrun.Credentials(credentials, map[string]string{"cloudflare": "synthetic-cloudflare-token"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range env {
			found = found || entry == "CLOUDFLARE_API_TOKEN=synthetic-cloudflare-token"
		}
		if found != needed {
			t.Fatalf("Cloudflare token not scoped to %s", name)
		}
	}
}

func TestPlanIsolationConsumptionAndFailedApplyInvalidatesNodes(t *testing.T) {
	d, s := fixture(t)
	tool := filepath.Join(t.TempDir(), "tofu")
	os.WriteFile(tool, []byte(`#!/bin/sh
case "$1" in
version) printf '{"terraform_version":"1.11.5"}';;
plan) for a in "$@"; do case "$a" in -out=*) printf 'saved plan' > "${a#-out=}";; esac; done; exit 2;;
apply) exit 1;;
esac
`), 0700)
	d.Tool = tool
	v, e := d.Execute(t.Context(), s, "plan", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := v.(PlanResult).PlanID
	s.ExecutionID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	args := map[string]any{"plan_id": id}
	bad := s
	bad.ArtifactDir = t.TempDir()
	for _, suffix := range []string{".bin", ".json"} {
		name := "tofu-plan-" + id + suffix
		data, err := artifacts.Read(s.ArtifactDir, name, artifacts.Limit)
		if err != nil {
			t.Fatal(err)
		}
		if err := artifacts.WriteBytes(bad.ArtifactDir, name, data); err != nil {
			t.Fatal(err)
		}
	}
	if _, e = d.Execute(t.Context(), bad, "apply", args, nil); e == nil {
		t.Fatal("plan copied to another artifact directory accepted")
	}
	if _, e = d.Execute(t.Context(), s, "destroy", args, nil); e == nil {
		t.Fatal("wrong mode accepted")
	}
	artifacts.Write(s.WorkspaceDir, "changed.tf", map[string]any{})
	if _, e = d.Execute(t.Context(), s, "apply", args, nil); e == nil {
		t.Fatal("changed source accepted")
	}
	artifacts.Remove(s.WorkspaceDir, "changed.tf")
	planName := "tofu-plan-" + id + ".bin"
	original, err := artifacts.Read(s.ArtifactDir, planName, artifacts.Limit)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.WriteBytes(s.ArtifactDir, planName, []byte("tampered"))
	if _, e = d.Execute(t.Context(), s, "apply", args, nil); e == nil {
		t.Fatal("corrupt plan accepted")
	}
	artifacts.WriteBytes(s.ArtifactDir, planName, original)
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", map[string]bool{"valid": true})
	if _, e = d.Execute(t.Context(), s, "apply", args, nil); e == nil {
		t.Fatal("failed tool accepted")
	}
	data, _ := artifacts.Read(s.ArtifactDir, "tofu-nodes-v1.json", artifacts.Limit)
	if bytes.Contains(data, []byte(`"valid":true`)) {
		t.Fatal("inventory remains valid")
	}
	if _, e = d.Execute(t.Context(), s, "apply", args, nil); e == nil {
		t.Fatal("partially applied plan replayed")
	}
}

func TestUsesEngineSuppliedProviderDirectory(t *testing.T) {
	d, s := fixture(t)
	s.Settings["id"] = "custom"
	s.Settings["artifact"] = "custom-nodes-v1.json"
	expected := filepath.Join(filepath.Dir(s.WorkspaceDir), "custom")
	if err := os.Rename(s.WorkspaceDir, expected); err != nil {
		t.Fatal(err)
	}
	s.WorkspaceDir = expected
	// The provider must not append its own name to the engine-supplied root.
	nested := filepath.Join(expected, "opentofu")
	os.Mkdir(nested, 0700)
	os.WriteFile(filepath.Join(nested, "kit-provider.json"), []byte("wrong nested root"), 0600)
	command, err := d.loadExecution(t.Context(), s, "plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := command.scope
	if scope.WorkspaceDir != expected || scope.ArtifactDir != s.ArtifactDir {
		t.Fatal("provider changed engine-supplied workspace or artifact paths")
	}
}

func TestS3RequiresExplicitStateKey(t *testing.T) {
	d, scope := fixture(t)
	scope.Settings["backend"] = "s3"
	for _, key := range []any{nil, "", "   ", 42} {
		settings := Settings{Schema: 1, Backend: map[string]any{"bucket": "state", "region": "test"}}
		if key != nil {
			settings.Backend["key"] = key
		}
		if err := artifacts.Write(scope.WorkspaceDir, "kit-provider.json", settings); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Execute(t.Context(), scope, "init", nil, nil); err == nil {
			t.Fatalf("accepted key %v", key)
		}
	}
	settings := Settings{Schema: 1, Backend: map[string]any{"key": "instances/example/terraform.tfstate"}}
	if err := artifacts.Write(scope.WorkspaceDir, "kit-provider.json", settings); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(t.TempDir(), "tofu")
	script := `#!/bin/sh
if [ "$1" = version ]; then printf '{"terraform_version":"1.11.5"}'; exit 0; fi
for arg in "$@"; do
    case "$arg" in
        -backend-config=key=instances/example/terraform.tfstate) found=true;;
        -backend-config=path=*|-backend-config=workspace_dir=*|-force-copy) exit 1;;
    esac
done
[ "$found" = true ]
`
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d.Tool = tool
	if _, err := d.Execute(t.Context(), scope, "init", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLocalBackendUsesProviderScopedArtifacts(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "migrate", true: "preserve"}[existing], func(t *testing.T) {
			d, scope := fixture(t)
			scope.Settings["id"] = "custom"
			scope.Settings["artifact"] = "custom-nodes-v1.json"
			state := filepath.Join(scope.ArtifactDir, "custom-terraform.tfstate")
			if existing {
				if err := os.WriteFile(state, []byte("existing state"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			tool := filepath.Join(t.TempDir(), "tofu")
			script := `#!/bin/sh
if [ "$1" = version ]; then printf '{"terraform_version":"1.11.5"}'; exit 0; fi
printf '%s\n' "$@" > args.txt
`
			if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			d.Tool = tool
			if _, err := d.Execute(t.Context(), scope, "init", nil, nil); err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(filepath.Join(scope.WorkspaceDir, "args.txt"))
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range []string{"-backend-config=path=" + state, "-backend-config=workspace_dir=" + state + ".d"} {
				if !bytes.Contains(args, []byte(arg+"\n")) {
					t.Fatalf("missing %s in %s", arg, args)
				}
			}
			if bytes.Contains(args, []byte("-force-copy\n")) == existing {
				t.Fatalf("unexpected migration option: %s", args)
			}
		})
	}
}

func TestPlanRejectsUnsafeExecutionIDBeforeEffects(t *testing.T) {
	d, scope := fixture(t)
	scope.ExecutionID = "../../outside"
	command, err := d.loadExecution(t.Context(), scope, "plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise plan directly: the identifier check must precede file creation.
	if _, err := command.plan(t.Context(), toolrun.Runner{}); err == nil {
		t.Fatal("unsafe execution ID accepted")
	}
	entries, err := os.ReadDir(scope.ArtifactDir)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
