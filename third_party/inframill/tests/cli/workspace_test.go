package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/internal/blueprint"
)

const defaultWorkspace = ".inframill"

func (f localFixture) defaultArgs() []string {
	// Relative source resolves from f.root, not the repository or process executable.
	return []string{localExecCommand, "debug.echo", "--blueprint-dir", filepath.Base(f.blueprint), instanceFlag, f.instance}
}

// SPEC-0000 REQ-007/017: repeated execs need no workspace or result-file flag.
func TestDefaultWorkspaceReuse(t *testing.T) {
	f := localFiles(t, "success")
	configDir := filepath.Join(f.root, "configs")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "cluster.json")
	if err := os.Rename(f.instance, configPath); err != nil {
		t.Fatal(err)
	}
	f.instance = configPath
	build := func(name string) string {
		t.Helper()
		digest, err := blueprint.BuildLayout(t.Context(), f.blueprint, filepath.Join(f.root, name))
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	digest := build("before-layout")
	for range 2 {
		got := localRun(t, f.command(t.Context(), f.defaultArgs()...))
		if got.code != 0 || !strings.Contains(got.stdout, "values") {
			t.Fatal(got)
		}
	}
	workspace := filepath.Join(f.root, defaultWorkspace)
	for _, unexpected := range []string{filepath.Join(configDir, defaultWorkspace), filepath.Join(f.blueprint, defaultWorkspace), filepath.Join(f.root, defaultWorkspace, "blueprints")} {
		if _, err := os.Stat(unexpected); !os.IsNotExist(err) {
			t.Fatal("local override created unrelated storage", unexpected, err)
		}
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("workspace is not private", info, err)
	}
	got := localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, workspace, outputFlag, jsonFormat))
	var page struct {
		Executions []localResult `json:"executions"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &page); err != nil || got.code != 0 || len(page.Executions) != 2 {
		t.Fatal(got, err)
	}
	first, second := page.Executions[0], page.Executions[1]
	if first.Instance == "" || first.Instance != second.Instance || first.Command == second.Command || len(first.Result) == 0 || len(second.Result) == 0 {
		t.Fatal("executions did not retain separate records in one Instance", page)
	}
	root := onlyInstanceRoot(t, workspace)
	for _, command := range page.Executions {
		data, err := os.ReadFile(filepath.Join(root, "logs", command.Command, "stdout.log"))
		if err != nil || !strings.Contains(string(data), "values") {
			t.Fatal("missing separate log", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, defaultWorkspace)); !os.IsNotExist(err) {
		t.Fatal("workspace entered saved source", err)
	}
	if after := build("after-layout"); after != digest {
		t.Fatal("workspace changed package digest", digest, after)
	}
	if err := os.WriteFile(filepath.Join(f.blueprint, "template/debug/changed.txt"), []byte("# changed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = localRun(t, f.command(t.Context(), f.defaultArgs()...))
	if got.code != 2 || !strings.Contains(got.stderr, "changed") {
		t.Fatal("changed source executed", got)
	}
	got = localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, workspace, "--command", first.Command, "-o", "json"))
	if got.code != 0 || !strings.Contains(got.stdout, first.Command) {
		t.Fatal("history depends on unchanged source", got)
	}
}

// SPEC-0000 REQ-018: result exports remain outside the selected data directory.
func TestDefaultWorkspaceResultBoundary(t *testing.T) {
	f := localFiles(t, "success")
	workspace := filepath.Join(f.root, defaultWorkspace)
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(workspace, "export.json")
	args := append(f.defaultArgs(), "--result-file", resultPath)
	got := localRun(t, f.command(t.Context(), args...))
	if got.code != 3 {
		t.Fatal("result inside default workspace accepted", got)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatal("result validation created workspace contents", entries, err)
	}
}

// SPEC-0000 REQ-007/017: missing inputs and workspace collisions preserve user files.
func TestDefaultWorkspaceRejections(t *testing.T) {
	for _, kind := range []string{"missing-instance", "invalid-package", fileCollision, symlinkCollision} {
		t.Run(kind, func(t *testing.T) {
			f := localFiles(t, "success")
			workspace := filepath.Join(f.root, defaultWorkspace)
			args := f.defaultArgs()
			wantCode := 3
			sentinel := "existing user data"
			target := filepath.Join(f.root, "target")
			switch kind {
			case "missing-instance":
				args = args[:len(args)-2]
				wantCode = 2
			case "invalid-package":
				if err := os.WriteFile(f.instance, []byte(`{"blueprint":"invalid","values":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				args = []string{localExecCommand, "debug.echo", instanceFlag, f.instance}
				wantCode = 2
			case fileCollision:
				if err := os.WriteFile(workspace, []byte(sentinel), 0o600); err != nil {
					t.Fatal(err)
				}
			case symlinkCollision:
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte(sentinel), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, workspace); err != nil {
					t.Fatal(err)
				}
			}
			got := localRun(t, f.command(t.Context(), args...))
			if got.code != wantCode || got.stdout != "" {
				t.Fatal(got)
			}
			switch kind {
			case fileCollision:
				data, err := os.ReadFile(workspace)
				if err != nil || string(data) != sentinel {
					t.Fatal("existing file changed", err)
				}
			case symlinkCollision:
				entries, err := os.ReadDir(target)
				if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
					t.Fatal("symlink target changed", entries, err)
				}
			default:
				if _, err := os.Stat(filepath.Join(workspace, "instances")); !os.IsNotExist(err) {
					t.Fatal("missing input created Instance storage", err)
				}
			}
		})
	}
}

