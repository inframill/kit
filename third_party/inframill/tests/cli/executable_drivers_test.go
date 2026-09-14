package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-0000 REQ-008/019: users can supply a driver without recompiling Inframill.
func TestCustomExecutableDriver(t *testing.T) {
	f := localFiles(t, "success")
	driver := filepath.Join(f.root, "custom-driver")
	const source = `#!/bin/sh
case "$1" in
info) printf '%s\n' '{"protocol":1,"name":"debug","version":"9.4.0","commands":{"echo":{"description":"custom fixture","argumentsSchema":{"type":"object","additionalProperties":false},"safety":"read_only","effects":["reads fixture"],"verification":"returns custom result"}}}';;
validate) cat >/dev/null; printf '{"valid":true}\n';;
run) echo 'custom log' >&2; printf '{"implementation":"custom","version":"9.4.0","request":'; cat; printf '}\n';;
*) exit 1;;
esac
`
	if err := os.WriteFile(driver, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("handlers:\n  - type: connect-engine\nengine: {dataDir: %q, drivers: {custom: {path: ./custom-driver}, offline: {path: ./missing-driver}}}\n", f.workspace)
	if err := os.WriteFile(filepath.Join(f.root, ".inframill.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(f.root, "source", "manifest.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.ReplaceAll(string(data), "debug:", "custom:")), 0o600); err != nil {
		t.Fatal(err)
	}
	got := localRun(t, f.command(t.Context(), f.args("custom.echo", "custom.json")...))
	if got.code != 0 || !strings.Contains(got.stdout, `"implementation":"custom"`) || got.stderr != "custom log\n" {
		t.Fatal(got)
	}
	result := readLocalResult(t, filepath.Join(f.root, "custom.json"))
	if result.Status != "succeeded" || !strings.Contains(string(result.Result), "9.4.0") {
		t.Fatal(result)
	}
	// SPEC-0009 REQ-006: inspect the actual stdin JSON received by an executable.
	var response struct {
		Request struct {
			WorkspaceDirectory string `json:"workspaceDirectory"`
			ArtifactDirectory  string `json:"artifactDirectory"`
			Binding            struct {
				InstanceID string `json:"instanceID"`
				Name       string `json:"name"`
			} `json:"binding"`
		} `json:"request"`
	}
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(f.workspace, "instances", result.Instance)
	if response.Request.WorkspaceDirectory != filepath.Join(root, "workspace", "custom") || response.Request.ArtifactDirectory != filepath.Join(root, "artifacts") || !filepath.IsAbs(root) || response.Request.Binding.InstanceID != result.Instance || response.Request.Binding.Name != localDefaultName {
		t.Fatal("incorrect executable paths or identity", response)
	}
	if err := os.Remove(driver); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, ".inframill.yaml")); err != nil {
		t.Fatal(err)
	}
	history := localRun(t, f.command(t.Context(), "history", "--workspace", f.workspace, "-o", "json"))
	if history.code != 0 || !strings.Contains(history.stdout, result.Command) {
		t.Fatal("history depended on driver or config", history)
	}
}

// SPEC-0000 REQ-019: a failed startup probe precedes workspace creation.
func TestMissingExecutableDriver(t *testing.T) {
	f := localFiles(t, "success")
	config := fmt.Sprintf("handlers:\n  - type: connect-engine\nengine: {dataDir: %q, drivers: {debug: {path: ./missing-driver}}}\n", f.workspace)
	if err := os.WriteFile(filepath.Join(f.root, ".inframill.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	got := localRun(t, f.command(t.Context(), f.args("debug.echo", "")...))
	if got.code != 3 || !strings.Contains(got.stderr, "not executable") {
		t.Fatal(got)
	}
	if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
		t.Fatal("failed driver probe created workspace", err)
	}
}

// SPEC-0000 REQ-010/015/021: Driver subcommands need no config and identify their own version.
func TestDebugSubcommandProtocol(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".inframill.yaml"), []byte("invalid config"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := invoke(t, t.Context(), root, "", "driver", "debug", "info")
	if info.code != 0 || info.stderr != "" {
		t.Fatal(info)
	}
	var metadata struct {
		Name     string         `json:"name"`
		Version  string         `json:"version"`
		Protocol int            `json:"protocol"`
		Commands map[string]any `json:"commands"`
	}
	if err := json.Unmarshal([]byte(info.stdout), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "debug" || metadata.Version != "0.0.0-dev" || metadata.Protocol != 1 || len(metadata.Commands) != 8 {
		t.Fatal(metadata)
	}
	valid := invoke(t, t.Context(), root, `{"protocol":1,"config":{}}`, "driver", "debug", "validate")
	if valid.code != 0 || valid.stdout != "{\"valid\":true}\n" || valid.stderr != "" {
		t.Fatal(valid)
	}
	for _, input := range []string{`{"protocol":9,"config":{}}`, `{"protocol":1,"config":{"unsupported":true}}`, `{} {}`} {
		got := invoke(t, t.Context(), root, input, "driver", "debug", "validate")
		if got.code == 0 || got.stdout != "" {
			t.Fatal("invalid protocol accepted", got)
		}
	}
}

// SPEC-0000 REQ-009/010: the embedded Driver uses the external host/log/result path.
func TestDebugExternalLogsFailureAndWait(t *testing.T) {
	f := localFiles(t, "success")
	for _, tc := range []struct {
		command, args, log string
		code               int
	}{
		{"debug.logs", `{"stdout":"first\n","stderr":"second\n"}`, "first\nsecond\n", 0},
		{"debug.secret", `{"reference":"test_token"}`, "[REDACTED]\n", 0},
		{"debug.wait", `{"durationMs":1}`, "", 0},
		{"debug.fail", `{"message":"expected debug failure"}`, "expected debug failure", 1},
	} {
		path := filepath.Join(f.root, "args.json")
		if err := os.WriteFile(path, []byte(tc.args), 0o600); err != nil {
			t.Fatal(err)
		}
		got := localRun(t, f.command(t.Context(), append(f.args(tc.command, ""), "--args-file", path)...))
		if got.code != tc.code || !strings.Contains(got.stderr, tc.log) || strings.Contains(got.stderr+got.stdout, "fixture-secret") {
			t.Fatal(tc.command, got)
		}
		if tc.code == 0 && !json.Valid([]byte(got.stdout)) {
			t.Fatal(got)
		}
		if tc.code != 0 && got.stdout != "" {
			t.Fatal("failed Driver published result", got)
		}
	}
}
