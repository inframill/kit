package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	oci "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sir-hassan/inframill/internal/blueprint"
	"github.com/sir-hassan/inframill/internal/blueprint/content"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	validateCommand = "debug.echo"
	missingInput    = "missing-file"
	resultFileFlag  = "--result-file"
	directoryInput  = "directory"
	malformedInput  = "malformed"
	oversizedInput  = "oversized"
)

func writeInstance(t *testing.T, path, reference string) {
	t.Helper()
	config := struct {
		Blueprint string                   `json:"blueprint"`
		Values    inframill.InstanceValues `json:"values"`
	}{reference, inframill.InstanceValues{"mode": "success", "secret_ref": "test_token"}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// This registry serves disposable source packages; it never contacts a provider.
func localRegistry(t *testing.T, directory string) (*httptest.Server, string) {
	t.Helper()
	files, err := content.Snapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := blueprint.Pack(files)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			data = archive.Manifest
			w.Header().Set("Content-Type", oci.MediaTypeImageManifest)
		case strings.HasSuffix(r.URL.Path, digest.FromBytes(archive.Config).String()):
			data = archive.Config
		case strings.HasSuffix(r.URL.Path, digest.FromBytes(archive.Layer).String()):
			data = archive.Layer
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(data).String())
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(data) //nolint:errcheck // Disposable registry client may disconnect.
		}
	}))
	t.Cleanup(server.Close)
	return server, digest.FromBytes(archive.Manifest).String()
}

// SPEC-0000 REQ-007/017: fetch missing tags/pins using Engine's store, then reuse offline.
func TestLocalInstanceFetchAndCache(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(strconv.FormatBool(pinned), func(t *testing.T) {
			f := localFiles(t, "success")
			server, dgst := localRegistry(t, f.blueprint)
			host := strings.TrimPrefix(server.URL, "http://")
			pin := host + "/fixture@" + dgst
			reference := host + "/fixture:latest"
			if pinned {
				reference = pin
			}
			writeInstance(t, f.instance, reference)
			args := []string{localExecCommand, validateCommand, instanceFlag, f.instance, resultFileFlag, filepath.Join(f.root, "fetched.json")}
			workspace := filepath.Join(f.root, defaultWorkspace)
			if pinned {
				workspace = filepath.Join(f.root, "configured-data")
				writeDataConfig(t, filepath.Join(f.root, ".inframill.yaml"), workspace)
			}
			command := f.command(t.Context(), args...)
			command.Env = append(command.Env, "INFRAMILL_HTTP_REGISTRIES="+host)
			if got := localRun(t, command); got.code != 0 {
				t.Fatal(got)
			}
			first := readLocalResult(t, filepath.Join(f.root, "fetched.json"))
			metadata, err := os.ReadFile(filepath.Join(workspace, "instances", first.Instance, "instance.json"))
			if err != nil || !strings.Contains(string(metadata), pin) {
				t.Fatal("configured package identity was not pinned", string(metadata), err)
			}
			store, err := blueprint.NewStore(filepath.Join(workspace, "blueprints"), nil)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := store.Resolve(t.Context(), reference)
			if err != nil || selected.Reference != pin {
				t.Fatal("exec did not populate Engine-compatible storage", selected, err)
			}
			if pinned {
				if _, statErr := os.Stat(filepath.Join(f.root, defaultWorkspace)); !os.IsNotExist(statErr) {
					t.Fatal("custom root created default storage", statErr)
				}
			}
			server.Close()
			// A cached tag must work offline too, before switching to its exact pin.
			args = args[:len(args)-2]
			for range 2 {
				if got := localRun(t, f.command(t.Context(), args...)); got.code != 0 {
					t.Fatal("cached execution required registry access", got)
				}
				writeInstance(t, f.instance, pin)
			}
			got := localRun(t, f.command(t.Context(), historyArgument, workspaceFlag, workspace, outputFlag, jsonFormat))
			var page struct {
				Executions []localResult `json:"executions"`
			}
			if decodeErr := json.Unmarshal([]byte(got.stdout), &page); decodeErr != nil || got.code != 0 || len(page.Executions) != 3 {
				t.Fatal("cached executions lost history", got, decodeErr)
			}
			for _, record := range page.Executions {
				if record.Instance != first.Instance || record.Status != localSucceeded {
					t.Fatal(record)
				}
			}
			// A newly fetched selection cannot replace this Instance's saved source.
			if writeErr := os.WriteFile(filepath.Join(f.blueprint, "template/debug/new.txt"), []byte("# different package"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			other, _ := localRegistry(t, f.blueprint)
			otherHost := strings.TrimPrefix(other.URL, "http://")
			writeInstance(t, f.instance, otherHost+"/fixture:latest")
			changedCommand := f.command(t.Context(), args...)
			changedCommand.Env = append(changedCommand.Env, "INFRAMILL_HTTP_REGISTRIES="+otherHost)
			if got := localRun(t, changedCommand); got.code != 2 || !strings.Contains(got.stderr, "changed") {
				t.Fatal("changed package was admitted", got)
			}
			writeInstance(t, f.instance, pin)
			preserved, err := os.ReadFile(filepath.Join(workspace, "instances", first.Instance, "instance.json"))
			if err != nil || string(preserved) != string(metadata) {
				t.Fatal("rejected package changed Instance metadata", err)
			}
			// Both source and index belong to protected package storage.
			for _, directory := range []string{selected.Directory, filepath.Join(workspace, "blueprints")} {
				resultPath := filepath.Join(directory, "result.json")
				if got := localRun(t, f.command(t.Context(), append(args, resultFileFlag, resultPath)...)); got.code != 3 {
					t.Fatal(got)
				}
				if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
					t.Fatal("reserved result in package storage", err)
				}
				if got := localRun(t, f.command(t.Context(), append(args, "--env-file", filepath.Join(directory, "missing.env"))...)); got.code != 2 || !strings.Contains(got.stderr, "outside") {
					t.Fatal(got)
				}
			}
			// Cached source corruption must be preserved and rejected, never refetched.
			changed := filepath.Join(selected.Directory, "template/debug/mode.gotmpl")
			if err := os.WriteFile(changed, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := localRun(t, f.command(t.Context(), args...)); got.code != 3 || !strings.Contains(got.stderr, "changed") {
				t.Fatal(got)
			}
		})
	}
}

// SPEC-0000 REQ-007/017: malformed inputs export rejections without workspace effects.
func TestLocalInstanceConfigRejections(t *testing.T) {
	cases := map[string]string{
		"raw-values":        `{"mode":"success","secret_ref":"test_token"}`,
		"missing-blueprint": `{"values":{}}`,
		"missing-values":    `{"blueprint":"unused"}`,
		"empty-blueprint":   `{"blueprint":" ","values":{}}`,
		"wrong-blueprint":   `{"blueprint":{},"values":{}}`,
		"null-values":       `{"blueprint":"unused","values":null}`,
		"array-values":      `{"blueprint":"unused","values":[]}`,
		"unknown-field":     `{"blueprint":"unused","values":{},"name":"ignored"}`,
		malformedInput:      `{"private-secret-sentinel":`,
		"null":              `null`, "array": `[]`, oversizedInput: strings.Repeat(" ", (1<<20)+1),
		missingInput: "", directoryInput: "", symlinkCollision: "", "stdin": "",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			f := localFiles(t, "success")
			path := filepath.Join(f.root, "bad.json")
			switch name {
			case missingInput:
			case directoryInput:
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case symlinkCollision:
				if err := os.Symlink(f.instance, path); err != nil {
					t.Fatal(err)
				}
			case "stdin":
				path = "-"
			default:
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f.instance = path
			got := localRun(t, f.command(t.Context(), f.args(validateCommand, "rejected.json")...))
			if got.code != 2 || got.stdout != "" || strings.Contains(got.stderr, "private-secret-sentinel") {
				t.Fatal(got)
			}
			r := readLocalResult(t, filepath.Join(f.root, "rejected.json"))
			if r.Status != localRejected || r.Command != "" {
				t.Fatal(r)
			}
			if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
				t.Fatal("invalid config created workspace", err)
			}
		})
	}
	for _, flag := range []string{"--values", "--blueprint", "--blueprint-dir="} {
		f := localFiles(t, "success")
		if got := localRun(t, f.command(t.Context(), append(f.args(validateCommand, ""), flag)...)); got.code != 2 {
			t.Fatal(flag, got)
		}
	}
}

