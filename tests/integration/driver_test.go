package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	driver "github.com/sir-hassan/inframill/pkg/driver"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func build(t *testing.T) string {
	t.Helper()
	root, _ := filepath.Abs("../..")
	bin := filepath.Join(t.TempDir(), "kit")
	cmd := exec.Command("go", "build", "-tags=kit_testdrivers", "-o", bin, "./cmd/inframill-kit")
	cmd.Dir = root
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", b, e)
	}
	return bin
}
func TestManagerVersionsRepliesAndCancellation(t *testing.T) {
	bin := build(t)
	configs := driver.Configurations{}
	for _, version := range []string{"0.1.0", "0.2.0"} {
		configs[map[string]string{"0.1.0": "first", "0.2.0": "second"}[version]] = driver.Executable{Path: bin, Args: []string{"driver", "fixture", "--driver-version", version}}
	}
	tool := filepath.Join(t.TempDir(), "tofu")
	if e := os.WriteFile(tool, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '{\"terraform_version\":\"1.11.5\"}'; exit; fi\n(sleep 0.4; touch escaped-child) &\nwait\n"), 0700); e != nil {
		t.Fatal(e)
	}
	configs["opentofu"] = driver.Executable{Path: bin, Args: []string{"driver", "opentofu", "--driver-version", "0.1.0", "--tool", tool}}
	m := driver.New(os.Environ())
	if e := m.Load(t.Context(), configs); e != nil {
		t.Fatal(e)
	}
	list, e := m.List(t.Context())
	if e != nil || len(list) != 3 {
		t.Fatal(list, e)
	}
	for _, entry := range list {
		if entry.Status != driver.Connected {
			t.Fatal(entry)
		}
	}
	if m.ValidateCommandArgs(t.Context(), "second", "probe", map[string]any{"text": "wrong"}) == nil {
		t.Fatal("version schema was aliased")
	}
	if e = m.ValidateCommandArgs(t.Context(), "second", "probe", map[string]any{"message": "right"}); e != nil {
		t.Fatal(e)
	}
	manifest := driver.BlueprintManifest{APIVersion: "inframill.dev/blueprint/v1", Description: "test", Drivers: map[string]driver.Config{"second": {"contract": "0.1.0"}}}
	if m.ValidateBlueprint(t.Context(), manifest) == nil {
		t.Fatal("wrong manifest contract accepted")
	}
	work := filepath.Join(t.TempDir(), "opentofu")
	os.Mkdir(work, 0700)
	os.WriteFile(filepath.Join(work, "kit-driver.json"), []byte(`{"schema":1,"backend_config":{},"environment":{}}`), 0600)
	scope := driver.ExecutionContext{WorkspaceDirectory: work, ArtifactDirectory: t.TempDir(), CommandID: "22222222-2222-4222-8222-222222222222", Binding: driver.Binding{InstanceID: "11111111-1111-4111-8111-111111111111", Name: "test/example", BlueprintReference: "local@example"}, DriverConfig: driver.Config{"id": "opentofu", "required_driver_version": "0.1.0", "backend": "local", "node_output": "nodes", "settings_file": "kit-driver.json", "artifact": "opentofu-nodes-v1.json"}}
	p, e := m.Prepare(t.Context(), driver.CommandRequest{DriverID: "opentofu", Command: "plan", Scope: scope})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, e = p.Execute(ctx, driver.Secrets{}); e == nil {
		t.Fatal("cancellation succeeded")
	}
	time.Sleep(500 * time.Millisecond)
	if _, e = os.Stat(filepath.Join(work, "escaped-child")); !os.IsNotExist(e) {
		t.Fatal("child escaped process group")
	}
	var replies bytes.Buffer
	scope.Replies = &replies
	scope.DriverConfig = driver.Config{"contract": "0.1.0"}
	p, e = m.Prepare(t.Context(), driver.CommandRequest{DriverID: "first", Command: "probe", Scope: scope, Arguments: map[string]any{"text": "ok"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execute(t.Context(), driver.Secrets{"token": "synthetic-value"}); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(replies.Bytes(), []byte(`"type":"message"`)) || !bytes.Contains(replies.Bytes(), []byte(`"type":"success"`)) || bytes.Contains(replies.Bytes(), []byte("synthetic-value")) {
		t.Fatal(replies.String())
	}
}
func TestExecutableUsesOnlyJSONDispatch(t *testing.T) {
	bin := build(t)
	for _, extra := range [][]string{nil, {"info"}} {
		cmd := exec.Command(bin, append([]string{"driver", "opentofu", "--driver-version", "0.1.0"}, extra...)...)
		cmd.Stdin = bytes.NewBufferString(`{"protocol":1,"method":"info","params":{}}`)
		b, e := cmd.Output()
		var reply driver.Reply
		if json.Unmarshal(b, &reply) != nil {
			t.Fatal(string(b))
		}
		if len(extra) == 0 && (e != nil || reply.Type != "success") {
			t.Fatal(string(b), e)
		}
		if len(extra) > 0 && (e == nil || reply.Type != "error") {
			t.Fatal("accepted protocol argv")
		}
	}
}

// Test the consumer boundary too: no raw or contradictory reply is a success.
func TestManagerRejectsInvalidFinalReplies(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		exit        int
	}{
		{"missing", `{"type":"message","message":"working"}`, 0},
		{"trailing", `{"type":"success","data":{}}
{"type":"error","code":"LATE","message":"late failure"}`, 0},
		{"nonzero", `{"type":"success","data":{}}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "driver")
			script := `#!/bin/sh
read request
case "$request" in
*'"method":"info"'*) printf '%s\n' '{"type":"success","data":{"name":"broken","version":"1","protocol":1,"commands":{"probe":{"description":"probe","argumentsSchema":{"type":"object"}}}}}';;
*'"method":"run"'*) printf '%s\n' '` + tc.reply + `'; exit ` + fmt.Sprint(tc.exit) + `;;
*) printf '%s\n' '{"type":"success"}';;
esac
`
			if e := os.WriteFile(path, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			manager := driver.New(os.Environ())
			if e := manager.Load(t.Context(), driver.Configurations{"broken": {Path: path}}); e != nil {
				t.Fatal(e)
			}
			scope := driver.ExecutionContext{DriverConfig: driver.Config{}, WorkspaceDirectory: t.TempDir(), ArtifactDirectory: t.TempDir(), CommandID: "22222222-2222-4222-8222-222222222222", Binding: driver.Binding{InstanceID: "11111111-1111-4111-8111-111111111111", Name: "test/broken", BlueprintReference: "local@sha256:fixture"}}
			prepared, e := manager.Prepare(t.Context(), driver.CommandRequest{DriverID: "broken", Command: "probe", Scope: scope})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = prepared.Execute(t.Context(), nil); e == nil {
				t.Fatal("invalid final reply succeeded")
			}
		})
	}
}