const (
	instanceFlag     = "--instance"
	fileCollision    = "file"
	symlinkCollision = "symlink"
)

// SPEC-0000 REQ-011/017: never start a new Instance beside a legacy flat workspace.
func TestLegacyLocalWorkspacePreserved(t *testing.T) {
	f := localFiles(t, "success")
	legacy := filepath.Join(f.workspace, instanceID)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "instance.json")
	if err := os.WriteFile(marker, []byte("existing user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{f.args("debug.echo", ""), {historyArgument, workspaceFlag, f.workspace}} {
		if got := localRun(t, f.command(t.Context(), args...)); got.code != 3 || !strings.Contains(got.stderr, "legacy") {
			t.Fatal(got)
		}
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "existing user data" {
		t.Fatal("legacy state changed", err)
	}
	entries, err := os.ReadDir(f.workspace)
	if err != nil || len(entries) != 1 || entries[0].Name() != instanceID {
		t.Fatal("legacy root changed", entries, err)
	}
}

// SPEC-0009 REQ-007: incompatible storage is preserved, while health/help and an
// intentionally fresh data root remain usable without a migration path.
func TestREQ007UUIDStorageTransition(t *testing.T) {
	for _, kind := range []string{"named", "unmarked", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			f := localFiles(t, "success")
			first := runNamedOutput(t, f, "", "before.json")
			root := filepath.Join(f.workspace, "instances", first.Instance)
			metadata := filepath.Join(root, "instance.json")
			data, err := os.ReadFile(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "unmarked" {
				var doc map[string]any
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				delete(doc, "storage_layout")
				data, err = json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(metadata, data, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				legacy := filepath.Join(f.workspace, "instances", "local", "default")
				if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "named" {
					if err := os.Rename(root, legacy); err != nil {
						t.Fatal(err)
					}
					metadata = filepath.Join(legacy, "instance.json")
				} else if err := os.Mkdir(legacy, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{f.args("debug.echo", ""), {historyArgument, workspaceFlag, f.workspace}} {
				if got := localRun(t, f.command(t.Context(), args...)); got.code != 3 || !strings.Contains(got.stderr, "new empty dataDir") {
					t.Fatal("incompatible storage accepted", got)
				}
			}
			for _, args := range [][]string{{"driver", "list", "-o", "json"}, {"--help"}} {
				if got := localRun(t, f.command(t.Context(), args...)); got.code != 0 {
					t.Fatal("storage blocked health/help", got)
				}
			}
			if after, err := os.ReadFile(metadata); err != nil || string(after) != string(data) {
				t.Fatal("old metadata modified", err)
			}
			f.workspace = filepath.Join(f.root, "fresh-data")
			writeDataConfig(t, filepath.Join(f.root, ".inframill.yaml"), f.workspace)
			fresh := runNamedOutput(t, f, "", "fresh.json")
			if fresh.Instance == first.Instance {
				t.Fatal("fresh root reused identity")
			}
			assertInstanceCount(t, f.workspace, 1)
			if _, err := os.Stat(filepath.Join(f.workspace, "instances", fresh.Instance, "instance.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