// SPEC-0000 REQ-007/017: a failed fetch preserves structured rejection and no workspace.
func TestLocalInstanceFetchFailure(t *testing.T) {
	f := localFiles(t, "success")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	writeInstance(t, f.instance, host+"/missing:latest")
	baseArgs := []string{localExecCommand, validateCommand, instanceFlag, f.instance}
	// All these rejections must precede network acquisition as well as admission.
	for _, flags := range [][]string{
		{"--args-file", filepath.Join(f.root, "missing-args.json")},
		{"--env-file", filepath.Join(f.root, "missing.env")},
		{resultFileFlag, f.instance},
	} {
		c := f.command(t.Context(), append(baseArgs, flags...)...)
		c.Env = append(c.Env, "INFRAMILL_HTTP_REGISTRIES="+host)
		if got := localRun(t, c); got.code != 2 && got.code != 3 {
			t.Fatal(got)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid inputs contacted registry", requests.Load())
	}
	args := []string{localExecCommand, validateCommand, instanceFlag, f.instance, resultFileFlag, filepath.Join(f.root, "rejected.json")}
	c := f.command(t.Context(), args...)
	c.Env = append(c.Env, "INFRAMILL_HTTP_REGISTRIES="+host)
	if got := localRun(t, c); got.code != 3 || got.stdout != "" {
		t.Fatal(got)
	}
	if requests.Load() == 0 {
		t.Fatal("missing Blueprint was not fetched")
	}
	r := readLocalResult(t, filepath.Join(f.root, "rejected.json"))
	if r.Status != localRejected || r.Command != "" {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(f.root, defaultWorkspace, "instances")); !os.IsNotExist(err) {
		t.Fatal("failed fetch created workspace", err)
	}
}
