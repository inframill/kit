package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	nameFlag         = "--name"
	localDefaultName = "local/default"
	namedDev         = "example/dev"
)

func runNamedOutput(t *testing.T, f localFixture, name, export string) localResult {
	t.Helper()
	args := f.args("debug.echo", export)
	if name != "" {
		args = append(args, nameFlag, name)
	}
	if got := localRun(t, f.command(t.Context(), args...)); got.code != 0 {
		t.Fatal(got)
	}
	return readLocalResult(t, filepath.Join(f.root, export))
}

func writeSelectionConfig(t *testing.T, f localFixture, mode string) {
	t.Helper()
	data := fmt.Sprintf(`{"blueprint":"example.invalid/test:latest","values":{"mode":"success","label":%q,"secret_ref":"test_token"}}`, mode)
	if err := os.WriteFile(f.instance, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

type selectionHistory struct {
	Executions []localResult `json:"executions"`
	NextCursor string        `json:"next_cursor"`
}

func readSelectionHistory(t *testing.T, f localFixture, name string, extra ...string) selectionHistory {
	t.Helper()
	args := []string{historyArgument, workspaceFlag, f.workspace, outputFlag, jsonFormat}
	if name != "" {
		args = append(args, nameFlag, name)
	}
	got := localRun(t, f.command(t.Context(), append(args, extra...)...))
	var page selectionHistory
	if err := json.Unmarshal([]byte(got.stdout), &page); err != nil || got.code != 0 {
		t.Fatal(got, err)
	}
	return page
}

// SPEC-0000 REQ-017/018: select names across projects, never a sole sibling.
func TestLocalMultipleInstanceSelection(t *testing.T) {
	f := localFiles(t, "success")
	if err := os.WriteFile(filepath.Join(f.blueprint, "template/debug/mode.gotmpl"), []byte("{{ .label }}"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{namedDev, "example/prod", "another/dev", localDefaultName}
	results := make(map[string]localResult)
	for index, name := range names {
		writeSelectionConfig(t, f, name)
		selector := name
		if name == localDefaultName {
			selector = "" // Omission creates the default even when siblings exist.
		}
		first := runNamedOutput(t, f, selector, fmt.Sprintf("first-%d.json", index))
		for _, sibling := range results {
			if first.Instance == sibling.Instance {
				t.Fatal("different names reused an Instance", name, first)
			}
		}
		if index == 0 {
			// A sole custom name does not become the implicit history target.
			got := localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, f.workspace))
			if got.code != 3 || !strings.Contains(got.stderr, "not found") {
				t.Fatal(got)
			}
			assertInstanceCount(t, f.workspace, 1)
		}
		results[name] = first
	}
	for index, name := range names {
		writeSelectionConfig(t, f, name)
		selector := name
		if name == localDefaultName {
			selector = ""
		}
		first := results[name]
		second := runNamedOutput(t, f, selector, fmt.Sprintf("second-%d.json", index))
		if second.Instance != first.Instance || second.Command == first.Command {
			t.Fatal("named reuse changed identity or reused Command", first, second)
		}
		root := filepath.Join(f.workspace, "instances", first.Instance)
		mode, err := os.ReadFile(filepath.Join(root, "workspace/debug/mode"))
		if err != nil || string(mode) != name {
			t.Fatal("selected Values did not render independently", name, string(mode), err)
		}
		for _, file := range []string{filepath.Join("logs", first.Command, "stdout.log"), filepath.Join("logs", second.Command, "stdout.log")} {
			if _, err := os.Stat(filepath.Join(root, file)); err != nil {
				t.Fatal(name, file, err)
			}
		}
		page := readSelectionHistory(t, f, selector, "--limit", "1")
		if len(page.Executions) != 1 || page.Executions[0].Command != second.Command || page.Executions[0].Instance != first.Instance || page.NextCursor == "" {
			t.Fatal("wrong selected history page", name, page)
		}
		next := readSelectionHistory(t, f, selector, "--limit", "1", "--cursor", page.NextCursor)
		if len(next.Executions) != 1 || next.Executions[0].Command != first.Command || next.NextCursor != "" {
			t.Fatal("wrong selected history continuation", name, next)
		}
		got := localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, f.workspace, nameFlag, name, "--command", first.Command, "-o", "json"))
		if got.code != 0 || !strings.Contains(got.stdout, first.Command) {
			t.Fatal("named command lookup failed", got)
		}
	}
	assertSelectionRejections(t, f, results)
}

