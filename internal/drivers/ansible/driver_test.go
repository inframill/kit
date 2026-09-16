package ansible

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inframill/kit/internal/artifacts"
	driver "github.com/sir-hassan/inframill/pkg/driver"
)

func fixture(t *testing.T) (Driver, driver.ExecutionContext, artifacts.Nodes) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "ansible")
	os.Mkdir(work, 0700)
	artifacts.Write(work, "kit-driver.json", Settings{1, "public_ipv4", "root"})
	artifacts.WriteBytes(work, "site.yml", []byte("[]"))
	s := driver.ExecutionContext{WorkspaceDirectory: work, ArtifactDirectory: t.TempDir(), CommandID: "22222222-2222-4222-8222-222222222222", Binding: driver.Binding{InstanceID: "11111111-1111-4111-8111-111111111111", Name: "test/demo", BlueprintReference: "local@sha256:" + strings.Repeat("a", 64)}, DriverConfig: driver.Config{"required_driver_version": "0.1.0", "id": "ansible", "settings_file": "kit-driver.json", "producer_id": "tofu", "producer_version": "0.1.0", "artifact": "tofu-nodes-v1.json", "connection": "local", "host_key_policy": "strict", "roles": []string{"local"}, "playbooks": map[string]string{"site": "site.yml"}}}
	nodes := artifacts.Nodes{Schema: 1, Valid: true, Binding: s.Binding, ProducerID: "tofu", ProducerVersion: "0.1.0", CommandID: s.CommandID, Nodes: []artifacts.Node{{Name: "local", Roles: []string{"local"}, Architecture: "arm64", PublicIPv4: "127.0.0.1"}}}
	return Driver{"0.1.0", "2.20.1", "/missing"}, s, nodes
}
func TestPreparationRequiresOnlyCommandInputs(t *testing.T) {
	d, s, n := fixture(t)
	if _, e := d.Prepare(t.Context(), s, "syntax-check", map[string]any{"playbook": "site"}); e != nil {
		t.Fatal(e)
	}
	if _, e := d.Prepare(t.Context(), s, "apply", map[string]any{"playbook": "site"}); e == nil {
		t.Fatal("missing nodes accepted")
	}
	artifacts.Write(s.ArtifactDirectory, "tofu-nodes-v1.json", n)
	if _, e := d.Prepare(t.Context(), s, "inventory", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.WorkspaceDirectory, "kit-inventory.json")); !os.IsNotExist(e) {
		t.Fatal("preparation wrote inventory")
	}
	n.Binding.BlueprintReference = "other"
	artifacts.Write(s.ArtifactDirectory, "tofu-nodes-v1.json", n)
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
			artifacts.Write(s.ArtifactDirectory, "tofu-nodes-v1.json", n)
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
