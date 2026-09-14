package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	wire "github.com/sir-hassan/inframill/pkg/driver"
)

func TestRealInframillDiscoveryVersionsAndCancellation(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(t.TempDir(), "inframill-kit")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/inframill-kit")
	cmd.Dir = root
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build: %s: %v", b, e)
	}
	work := t.TempDir()
	marker := filepath.Join(work, "escaped-child")
	tool := filepath.Join(t.TempDir(), "tofu")
	script := `#!/bin/sh
if [ "$1" = version ]; then printf '{"terraform_version":"1.11.5"}'; exit 0; fi
(sleep 0.4; touch escaped-child) &
wait
`
	if e = os.WriteFile(tool, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	configs := wire.Configurations{}
	for _, name := range []string{"opentofu", "ansible"} {
		for _, v := range []string{"0.1.0", "0.2.0"} {
			id := name + "-" + map[string]string{"0.1.0": "old", "0.2.0": "new"}[v]
			configs[id] = wire.Executable{Path: bin, Args: []string{"driver", name, "--driver-version", v, "--tool", tool}}
		}
	}
	m, e := wire.New(t.Context(), configs, os.Environ())
	if e != nil {
		t.Fatal(e)
	}
	list, e := m.List(t.Context())
	if e != nil || len(list) != 4 {
		t.Fatal(list, e)
	}
	for _, entry := range list {
		if entry.Status != wire.Connected {
			t.Fatal(entry)
		}
	}
	config := wire.Config{"id": "opentofu-old", "required_driver_version": "0.1.0", "backend": "local", "node_output": "inframill_nodes", "artifact": "opentofu-old-nodes-v1.json"}
	if e = m.Validate(t.Context(), wire.ValidationRequest{Drivers: map[string]wire.Config{"opentofu-new": config}}); e == nil {
		t.Fatal("remapped driver alias bypassed exact version")
	}
	p, e := m.Prepare(t.Context(), wire.CommandRequest{DriverID: "opentofu-old", Command: "plan", Scope: wire.ExecutionContext{WorkspaceDirectory: work, ArtifactDirectory: t.TempDir(), DriverConfig: config, Binding: wire.Binding{InstanceID: "11111111-1111-4111-8111-111111111111"}, CommandID: "22222222-2222-4222-8222-222222222222"}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	if _, e = p.Execute(ctx); e == nil {
		t.Fatal("cancelled plan succeeded")
	}
	time.Sleep(500 * time.Millisecond)
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("descendant escaped Inframill process group cancellation")
	}
}