func assertSelectionRejections(t *testing.T, f localFixture, results map[string]localResult) {
	t.Helper()
	// SPEC-0000 REQ-017/018: command lookup cannot cross the selected Instance boundary.
	got := localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, f.workspace, nameFlag, namedDev, "--command", results[localDefaultName].Command))
	if got.code != 3 || got.stdout != "" {
		t.Fatal("cross-Instance lookup accepted", got)
	}
	got = localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, f.workspace, nameFlag, "missing/target"))
	if got.code != 3 || !strings.Contains(got.stderr, "not found") {
		t.Fatal("missing history target accepted", got)
	}
	assertInstanceCount(t, f.workspace, len(results))
	// SPEC-0000 REQ-017: input mismatch checks apply to the requested name, not a sibling.
	writeSelectionConfig(t, f, "changed")
	got = localRun(t, f.command(t.Context(), append(f.args("debug.echo", "rejected.json"), nameFlag, namedDev)...))
	rejected := readLocalResult(t, filepath.Join(f.root, "rejected.json"))
	if got.code != 2 || rejected.Instance != results[namedDev].Instance || rejected.Command != "" || rejected.Status != localRejected {
		t.Fatal("changed selected Values accepted", got, rejected)
	}
	for name, first := range results {
		page := readSelectionHistory(t, f, name)
		if len(page.Executions) != 2 {
			t.Fatal("rejection altered history", name, page)
		}
		for siblingName, sibling := range results {
			if siblingName == name {
				continue
			}
			path := filepath.Join(f.workspace, "instances", first.Instance, "logs", sibling.Command)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("sibling Command entered selected logs", first, path, err)
			}
		}
	}
}

// SPEC-0000 REQ-017/018: invalid selectors fail before creating or acquiring storage.
func TestLocalInstanceNameRejections(t *testing.T) {
	for _, mode := range []string{localExecCommand, historyArgument} {
		for _, name := range []string{"", "invalid", "../target", "project/name/extra"} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				f := localFiles(t, "success")
				args := []string{historyArgument, workspaceFlag, f.workspace}
				if mode == localExecCommand {
					args = f.args("debug.echo", "invalid-name.json")
				}
				got := localRun(t, f.command(t.Context(), append(args, nameFlag, name)...))
				if got.code != 2 || !strings.Contains(got.stderr, "name must be project/name") {
					t.Fatal(got)
				}
				if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
					t.Fatal("invalid selector created workspace", err)
				}
				if mode == localExecCommand {
					r := readLocalResult(t, filepath.Join(f.root, "invalid-name.json"))
					if r.Status != localRejected || r.Command != "" || r.Instance != "" {
						t.Fatal(r)
					}
				}
			})
		}
	}
}

// SPEC-0000 REQ-017/018: readable failed siblings do not block a healthy target.
func TestLocalInstanceSelectionFailedSibling(t *testing.T) {
	f := localFiles(t, "success")
	failed := runNamedOutput(t, f, namedDev, "failed-before.json")
	brokenManifest := filepath.Join(f.workspace, "instances", failed.Instance, "manifest.yaml")
	if err := os.WriteFile(brokenManifest, []byte("invalid execution manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	healthy := runNamedOutput(t, f, "", "healthy.json")
	reopened := runNamedOutput(t, f, "", "reopened.json")
	if reopened.Instance != healthy.Instance || healthy.Instance == failed.Instance {
		t.Fatal(healthy, reopened, failed)
	}
	page := readSelectionHistory(t, f, "")
	if len(page.Executions) != 2 {
		t.Fatal(page)
	}
	got := localRun(t, f.command(t.Context(), append(f.args("debug.echo", "failed-target.json"), nameFlag, namedDev)...))
	r := readLocalResult(t, filepath.Join(f.root, "failed-target.json"))
	if got.code != 3 || r.Instance != failed.Instance || r.Command != "" || r.Status != localRejected {
		t.Fatal("failed target executed", got, r)
	}
	data, err := os.ReadFile(brokenManifest)
	if err != nil || string(data) != "invalid execution manifest" {
		t.Fatal("failed Instance was replaced", string(data), err)
	}
}

// SPEC-0009 REQ-006: absence checks count UUID leaves, never obsolete name paths.
func assertInstanceCount(t *testing.T, workspace string, want int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(workspace, "instances"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			count++
		}
	}
	if count != want {
		t.Fatal("unexpected Instance count", count, want)
	}
}
