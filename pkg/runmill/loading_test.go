package runmill

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

// SPEC-0000 REQ-008: successful loading is one-shot, including an empty catalog.
func TestLoadLifecycle(t *testing.T) {
	m := NewHost(nil)
	if _, err := m.List(t.Context()); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
	if err := m.ValidateSettings(t.Context(), "debug", Settings{}); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
	if _, err := m.Execute(t.Context(), Execution{}, nil); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
	if err := m.Load(t.Context(), map[string]Executable{"BAD_ID": {}}); err == nil {
		t.Fatal("invalid ID accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Load(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := m.Load(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	entries := listEntries(t, m)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
	if err := m.Load(t.Context(), map[string]Executable{"debug": {}}); !errors.Is(err, ErrAlreadyLoaded) {
		t.Fatal(err)
	}
	if len(listEntries(t, m)) != 0 {
		t.Fatal("second Load changed catalog")
	}
}

func recordRequests(t *testing.T, path, log string) {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "request=$(cat)", "request=$(cat)\nprintf '%s\\n' \"$request\" >> '"+log+"'", 1))
	if err := os.WriteFile(path, source, 0o700); err != nil {
		t.Fatal(err)
	}
}

func recordedMethods(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var envelope requestEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, envelope.Method)
	}
	return methods
}

// SPEC-0000 REQ-008: concurrent loads publish once and all runtime methods
// use the startup capability catalog without another info invocation.
func TestStartupProbesOnly(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests")
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	recordRequests(t, path, log)
	configs := map[string]Executable{"b": {Path: path}, "a": {Path: path}}
	m := requireType[*host](t, NewHost(os.Environ()))
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() { results <- m.Load(t.Context(), configs) })
	}
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrAlreadyLoaded) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
	initial := listEntries(t, m)
	if initial[0].ID != "a" || initial[1].ID != "b" {
		t.Fatal(initial)
	}
	configs["a"] = Executable{Path: "changed"}
	delete(configs, "b")
	for range 3 {
		if err := m.ValidateSettings(t.Context(), "a", Settings{}); err != nil {
			t.Fatal(err)
		}
		request := Execution{ProviderID: "a", Command: "echo", Context: testScope(t)}

		if err := m.Load(t.Context(), nil); !errors.Is(err, ErrAlreadyLoaded) {
			t.Fatal(err)
		}
		if _, err := m.Execute(t.Context(), request, map[string]string{}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(initial, listEntries(t, m)) {
		t.Fatal("runtime operation changed startup health")
	}
	want := []string{"info", "info"}
	for range 3 {
		want = append(want, "validate_settings", "run")
	}
	if got := recordedMethods(t, log); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	// Metadata remains cached even if the executable disappears.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(t.Context(), Execution{ProviderID: "a", Command: "echo", Context: testScope(t)}, nil); err == nil {
		t.Fatal("unavailable executable prepared")
	}
	if !reflect.DeepEqual(initial, listEntries(t, m)) {
		t.Fatal("execution failure changed loaded health")
	}
}

// SPEC-0000 REQ-008: cancellation after partial discovery publishes no entries;
// another caller can observe unloaded state and cancellation stays retryable.
func TestCancelledLoadIsAtomic(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	good := executableFixture(t, printInfo(fixtureInfo), "cat")
	hanging := executableFixture(t, "touch '"+started+"'; sleep 60", "cat")
	m := NewHost(os.Environ())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Load(ctx, map[string]Executable{"a": {Path: good}, "b": {Path: hanging}}) }()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("probe did not start")
		case <-ticker.C:
		}
	}
	if _, err := m.List(t.Context()); !errors.Is(err, ErrNotLoaded) {
		t.Fatal("partial catalog published", err)
	}
	waiting, stop := context.WithCancel(t.Context())
	stop()
	if err := m.Load(waiting, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := m.List(t.Context()); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
	if err := m.Load(t.Context(), map[string]Executable{"a": {Path: good}}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentSnapshot(t *testing.T) {
	path := executableFixture(t, `test -z "$INFRAMILL_SECRET_TEST" || exit 9; `+printInfo(fixtureInfo), `cat >/dev/null; test -z "${INFRAMILL_SECRET_TEST+x}" || exit 7; printf true`)
	env := []string{"PATH=" + os.Getenv("PATH"), "INFRAMILL_SECRET_TEST=original"}
	m := NewHost(env)
	env[1] = "INFRAMILL_SECRET_TEST=changed"
	if err := m.Load(t.Context(), map[string]Executable{"custom": {Path: path}}); err != nil {
		t.Fatal(err)
	}
	request := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

	if _, err := m.Execute(t.Context(), request, map[string]string{}); err != nil {
		t.Fatal(err)
	}
}

// SPEC-0090 AC-005/006: only capability/transport checks precede one direct run.
func TestDirectExecutionCapabilities(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests")
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	recordRequests(t, path, log)
	m, err := newLoadedHost(t.Context(), map[string]Executable{"alias": {Path: path}, "offline": {Path: "/missing"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Execution{
		{ProviderID: "missing", Command: "echo", Context: testScope(t)},
		{ProviderID: "offline", Command: "echo", Context: testScope(t)},
		{ProviderID: "alias", Command: "missing", Context: testScope(t)},
		{ProviderID: "alias", Command: "echo"},
		{ProviderID: "alias", Command: "echo", Context: testScope(t), Arguments: Arguments{"bad": make(chan int)}},
	} {
		if _, err := m.Execute(t.Context(), request, nil); err == nil {
			t.Fatal("invalid execution accepted")
		}
	}
	if got := recordedMethods(t, log); !reflect.DeepEqual(got, []string{"info"}) {
		t.Fatal(got)
	}
	value, err := m.Execute(t.Context(), Execution{ProviderID: "alias", Command: "echo", Context: testScope(t), Arguments: Arguments{"arbitrary": []any{true, 42, "text"}}}, nil)
	if err != nil || value == nil {
		t.Fatal(value, err)
	}
	if got := recordedMethods(t, log); !reflect.DeepEqual(got, []string{"info", "run"}) {
		t.Fatal(got)
	}
}
