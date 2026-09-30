package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/sir-hassan/inframill/pkg/runmill"
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
	cmd := exec.Command("go", "build", "-o", bin, "./tests/harness")
	cmd.Dir = root
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", b, e)
	}
	return bin
}

func TestVersionedProviderEntrypoints(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opentofu", "ansible"} {
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "provider")
			cmd := exec.Command("go", "build", "-o", binary, "./roots/"+name+"/0.3.0/cmd")
			cmd.Dir = root
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %s: %v", output, err)
			}
			output, err := exec.Command(binary, "version").Output()
			var metadata struct{ Name, Version string }
			if err != nil || json.Unmarshal(output, &metadata) != nil || metadata.Name != name || metadata.Version != "0.3.0" {
				t.Fatalf("version metadata: %s: %v", output, err)
			}
			cmd = exec.Command(binary)
			cmd.Stdin = bytes.NewBufferString(`{"protocol":1,"method":"info","params":{}}`)
			output, err = cmd.Output()
			var reply runmill.Reply
			if err != nil || json.Unmarshal(output, &reply) != nil || reply.Type != "success" {
				t.Fatalf("standalone discovery: %s: %v", output, err)
			}
		})
	}
}
func TestHostVersionsRepliesAndCancellation(t *testing.T) {
	bin := build(t)
	configs := map[string]runmill.Executable{}
	for _, version := range []string{"0.1.0", "0.2.0"} {
		configs[map[string]string{"0.1.0": "first", "0.2.0": "second"}[version]] = runmill.Executable{Path: bin, Args: []string{"provider", "fixture", "--provider-version", version}}
	}
	tool := filepath.Join(t.TempDir(), "tofu")
	if e := os.WriteFile(tool, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '{\"terraform_version\":\"1.11.5\"}'; exit; fi\n(sleep 0.4; touch escaped-child) &\nwait\n"), 0700); e != nil {
		t.Fatal(e)
	}
	configs["opentofu"] = runmill.Executable{Path: bin, Args: []string{"provider", "opentofu", "--provider-version", "0.3.0", "--tool", tool}}
	m := runmill.NewHost(os.Environ())
	if e := m.Load(t.Context(), configs); e != nil {
		t.Fatal(e)
	}
	list, e := m.List(t.Context())
	if e != nil || len(list) != 3 {
		t.Fatal(list, e)
	}
	for _, entry := range list {
		if entry.Availability != runmill.Available {
			t.Fatal(entry)
		}
	}
	if m.ValidateArguments(t.Context(), "second", "probe", map[string]any{"text": "wrong"}) == nil {
		t.Fatal("version schema was aliased")
	}
	if e = m.ValidateArguments(t.Context(), "second", "probe", map[string]any{"message": "right"}); e != nil {
		t.Fatal(e)
	}
	settings := runmill.Settings{"contract": "0.1.0"}
	if m.ValidateSettings(t.Context(), "second", settings) == nil {
		t.Fatal("wrong manifest contract accepted")
	}
	work := filepath.Join(t.TempDir(), "opentofu")
	os.Mkdir(work, 0700)
	os.WriteFile(filepath.Join(work, "kit-provider.json"), []byte(`{"schema":1,"backend_config":{},"environment":{}}`), 0600)
	scope := runmill.ExecutionContext{WorkspaceDir: work, ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Settings: runmill.Settings{"id": "opentofu", "backend": "local", "node_output": "nodes", "settings_file": "kit-provider.json", "artifact": "opentofu-nodes-v1.json"}}
	p, e := m.Prepare(t.Context(), runmill.Execution{ProviderID: "opentofu", Command: "plan", Context: scope})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, e = p.Execute(ctx, map[string]string{}); e == nil {
		t.Fatal("cancellation succeeded")
	}
	time.Sleep(500 * time.Millisecond)
	if _, e = os.Stat(filepath.Join(work, "escaped-child")); !os.IsNotExist(e) {
		t.Fatal("child escaped process group")
	}
	var replies bytes.Buffer
	scope.Replies = &replies
	scope.Settings = runmill.Settings{"contract": "0.1.0"}
	p, e = m.Prepare(t.Context(), runmill.Execution{ProviderID: "first", Command: "probe", Context: scope, Arguments: map[string]any{"text": "ok"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execute(t.Context(), map[string]string{"token": "synthetic-value"}); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(replies.Bytes(), []byte(`"type":"message"`)) || !bytes.Contains(replies.Bytes(), []byte(`"type":"success"`)) || bytes.Contains(replies.Bytes(), []byte("synthetic-value")) {
		t.Fatal(replies.String())
	}
}
func TestExecutableUsesOnlyJSONDispatch(t *testing.T) {
	bin := build(t)
	for _, extra := range [][]string{nil, {"info"}} {
		cmd := exec.Command(bin, append([]string{"provider", "opentofu", "--provider-version", "0.3.0"}, extra...)...)
		cmd.Stdin = bytes.NewBufferString(`{"protocol":1,"method":"info","params":{}}`)
		b, e := cmd.Output()
		var reply runmill.Reply
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
	for _, tc := range []struct {
		request string
		ok      bool
	}{
		{`{"protocol":1,"method":"validate_settings","params":{"settings":{"contract":"0.1.0"}}}`, true},
		{`{"protocol":1,"method":"validate_settings","params":{"config":{"contract":"0.1.0"}}}`, false},
		{`{"protocol":1,"method":"validate_manifest","params":{"settings":{"contract":"0.1.0"}}}`, false},
	} {
		cmd := exec.Command(bin, "provider", "fixture", "--provider-version", "0.1.0")
		cmd.Stdin = bytes.NewBufferString(tc.request)
		output, err := cmd.Output()
		var reply runmill.Reply
		if json.Unmarshal(output, &reply) != nil || (tc.ok && (err != nil || reply.Type != "success")) || (!tc.ok && (err == nil || reply.Type != "error")) {
			t.Fatalf("request %s: %s, %v", tc.request, output, err)
		}
	}
}

// Test the consumer boundary too: no raw or contradictory reply is a success.
func TestHostRejectsInvalidFinalReplies(t *testing.T) {
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
			path := filepath.Join(t.TempDir(), "provider")
			script := `#!/bin/sh
read request
case "$request" in
*'"method":"info"'*) printf '%s\n' '{"type":"success","data":{"name":"broken","version":"1.0.0","protocolVersion":1,"commands":{"probe":{"description":"probe","argumentsSchema":{"type":"object"}}}}}';;
*'"method":"run"'*) printf '%s\n' '` + tc.reply + `'; exit ` + fmt.Sprint(tc.exit) + `;;
*) printf '%s\n' '{"type":"success"}';;
esac
`
			if e := os.WriteFile(path, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			manager := runmill.NewHost(os.Environ())
			if e := manager.Load(t.Context(), map[string]runmill.Executable{"broken": {Path: path}}); e != nil {
				t.Fatal(e)
			}
			scope := runmill.ExecutionContext{Settings: runmill.Settings{}, WorkspaceDir: t.TempDir(), ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
			prepared, e := manager.Prepare(t.Context(), runmill.Execution{ProviderID: "broken", Command: "probe", Context: scope})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = prepared.Execute(t.Context(), nil); e == nil {
				t.Fatal("invalid final reply succeeded")
			}
		})
	}
}
