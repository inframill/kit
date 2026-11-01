package runmill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func listEntries(t *testing.T, host Host) []ProviderSnapshot {
	t.Helper()
	entries, err := host.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// SPEC-0000 REQ-008: configuration identity, complete health and independent snapshots.
func TestConfiguredHealth(t *testing.T) {
	good := executableFixture(t, printInfo(fixtureInfo), "cat")
	nonexec := executableFixture(t, printInfo(fixtureInfo), "cat")
	if err := os.Chmod(nonexec, 0o600); err != nil {
		t.Fatal(err)
	}
	configs := map[string]Executable{
		"a-alias": {Path: good}, "b-alias": {Path: good},
		"empty": {}, "missing": {Path: filepath.Join(t.TempDir(), "missing")},
		"directory": {Path: t.TempDir()}, "permissions": {Path: nonexec},
		"nul-path": {Path: "\x00"}, "nul-args": {Path: good, Args: []string{"\x00"}},
		"failure": {Path: executableFixture(t, "printf 'probe rejected' >&2; exit 7", "cat")},
	}
	for name, document := range map[string]string{
		"malformed": "{", "protocol": strings.Replace(fixtureInfo, `"protocolVersion":1`, `"protocolVersion":9`, 1),
		"version":      strings.Replace(fixtureInfo, `"version":"1.2.3"`, `"version":""`, 1),
		"invalid-name": strings.Replace(fixtureInfo, `"name":"custom"`, `"name":"INVALID_NAME"`, 1),
		"description":  strings.Replace(fixtureInfo, "fixture", "", 1),
		"trailing":     fixtureInfo + " {}",
	} {
		configs[name] = Executable{Path: executableFixture(t, printInfo(document), "cat")}
	}
	host, err := newLoadedHost(t.Context(), configs, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	entries := listEntries(t, host)
	if len(entries) != len(configs) {
		t.Fatal(entries)
	}
	for index, entry := range entries {
		if entry.CheckedAt.IsZero() || (index > 0 && entries[index-1].ID >= entry.ID) {
			t.Fatal("unsorted or unchecked", entries)
		}
		if index < 2 {
			if entry.Availability != Available || entry.Info == nil || entry.Info.Name != "custom" || entry.Info.Version != "1.2.3" || entry.Reason != "" {
				t.Fatal(entry)
			}
		} else if entry.Availability != Unavailable || entry.Info != nil || entry.Reason == "" {
			t.Fatal("invalid executable not retained offline", entry)
		}
	}
	delete(configs, "a-alias")
	entries[0].Info.Name = "corrupt"
	op := entries[0].Info.Commands["echo"]
	op.Description = "corrupt"
	entries[0].Info.Commands["echo"] = op
	again := listEntries(t, host)
	if again[0].Info.Name != "custom" || again[0].Info.Commands["echo"].Description != "fixture" {
		t.Fatal("List shares mutable state", again)
	}
	// An unrelated offline entry does not block an alias selecting this executable.
	if _, err := host.Execute(t.Context(), Execution{ProviderID: "a-alias", Command: "echo", Context: testScope(t)}, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "Bad_ID", "a.b", "../provider"} {
		if _, err := newLoadedHost(t.Context(), map[string]Executable{id: {Path: good}}, nil); err == nil {
			t.Fatal("invalid configured ID accepted", id)
		}
	}
	empty, err := newLoadedHost(t.Context(), nil, nil)
	if err != nil || len(listEntries(t, empty)) != 0 {
		t.Fatal(empty, err)
	}
}

// SPEC-0000 REQ-008: recovery requires a new Host; execution copies input.
func TestStartupRecoveryRoutingAndIsolation(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := newLoadedHost(t.Context(), map[string]Executable{"selected": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	initial := listEntries(t, host)[0]
	if initial.Availability != Unavailable {
		t.Fatal(initial)
	}
	request := Execution{ProviderID: "selected", Command: "echo", Context: testScope(t), Arguments: map[string]any{"message": []any{"original"}}}
	if _, err := host.Execute(t.Context(), request, nil); err == nil {
		t.Fatal("offline Provider prepared")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if listEntries(t, host)[0].Availability != Unavailable {
		t.Fatal("List unexpectedly probed")
	}
	if _, err := host.Execute(t.Context(), request, nil); err == nil {
		t.Fatal("repaired Provider recovered without startup load")
	}
	host, err = newLoadedHost(t.Context(), map[string]Executable{"selected": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}

	current := listEntries(t, host)[0]
	if current.Availability != Available || !current.CheckedAt.After(initial.CheckedAt) {
		t.Fatal(current)
	}
	result, err := host.Execute(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Context.Settings["value"] = "changed"
	requireType[[]any](t, request.Arguments["message"])[0] = "changed"
	object := requireType[map[string]any](t, result)
	if object["values"] != nil || object["command"] != "echo" || requireType[map[string]any](t, object["settings"])["value"] != "fixture" || requireType[[]any](t, requireType[map[string]any](t, object["arguments"])["message"])[0] != "original" {
		t.Fatal("routing or snapshot failed", object)
	}
	request.ProviderID = "custom" // Self-reported name is not the configured ID.
	if _, err := host.Execute(t.Context(), request, nil); err == nil {
		t.Fatal("name bypassed configured ID")
	}
	request.ProviderID, request.Command = "selected", "selected.echo"
	if _, err := host.Execute(t.Context(), request, nil); err == nil {
		t.Fatal("qualified local command accepted")
	}
}

// SPEC-0000 REQ-008/009/025: one settings call invokes only the selected Provider.
func TestSettingsValidation(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "validation.jsonl")
			validate := `cat > '` + log + `'; printf '{"valid":true}'`
			if reject {
				validate = `cat > '` + log + `'; printf 'rejected' >&2; exit 8`
			}
			path := validatedFixture(t, printInfo(fixtureInfo), validate, "exit 9")
			host, err := newLoadedHost(t.Context(), map[string]Executable{"selected": {Path: path}, "unused": {Path: "/missing"}}, os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			err = host.ValidateSettings(t.Context(), "selected", Settings{"owner": "chosen"})
			if (err != nil) != reject {
				t.Fatal(err)
			}
			if err != nil && !strings.Contains(err.Error(), "providers.selected") {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wire, map[string]any{"settings": map[string]any{"owner": "chosen"}}) {
				t.Fatal(wire)
			}
			if listEntries(t, host)[0].Availability != Available {
				t.Fatal("validation changed availability")
			}
		})
	}
}

// SPEC-0000 REQ-008/009/025: invalid inputs fail before settings callbacks.
func TestSettingsRejections(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "validated")
	path := validatedFixture(t, printInfo(fixtureInfo), "touch '"+marker+"'; exit 9", "exit 9")
	host, err := newLoadedHost(t.Context(), map[string]Executable{"alias": {Path: path}, "unavailable": {Path: filepath.Join(t.TempDir(), "absent")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		settings Settings
		want     string
	}{
		{"null", "alias", nil, "settings"},
		{"non-json", "alias", Settings{"bad": make(chan int)}, "JSON"},
		{"invalid-id", "BAD_ID", Settings{}, "providers.BAD_ID"},
		{"unknown", "unknown", Settings{}, "providers.unknown"},
		{"unavailable", "unavailable", Settings{}, "providers.unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := host.ValidateSettings(t.Context(), tc.id, tc.settings); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("invalid settings invoked Provider", err)
			}
		})
	}
}

// SPEC-0000 REQ-008/009/025: host-reserved words and non-UUID IDs are valid here.
func TestStandaloneBuiltin(t *testing.T) {
	info := strings.Replace(fixtureInfo, `"name":"custom"`, `"name":"builtin"`, 1)
	path := executableFixture(t, printInfo(info), "cat")
	host, err := newLoadedHost(t.Context(), map[string]Executable{"builtin": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	entry := listEntries(t, host)[0]
	if entry.Availability != Available || entry.Info.Name != "builtin" {
		t.Fatal(entry)
	}
	if err := host.ValidateSettings(t.Context(), "builtin", Settings{}); err != nil {
		t.Fatal(err)
	}
	scope := testScope(t)
	scope.ExecutionID = "queue/job:42"
	execution := Execution{ProviderID: "builtin", Command: "echo", Context: scope}

	value, err := host.Execute(t.Context(), execution, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if requireType[map[string]any](t, value)["executionId"] != scope.ExecutionID {
		t.Fatal(value)
	}
}

// SPEC-0000 REQ-008: concurrent callers and cancellation are supported.
func TestConcurrentHealthAndCancellation(t *testing.T) {
	path := validatedFixture(t, printInfo(fixtureInfo), "cat >/dev/null; sleep 60", "cat")
	host, err := newLoadedHost(t.Context(), map[string]Executable{"alias": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := host.ValidateSettings(ctx, "alias", Settings{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("validation ignored cancellation")
	}
	if _, err := host.List(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := host.Execute(ctx, Execution{ProviderID: "alias", Command: "echo"}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	healthy := executableFixture(t, printInfo(fixtureInfo), "cat")
	host, err = newLoadedHost(t.Context(), map[string]Executable{"alias": {Path: healthy}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			if _, err := host.Execute(t.Context(), Execution{ProviderID: "alias", Command: "echo", Context: testScope(t)}, nil); err != nil {
				t.Error(err)
			}
			entries, err := host.List(t.Context())
			if err != nil || len(entries) != 1 || entries[0].Availability != Available {
				t.Error(entries, err)
			}
			if err == nil && len(entries) == 1 {
				delete(entries[0].Info.Commands, "echo")
			}
		})
	}
	workers.Wait()
	if listEntries(t, host)[0].Info.Commands["echo"].Description != "fixture" {
		t.Fatal("shared metadata mutation")
	}
}

type changingArgument struct{ calls int }

func (a *changingArgument) MarshalJSON() ([]byte, error) {
	a.calls++
	if a.calls == 1 {
		return []byte(`"validated"`), nil
	}
	return []byte(`"changed"`), nil
}

// SPEC-0090 AC-006: execution normalizes dynamic arguments once before transport.
func TestExecutionArgumentSnapshot(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	host, err := newLoadedHost(t.Context(), map[string]Executable{"example": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	argument := &changingArgument{}
	execution := Execution{
		ProviderID: "example", Command: "echo", Context: testScope(t),
		Arguments: Arguments{"message": argument},
	}

	value, err := host.Execute(t.Context(), execution, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if argument.calls != 1 || requireType[map[string]any](t, requireType[map[string]any](t, value)["arguments"])["message"] != "validated" {
		t.Fatal("arguments changed after validation", argument.calls, value)
	}
}

func requireType[T any](t testing.TB, value any) T {
	t.Helper()
	result, ok := value.(T)
	if !ok {
		t.Fatalf("unexpected type %T", value)
	}
	return result
}
