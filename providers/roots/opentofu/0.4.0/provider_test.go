package opentofu

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
func TestPreparationAndSecretsBeforeEffects(t *testing.T) {
	d, s := fixture(t)
	artifacts.Write(s.WorkspaceDir, "kit-provider.json", Settings{Schema: 1, Environment: map[string]string{"HCLOUD_TOKEN": "cloud-token"}})
	if e := d.ValidateSettings(t.Context(), s.Settings); e != nil {
		t.Fatal(e)
	}
	p, e := d.Prepare(t.Context(), s, "plan", nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Execute(t.Context(), map[string]string{})
	var detail *runmill.ProviderError
	if !errors.As(e, &detail) || detail.Code != "MISSING_SECRET" {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(s.ArtifactDir)
	if len(entries) != 0 {
		t.Fatal("effects before secrets")
	}
	entries, _ = os.ReadDir(s.WorkspaceDir)
	if len(entries) != 1 {
		t.Fatal("preparation wrote files")
	}
	for _, args := range []map[string]any{{"destroy": true, "refresh_only": true}, {"plan_id": "x"}, {"unknown": false}} {
		if _, e = d.Prepare(t.Context(), s, "plan", args); e == nil {
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
		if runmill.Serve(t.Context(), d, runmill.ServeOptions{Version: d.Version, Stdin: bytes.NewReader(data), Stdout: &out}) != 0 {
			t.Fatal(out.String())
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
	p, e := d.Prepare(t.Context(), s, "plan", nil)
	if e != nil {
		t.Fatal(e)
	}
	v, e := p.Execute(t.Context(), nil)
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
	if _, e = d.Prepare(t.Context(), bad, "apply", args); e == nil {
		t.Fatal("plan copied to another artifact directory accepted")
	}
	if _, e = d.Prepare(t.Context(), s, "destroy", args); e == nil {
		t.Fatal("wrong mode accepted")
	}
	artifacts.Write(s.WorkspaceDir, "changed.tf", map[string]any{})
	if _, e = d.Prepare(t.Context(), s, "apply", args); e == nil {
		t.Fatal("changed source accepted")
	}
	artifacts.Remove(s.WorkspaceDir, "changed.tf")
	planName := "tofu-plan-" + id + ".bin"
	original, err := artifacts.Read(s.ArtifactDir, planName, artifacts.Limit)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.WriteBytes(s.ArtifactDir, planName, []byte("tampered"))
	if _, e = d.Prepare(t.Context(), s, "apply", args); e == nil {
		t.Fatal("corrupt plan accepted")
	}
	artifacts.WriteBytes(s.ArtifactDir, planName, original)
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", map[string]bool{"valid": true})
	p, e = d.Prepare(t.Context(), s, "apply", args)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execute(t.Context(), nil); e == nil {
		t.Fatal("failed tool accepted")
	}
	data, _ := artifacts.Read(s.ArtifactDir, "tofu-nodes-v1.json", artifacts.Limit)
	if bytes.Contains(data, []byte(`"valid":true`)) {
		t.Fatal("inventory remains valid")
	}
	if _, e = d.Prepare(t.Context(), s, "apply", args); e == nil {
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
	command, err := d.Prepare(t.Context(), s, "plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := command.(*prepared).scope
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
		if _, err := d.Prepare(t.Context(), scope, "init", nil); err == nil {
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
    if [ "$arg" = "-backend-config=key=instances/example/terraform.tfstate" ]; then exit 0; fi
done
exit 1
`
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d.Tool = tool
	prepared, err := d.Prepare(t.Context(), scope, "init", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Execute(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRejectsUnsafeExecutionIDBeforeEffects(t *testing.T) {
	d, scope := fixture(t)
	scope.ExecutionID = "../../outside"
	command, err := d.Prepare(t.Context(), scope, "plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise plan directly: the identifier check must precede file creation.
	if _, err := command.(*prepared).plan(t.Context(), toolrun.Runner{}); err == nil {
		t.Fatal("unsafe execution ID accepted")
	}
	entries, err := os.ReadDir(scope.ArtifactDir)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
