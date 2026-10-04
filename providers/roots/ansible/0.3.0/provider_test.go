package ansible

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

func TestPingPreparation(t *testing.T) {
	d, s, n := fixture(t)
	if _, err := d.Prepare(t.Context(), s, "ping", nil); err == nil {
		t.Fatal("ping accepted missing nodes")
	}
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
	os.Remove(filepath.Join(s.WorkspaceDir, "site.yml"))
	for _, args := range []runmill.Arguments{nil, {"hosts": []string{"local"}}} {
		if _, err := d.Prepare(t.Context(), s, "ping", args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range []runmill.Arguments{{"playbook": "site"}, {"tags": []string{}}, {"hosts": []string{"missing"}}, {"hosts": []string{"local", "local"}}} {
		if _, err := d.Prepare(t.Context(), s, "ping", args); err == nil {
			t.Fatalf("accepted invalid ping arguments: %v", args)
		}
	}
}

func TestToolFailuresReturnWithDiagnostics(t *testing.T) {
	for _, command := range []string{"apply", "ping"} {
		t.Run(command, func(t *testing.T) {
			d, s, n := fixture(t)
			artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
			var logs bytes.Buffer
			s.Diagnostics = &logs
			d.Tool = filepath.Join(t.TempDir(), "ansible")
			os.WriteFile(d.Tool, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf '[core 2.20.1]'; else printf 'host unreachable\\n' >&2; exit 4; fi\n"), 0700)
			args := runmill.Arguments{}
			if command == "apply" {
				args["playbook"] = "site"
			}
			p, err := d.Prepare(t.Context(), s, command, args)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Execute(t.Context(), nil); err == nil || !bytes.Contains(logs.Bytes(), []byte("host unreachable")) {
				t.Fatal("lost tool failure or diagnostics", err)
			}
		})
	}
}

func fixture(t *testing.T) (Provider, runmill.ExecutionContext, artifacts.Nodes) {
	t.Helper()
	shared := t.TempDir()
	work := filepath.Join(shared, "ansible")
	os.Mkdir(work, 0700)
	artifacts.Write(work, "kit-provider.json", Settings{1, "public_ipv4", "root"})
	artifacts.WriteBytes(work, "site.yml", []byte("[]"))
	s := runmill.ExecutionContext{WorkspaceDir: work, ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Settings: runmill.Settings{"id": "ansible", "settings_file": "kit-provider.json", "producer_id": "tofu", "producer_version": "0.3.0", "artifact": "tofu-nodes-v1.json", "connection": "local", "host_key_policy": "strict", "roles": []string{"local"}, "playbooks": map[string]string{"site": "site.yml"}}}
	nodes := artifacts.Nodes{Schema: 1, Valid: true, ArtifactScope: artifacts.Scope(s.ArtifactDir), ProducerID: "tofu", ProducerVersion: "0.3.0", ExecutionID: s.ExecutionID, Nodes: []artifacts.Node{{Name: "local", Roles: []string{"local"}, Architecture: "arm64", PublicIPv4: "127.0.0.1"}}}
	return Provider{"0.3.0", "2.20.1", "/missing"}, s, nodes
}
func TestPreparationRequiresOnlyCommandInputs(t *testing.T) {
	d, s, n := fixture(t)
	if _, e := d.Prepare(t.Context(), s, "syntax-check", map[string]any{"playbook": "site"}); e != nil {
		t.Fatal(e)
	}
	if _, e := d.Prepare(t.Context(), s, "apply", map[string]any{"playbook": "site"}); e == nil {
		t.Fatal("missing nodes accepted")
	}
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
	if _, e := d.Prepare(t.Context(), s, "inventory", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.WorkspaceDir, "kit-inventory.json")); !os.IsNotExist(e) {
		t.Fatal("preparation wrote inventory")
	}
	n.ArtifactScope = "other"
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
	if _, e := d.Prepare(t.Context(), s, "inventory", nil); e == nil {
		t.Fatal("wrong binding accepted")
	}
}
func TestExecutionVerifiesRecap(t *testing.T) {
	for _, tc := range []struct {
		recap string
		ok    bool
	}{{`{"schema":1,"hosts":{"local":{"ok":1,"changed":1}}}`, true}, {`{"schema":1,"hosts":{"local":{"ok":1,"failures":1}}}`, false}, {`{"schema":1,"hosts":{"local":{"ok":1,"ignored":1}}}`, false}, {`{"schema":1,"hosts":{"local":{"ok":1,"rescued":1}}}`, false}, {`{"schema":1,"hosts":{"local":{"ok":1,"unreachable":1}}}`, false}, {`{"schema":1,"hosts":{"wrong":{"ok":1}}}`, false}, {`{"schema":1,"hosts":{}}`, false}, {`missing`, false}} {
		t.Run(tc.recap, func(t *testing.T) {
			d, s, n := fixture(t)
			artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
			d.Tool = filepath.Join(t.TempDir(), "ansible")
			os.WriteFile(d.Tool, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf '[core 2.20.1]'; else printf '%s' '"+tc.recap+"'; fi\n"), 0700)
			p, e := d.Prepare(t.Context(), s, "apply", map[string]any{"playbook": "site"})
			if e != nil {
				t.Fatal(e)
			}
			_, e = p.Execute(t.Context(), nil)
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
		})
	}
}

func TestUsesEngineSuppliedProviderDirectory(t *testing.T) {
	d, s, _ := fixture(t)
	s.Settings["id"] = "custom"

	expected := filepath.Join(filepath.Dir(s.WorkspaceDir), "custom")
	if err := os.Rename(s.WorkspaceDir, expected); err != nil {
		t.Fatal(err)
	}
	s.WorkspaceDir = expected
	// The provider must not append its own name to the engine-supplied root.
	nested := filepath.Join(expected, "ansible")
	os.Mkdir(nested, 0700)
	os.WriteFile(filepath.Join(nested, "kit-provider.json"), []byte("wrong nested root"), 0600)
	command, err := d.Prepare(t.Context(), s, "syntax-check", map[string]any{"playbook": "site"})
	if err != nil {
		t.Fatal(err)
	}
	scope := command.(*prepared).scope
	if scope.WorkspaceDir != expected || scope.ArtifactDir != s.ArtifactDir {
		t.Fatal("provider changed engine-supplied workspace or artifact paths")
	}
}
