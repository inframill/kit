package driver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func listEntries(t *testing.T, manager Manager) []Entry {
	t.Helper()
	entries, err := manager.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// SPEC-0006 REQ-002/003: configuration identity, complete health and independent snapshots.
func TestREQ003ConfiguredHealth(t *testing.T) {
	good := executableFixture(t, printInfo(fixtureInfo), "cat")
	nonexec := executableFixture(t, printInfo(fixtureInfo), "cat")
	if err := os.Chmod(nonexec, 0o600); err != nil {
		t.Fatal(err)
	}
	configs := Configurations{
		"a-alias": {Path: good}, "b-alias": {Path: good},
		"empty": {}, "missing": {Path: filepath.Join(t.TempDir(), "missing")},
		"directory": {Path: t.TempDir()}, "permissions": {Path: nonexec},
		"nul-path": {Path: "\x00"}, "nul-args": {Path: good, Args: []string{"\x00"}},
		"failure": {Path: executableFixture(t, "printf 'probe rejected' >&2; exit 7", "cat")},
	}
	for name, document := range map[string]string{
		"malformed": "{", "protocol": strings.Replace(fixtureInfo, `"protocol":1`, `"protocol":9`, 1),
		"version":      strings.Replace(fixtureInfo, `"version":"1.2.3"`, `"version":""`, 1),
		"reserved":     strings.Replace(fixtureInfo, `"name":"custom"`, `"name":"builtin"`, 1),
		"safety":       strings.Replace(fixtureInfo, "read_only", "unknown", 1),
		"description":  strings.Replace(fixtureInfo, "fixture", "", 1),
		"verification": strings.Replace(fixtureInfo, "fixture exits successfully", "", 1),
		"trailing":     fixtureInfo + " {}",
	} {
		configs[name] = Executable{Path: executableFixture(t, printInfo(document), "cat")}
	}
	manager, err := New(t.Context(), configs, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	entries := listEntries(t, manager)
	if len(entries) != len(configs) {
		t.Fatal(entries)
	}
	for index, entry := range entries {
		if entry.CheckedAt.IsZero() || (index > 0 && entries[index-1].ID >= entry.ID) {
			t.Fatal("unsorted or unchecked", entries)
		}
		if index < 2 {
			if entry.Status != Connected || entry.Info == nil || entry.Info.Name != "custom" || entry.Info.Version != "1.2.3" || entry.Reason != "" {
				t.Fatal(entry)
			}
		} else if entry.Status != Offline || entry.Info != nil || entry.Reason == "" {
			t.Fatal("invalid executable not retained offline", entry)
		}
	}
	delete(configs, "a-alias")
	entries[0].Info.Name = "corrupt"
	op := entries[0].Info.Commands["echo"]
	op.Effects[0] = "corrupt"
	op.ArgumentsSchema["type"] = "corrupt"
	again := listEntries(t, manager)
	if again[0].Info.Name != "custom" || again[0].Info.Commands["echo"].Effects[0] == "corrupt" || again[0].Info.Commands["echo"].ArgumentsSchema["type"] != "object" {
		t.Fatal("List shares mutable state", again)
	}
	// An unrelated offline entry does not block an alias selecting this executable.
	if _, err := manager.Prepare(t.Context(), CommandRequest{DriverID: "a-alias", Command: "echo", Scope: testScope(t)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "builtin", "Bad", "a.b", "../driver"} {
		if _, err := New(t.Context(), Configurations{id: {Path: good}}, nil); err == nil {
			t.Fatal("invalid configured ID accepted", id)
		}
	}
	empty, err := New(t.Context(), nil, nil)
	if err != nil || len(listEntries(t, empty)) != 0 {
		t.Fatal(empty, err)
	}
}

// SPEC-0006 REQ-003/005: use refreshes health, List does no I/O, and preparation copies input.
func TestREQ005RecoveryRoutingAndIsolation(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(t.Context(), Configurations{"selected": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	initial := listEntries(t, manager)[0]
	if initial.Status != Offline {
		t.Fatal(initial)
	}
	request := CommandRequest{DriverID: "selected", Command: "echo", Scope: testScope(t), Arguments: map[string]any{"message": []any{"original"}}}
	if _, err := manager.Prepare(t.Context(), request); err == nil {
		t.Fatal("offline Driver prepared")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if listEntries(t, manager)[0].Status != Offline {
		t.Fatal("List unexpectedly probed")
	}
	prepared, err := manager.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	current := listEntries(t, manager)[0]
	if current.Status != Connected || !current.CheckedAt.After(initial.CheckedAt) {
		t.Fatal(current)
	}
	request.Scope.Values["value"] = "changed"
	request.Arguments["message"].([]any)[0] = "changed"
	result, err := prepared.Execute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	object := result.(map[string]any)
	if object["command"] != "echo" || object["values"].(map[string]any)["value"] != "fixture" || object["arguments"].(map[string]any)["message"].([]any)[0] != "original" {
		t.Fatal("routing or snapshot failed", object)
	}
	request.DriverID = "custom" // Self-reported name is not the configured ID.
	if _, err := manager.Prepare(t.Context(), request); err == nil {
		t.Fatal("name bypassed configured ID")
	}
	request.DriverID, request.Command = "selected", "selected.echo"
	if _, err := manager.Prepare(t.Context(), request); err == nil {
		t.Fatal("qualified local command accepted")
	}
}

// SPEC-0006 REQ-004: one API validates config, source and Values in order; no run effects.
func TestREQ004BlueprintValidation(t *testing.T) {
	for _, reject := range []string{"", "config", "files", "values"} {
		t.Run(reject, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "validation.jsonl")
			validate := `input=$(cat); printf '%s\n' "$input" >> '` + log + `'; phase=config; case "$input" in *blueprintDirectory*) phase=files;; *binding*) phase=values;; esac; if [ "$phase" = '` + reject + `' ]; then printf 'rejected %s' "$phase" >&2; exit 8; fi; printf '{"valid":true}'`
			path := validatedFixture(t, printInfo(fixtureInfo), validate, "exit 9")
			manager, err := New(t.Context(), Configurations{"alias": {Path: path}}, os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			scope := testScope(t)
			request := ValidationRequest{Drivers: map[string]Config{"alias": {"setting": "expected"}}, BlueprintDirectory: scope.WorkspaceDirectory, Binding: &scope.Binding, Values: scope.Values}
			err = manager.Validate(t.Context(), request)
			if (err != nil) != (reject != "") {
				t.Fatal(err)
			}
			if err != nil && (!strings.Contains(err.Error(), "drivers.alias") || !strings.Contains(err.Error(), reject)) {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			count := map[string]int{"": 3, "config": 1, "files": 2, "values": 3}[reject]
			if len(lines) != count {
				t.Fatal(string(data))
			}
			for index, line := range lines {
				var wire Request
				if err := json.Unmarshal([]byte(line), &wire); err != nil {
					t.Fatal(err)
				}
				if wire.Config["setting"] != "expected" || wire.Protocol != Protocol || wire.Command != "" || (wire.BlueprintDirectory != "") != (index == 1) || (wire.Binding != nil) != (index == 2) {
					t.Fatal(wire)
				}
			}
			if listEntries(t, manager)[0].Status != Connected {
				t.Fatal("validation rejection changed connection health")
			}
			request.Drivers = map[string]Config{"unknown": {}}
			if err := manager.Validate(t.Context(), request); err == nil || !strings.Contains(err.Error(), "drivers.unknown") {
				t.Fatal(err)
			}
			request.Drivers = map[string]Config{"alias": nil}
			if err := manager.Validate(t.Context(), request); err == nil {
				t.Fatal("null config accepted")
			}
		})
	}
}

// SPEC-0006 REQ-003/004: concurrent callers and cancellation are supported.
func TestREQ003ConcurrentHealthAndCancellation(t *testing.T) {
	path := validatedFixture(t, printInfo(fixtureInfo), "cat >/dev/null; sleep 60", "cat")
	manager, err := New(t.Context(), Configurations{"alias": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := manager.Validate(ctx, ValidationRequest{Drivers: map[string]Config{"alias": {}}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("validation ignored cancellation")
	}
	if _, err := manager.List(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(ctx, CommandRequest{DriverID: "alias", Command: "echo"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	healthy := executableFixture(t, printInfo(fixtureInfo), "cat")
	manager, err = New(t.Context(), Configurations{"alias": {Path: healthy}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			if _, err := manager.Prepare(t.Context(), CommandRequest{DriverID: "alias", Command: "echo", Scope: ExecutionContext{DriverConfig: Config{}}}); err != nil {
				t.Error(err)
			}
			entries, err := manager.List(t.Context())
			if err != nil || len(entries) != 1 || entries[0].Status != Connected {
				t.Error(entries, err)
			}
			if err == nil && len(entries) == 1 {
				entries[0].Info.Commands["echo"].Effects[0] = "private"
			}
		})
	}
	workers.Wait()
	if !reflect.DeepEqual(listEntries(t, manager)[0].Info.Commands["echo"].Effects, []string{"reads fixture"}) {
		t.Fatal("shared metadata mutation")
	}
}

type panicCommand struct{}

func (panicCommand) Execute(context.Context) (any, error) { panic("private panic value") }
func TestPreparedCommandContainsPanic(t *testing.T) {
	value, err := (safeCommand{panicCommand{}}).Execute(t.Context())
	if value != nil || err == nil || err.Error() != "command implementation panicked" {
		t.Fatal(value, err)
	}
}
