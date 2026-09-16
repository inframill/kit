package opentofu

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inframill/kit/internal/artifacts"
	driver "github.com/sir-hassan/inframill/pkg/driver"
)

func fixture(t *testing.T) (Driver, driver.ExecutionContext) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "tofu")
	os.Mkdir(work, 0700)
	artifacts.Write(work, "kit-driver.json", Settings{Schema: 1, Backend: map[string]any{}, Environment: map[string]string{}})
	return Driver{Version: "0.1.0", ToolVersion: "1.11.5", Tool: "/missing"}, driver.ExecutionContext{WorkspaceDirectory: work, ArtifactDirectory: t.TempDir(), CommandID: "22222222-2222-4222-8222-222222222222", Binding: driver.Binding{InstanceID: "11111111-1111-4111-8111-111111111111", Name: "test/demo", BlueprintReference: "local@sha256:" + strings.Repeat("a", 64)}, DriverConfig: driver.Config{"required_driver_version": "0.1.0", "id": "tofu", "settings_file": "kit-driver.json", "backend": "local", "node_output": "nodes", "artifact": "tofu-nodes-v1.json"}}
}
func TestPreparationAndSecretsBeforeEffects(t *testing.T) {
	d, s := fixture(t)
	artifacts.Write(s.WorkspaceDirectory, "kit-driver.json", Settings{Schema: 1, Environment: map[string]string{"HCLOUD_TOKEN": "cloud-token"}})
	if e := d.ValidateManifest(t.Context(), s.DriverConfig); e != nil {
		t.Fatal(e)
	}
	p, e := d.Prepare(t.Context(), s, "plan", nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Execute(t.Context(), driver.Secrets{})
	var detail *driver.Error
	if !errors.As(e, &detail) || detail.Code != "MISSING_SECRET" {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(s.ArtifactDirectory)
	if len(entries) != 0 {
		t.Fatal("effects before secrets")
	}
	entries, _ = os.ReadDir(s.WorkspaceDirectory)
	if len(entries) != 1 {
		t.Fatal("preparation wrote files")
	}
	for _, args := range []map[string]any{{"destroy": true, "refresh_only": true}, {"plan_id": "x"}, {"unknown": false}} {
		if _, e = d.Prepare(t.Context(), s, "plan", args); e == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	for _, method := range []string{"info", "validate_manifest", "validate_command"} {
		params := map[string]any{}
		if method != "info" {
			params["config"] = s.DriverConfig
		}
		if method == "validate_command" {
			params["binding"] = s.Binding
			params["workspaceDirectory"] = s.WorkspaceDirectory
			params["artifactDirectory"] = s.ArtifactDirectory
			params["command"] = "plan"
		}
		data, _ := json.Marshal(map[string]any{"protocol": 1, "method": method, "params": params})
		var out bytes.Buffer
		if driver.Serve(t.Context(), d, driver.ServeOptions{Version: d.Version, Stdin: bytes.NewReader(data), Stdout: &out}) != 0 {
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
	s.CommandID = "33333333-3333-4333-8333-333333333333"
	args := map[string]any{"plan_id": id}
	bad := s
	bad.Binding.BlueprintReference = "different"
	if _, e = d.Prepare(t.Context(), bad, "apply", args); e == nil {
		t.Fatal("wrong reference accepted")
	}
	if _, e = d.Prepare(t.Context(), s, "destroy", args); e == nil {
		t.Fatal("wrong mode accepted")
	}
	artifacts.Write(s.WorkspaceDirectory, "changed.tf", map[string]any{})
	if _, e = d.Prepare(t.Context(), s, "apply", args); e == nil {
		t.Fatal("changed source accepted")
	}
	artifacts.Remove(s.WorkspaceDirectory, "changed.tf")
	planName := "tofu-plan-" + id + ".bin"
	original, err := artifacts.Read(s.ArtifactDirectory, planName, artifacts.Limit)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.WriteBytes(s.ArtifactDirectory, planName, []byte("tampered"))
	if _, e = d.Prepare(t.Context(), s, "apply", args); e == nil {
		t.Fatal("corrupt plan accepted")
	}
	artifacts.WriteBytes(s.ArtifactDirectory, planName, original)
	artifacts.Write(s.ArtifactDirectory, "tofu-nodes-v1.json", map[string]bool{"valid": true})
	p, e = d.Prepare(t.Context(), s, "apply", args)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execute(t.Context(), nil); e == nil {
		t.Fatal("failed tool accepted")
	}
	data, _ := artifacts.Read(s.ArtifactDirectory, "tofu-nodes-v1.json", artifacts.Limit)
	if bytes.Contains(data, []byte(`"valid":true`)) {
		t.Fatal("inventory remains valid")
	}
	if _, e = d.Prepare(t.Context(), s, "apply", args); e == nil {
		t.Fatal("partially applied plan replayed")
	}
}
