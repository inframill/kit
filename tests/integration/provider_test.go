package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/inframill/kit/pkg/runmill"
)

func build(t *testing.T, fixtures bool) string {
	t.Helper()
	root, _ := filepath.Abs("../..")
	bin := filepath.Join(t.TempDir(), "kit")
	args := []string{"build", "-o", bin}
	if fixtures {
		args = append(args, "-tags=testproviders")
	}
	cmd := exec.Command("go", append(args, "./cmd")...)
	cmd.Dir = root
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", b, e)
	}
	return bin
}

func TestVersionedProviderEntrypoints(t *testing.T) {
	binary := build(t, false)
	for name, version := range map[string]string{"opentofu": "0.4.0", "ansible": "0.4.0", "dispatch": "0.1.0"} {
		t.Run(name, func(t *testing.T) {
			selector := name + "-" + version
			output, err := exec.Command(binary, selector, "version").Output()
			var metadata struct{ Name, Version string }
			if err != nil || json.Unmarshal(output, &metadata) != nil || metadata.Name != name || metadata.Version != version {
				t.Fatalf("version metadata: %s: %v", output, err)
			}
			cmd := exec.Command(binary, selector)
			cmd.Stdin = bytes.NewBufferString(`{"protocol":1,"method":"info","params":{}}`)
			output, err = cmd.Output()
			var reply runmill.Reply
			if err != nil || json.Unmarshal(output, &reply) != nil || reply.Type != "success" {
				t.Fatalf("discovery: %s: %v", output, err)
			}
			var info runmill.ProviderInfo
			if err := json.Unmarshal(reply.Data, &info); err != nil || info.Name != name || len(info.Commands) == 0 {
				t.Fatalf("provider metadata: %s: %v", reply.Data, err)
			}
			if bytes.Contains(reply.Data, []byte("argumentsSchema")) {
				t.Fatal("Provider advertised Blueprint-owned argument schemas")
			}
			host := runmill.NewHost(os.Environ())
			if err := host.Load(t.Context(), map[string]runmill.Executable{name: {Path: binary, Args: []string{selector}}}); err != nil {
				t.Fatal(err)
			}
			entries, err := host.List(t.Context())
			if err != nil || len(entries) != 1 || entries[0].Availability != runmill.Available {
				t.Fatal(entries, err)
			}
		})
	}
	output, err := exec.Command(binary, "list").Output()
	var releases []struct{ Name, Version string }
	if err != nil || json.Unmarshal(output, &releases) != nil || len(releases) != 3 || bytes.Contains(output, []byte("fixture")) {
		t.Fatalf("production registry: %s: %v", output, err)
	}
	for _, args := range [][]string{nil, {"ansible"}, {"ansible-latest"}, {"opentofu-0.3.1"}, {"fixture-0.1.0"}, {"ansible-0.4.0", "--unknown"}} {
		cmd := exec.Command(binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err == nil || len(output) != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid invocation %v: stdout=%s stderr=%s error=%v", args, output, &stderr, err)
		}
	}
}

func TestHostVersionsRepliesAndCancellation(t *testing.T) {
	bin := build(t, true)
	configs := map[string]runmill.Executable{}
	for _, version := range []string{"0.1.0", "0.2.0"} {
		configs[map[string]string{"0.1.0": "first", "0.2.0": "second"}[version]] = runmill.Executable{Path: bin, Args: []string{"fixture-" + version}}
	}
	tool := filepath.Join(t.TempDir(), "tofu")
	if e := os.WriteFile(tool, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '{\"terraform_version\":\"1.11.5\"}'; exit; fi\n(sleep 0.4; touch escaped-child) &\nwait\n"), 0700); e != nil {
		t.Fatal(e)
	}
	configs["opentofu"] = runmill.Executable{Path: bin, Args: []string{"opentofu-0.4.0", "--tool", tool}}
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
	settings := runmill.Settings{"contract": "0.1.0"}
	if m.ValidateSettings(t.Context(), "second", settings) == nil {
		t.Fatal("wrong manifest contract accepted")
	}
	work := filepath.Join(t.TempDir(), "opentofu")
	os.Mkdir(work, 0700)
	os.WriteFile(filepath.Join(work, "kit-provider.json"), []byte(`{"schema":1,"backend_config":{},"environment":{}}`), 0600)
	scope := runmill.ExecutionContext{WorkspaceDir: work, ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Settings: runmill.Settings{"id": "opentofu", "backend": "local", "node_output": "nodes", "settings_file": "kit-provider.json", "artifact": "opentofu-nodes-v1.json"}}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, e = m.Execute(ctx, runmill.Execution{ProviderID: "opentofu", Command: "plan", Context: scope}, nil); e == nil {
		t.Fatal("cancellation succeeded")
	}
	time.Sleep(500 * time.Millisecond)
	if _, e = os.Stat(filepath.Join(work, "escaped-child")); !os.IsNotExist(e) {
		t.Fatal("child escaped process group")
	}
	var replies bytes.Buffer
	scope.Replies = &replies
	scope.Settings = runmill.Settings{"contract": "0.1.0"}
	if _, e = m.Execute(t.Context(), runmill.Execution{ProviderID: "first", Command: "probe", Context: scope, Arguments: map[string]any{"text": "ok"}}, map[string]string{"token": "synthetic-value"}); e != nil {
		t.Fatal(e)
	}
	scope.Settings = runmill.Settings{"contract": "0.2.0"}
	if _, e = m.Execute(t.Context(), runmill.Execution{ProviderID: "second", Command: "probe", Context: scope, Arguments: map[string]any{"text": "wrong"}}, nil); e == nil {
		t.Fatal("Provider did not defensively reject an incompatible argument contract")
	}
	if _, e = m.Execute(t.Context(), runmill.Execution{ProviderID: "second", Command: "probe", Context: scope, Arguments: map[string]any{"message": "right"}}, nil); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(replies.Bytes(), []byte(`"type":"message"`)) || !bytes.Contains(replies.Bytes(), []byte(`"type":"success"`)) || bytes.Contains(replies.Bytes(), []byte("synthetic-value")) {
		t.Fatal(replies.String())
	}
}
func TestExecutableUsesOnlyJSONDispatch(t *testing.T) {
	bin := build(t, true)
	for _, extra := range [][]string{nil, {"info"}} {
		cmd := exec.Command(bin, append([]string{"opentofu-0.4.0"}, extra...)...)
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
		{`{"protocol":1,"method":"validate_command","params":{}}`, false},
	} {
		cmd := exec.Command(bin, "fixture-0.1.0")
		cmd.Stdin = bytes.NewBufferString(tc.request)
		output, err := cmd.Output()
		var reply runmill.Reply
		if json.Unmarshal(output, &reply) != nil || (tc.ok && (err != nil || reply.Type != "success")) || (!tc.ok && (err == nil || reply.Type != "error")) {
			t.Fatalf("request %s: %s, %v", tc.request, output, err)
		}
	}
}
