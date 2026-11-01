package runmill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fixtureInfo = `{"name":"custom","version":"1.2.3","protocolVersion":1,"commands":{"echo":{"description":"fixture","accessMode":"read_write"}}}`

func executableFixture(t *testing.T, info, run string) string {
	t.Helper()
	return validatedFixture(t, info, `cat >/dev/null; printf '{"valid":true}'`, run)
}

func validatedFixture(t *testing.T, info, validate, run string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "custom-provider")
	// The shell fixture consumes one complete envelope before dispatch, and the
	// snippet receives only params. No protocol arguments are accepted.
	validate = strings.ReplaceAll(validate, `printf '{"valid":true}'`, `printf '{"type":"success"}\n'`)
	body := `#!/bin/sh
test "$#" = 0 || exit 2
request=$(cat)
params=${request#*'"params":'}
params=${params%?}
validate_phase() { ` + validate + `; }
run_phase() { printf '{"type":"success","data":'; ` + run + `; printf '}\n'; }
case "$request" in
*'"method":"info"'*) ` + info + `;;
*'"method":"validate_settings"'*) printf '%s' "$params" | validate_phase;;
*'"method":"run"'*) printf '%s' "$params" | run_phase;;
*) exit 2;;
esac
`

	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func printInfo(document string) string {
	return "printf '%s\\n' '{\"type\":\"success\",\"data\":" + document + "}'"
}

// SPEC-0000 REQ-008: hanging and excessive output are bounded.
func TestExecutableLimits(t *testing.T) {
	path := executableFixture(t, "sleep 60", "exit 0")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := loadFixture(ctx, []string{path}, nil); err == nil {
		t.Fatal("hanging probe accepted")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("probe cancellation was not bounded")
	}
	path = executableFixture(t, "head -c 1100000 /dev/zero", "exit 0")
	oversized, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := oversized.List(t.Context())
	if err != nil || len(entries) != 1 || entries[0].Availability != Unavailable || !strings.Contains(entries[0].Reason, "size limit") {
		t.Fatal(entries, err)
	}
	path = executableFixture(t, printInfo(fixtureInfo), "head -c 17000000 /dev/zero")
	registry, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	command := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

	if _, err := registry.Execute(t.Context(), command, map[string]string{}); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatal(err)
	}
}

func testScope(t *testing.T) ExecutionContext {
	t.Helper()
	root := t.TempDir()
	return ExecutionContext{ExecutionID: "00000000-0000-4000-8000-000000000002", WorkspaceDir: root, ArtifactDir: root, Settings: Settings{"value": "fixture"}}
}

// SPEC-0000 REQ-009/010: structured input and output values survive unchanged.
func TestExecutablePreservesOutput(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	registry, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	scope := testScope(t)
	scope.Diagnostics = &logs
	command := Execution{ProviderID: "custom", Command: "echo", Context: scope, Arguments: map[string]any{"message": "hello"}}

	value, err := registry.Execute(t.Context(), command, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok || object["executionId"] != scope.ExecutionID || object["workspaceDir"] != scope.WorkspaceDir || logs.Len() != 0 {
		t.Fatal(value, logs.String())
	}
	// SPEC-0000 REQ-009/010: the actual subprocess request has only execution inputs.
	want := map[string]any{
		"settings": map[string]any{"value": "fixture"}, "command": "echo",
		"arguments": map[string]any{"message": "hello"}, "secrets": map[string]any{},
		"executionId": scope.ExecutionID, "workspaceDir": scope.WorkspaceDir,
		"artifactDir": scope.ArtifactDir,
	}
	if !reflect.DeepEqual(object, want) {
		t.Fatalf("unexpected execution request: %#v", object)
	}

	for index, run := range []string{
		`cat >/dev/null; printf sensitive-value >&2; printf '{"value":"sensitive-value"}'`,
		`cat >/dev/null; printf '%s' '{"value":"\u0073ensitive-value"}'`,
		`cat >/dev/null; printf sensitive-value >&2; exit 7`,
	} {
		path = executableFixture(t, printInfo(fixtureInfo), run)
		registry, err = loadFixture(t.Context(), []string{path}, append(os.Environ(), "INFRAMILL_SECRET_TEST=sensitive-value"))
		if err != nil {
			t.Fatal(err)
		}
		logs.Reset()
		command = Execution{ProviderID: "custom", Command: "echo", Context: scope}

		value, err = registry.Execute(t.Context(), command, map[string]string{})
		if (err != nil) != (index == 2) {
			t.Fatalf("unexpected result: %v, %v", value, err)
		}
		object, ok = value.(map[string]any)
		if index != 2 && (!ok || object["value"] != "sensitive-value") {
			t.Fatal("result value changed", value)
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		combined := logs.String() + string(encoded)
		if err != nil {
			combined += err.Error()
		}
		if !strings.Contains(combined, "sensitive-value") {
			t.Fatal("output value changed", combined)
		}
	}
}

// SPEC-0000 REQ-008/009: validation rejects before run and has no runtime secrets.
func TestValidationBeforeEffectsAndSecretEnvironment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	info := `test -z "${INFRAMILL_SECRET_TEST+x}" || exit 8; ` + printInfo(fixtureInfo)
	validate := `cat >/dev/null; test -z "${INFRAMILL_SECRET_TEST+x}" || exit 8; printf 'invalid configuration' >&2; exit 9`
	run := "echo ran > '" + marker + "'; printf '{}'"
	path := validatedFixture(t, info, validate, run)
	registry, err := loadFixture(t.Context(), []string{path}, append(os.Environ(), "INFRAMILL_SECRET_TEST=runtime-only"))
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.ValidateSettings(t.Context(), "custom", Settings{}); err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatal("validation did not reject safely", err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("validation rejection ran effects", err)
	}
	path = validatedFixture(t, info, `cat >/dev/null; test -z "${INFRAMILL_SECRET_TEST+x}" || exit 8; printf '{"valid":true}'`, `cat >/dev/null; test -z "${INFRAMILL_SECRET_TEST+x}" || exit 8; printf '{"received":true}'`)
	registry, err = loadFixture(t.Context(), []string{path}, append(os.Environ(), "INFRAMILL_SECRET_TEST=runtime-only"))
	if err != nil {
		t.Fatal(err)
	}
	command := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

	if _, err := registry.Execute(t.Context(), command, map[string]string{}); err != nil {
		t.Fatal("obsolete environment was forwarded", err)
	}
	scope := testScope(t)
	scope.Settings["large"] = strings.Repeat("x", MaxRequestBytes)
	if _, err := registry.Execute(t.Context(), Execution{ProviderID: "custom", Command: "echo", Context: scope}, nil); err == nil || !strings.Contains(err.Error(), "request exceeds size limit") {
		t.Fatal("excessive request accepted", err)
	}
}

// SPEC-0000 REQ-009: malformed results fail, and cancellation kills child processes.
func TestExecutableFailureAndCancellation(t *testing.T) {
	for _, run := range []string{"printf not-json", "printf '{} {}'", "exit 4"} {
		path := executableFixture(t, printInfo(fixtureInfo), run)
		registry, err := loadFixture(t.Context(), []string{path}, nil)
		if err != nil {
			t.Fatal(err)
		}
		command := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

		if _, err := registry.Execute(t.Context(), command, map[string]string{}); err == nil {
			t.Fatal("failed or malformed command succeeded")
		}
	}
	marker := filepath.Join(t.TempDir(), "survived")
	path := executableFixture(t, printInfo(fixtureInfo), "cat >/dev/null; (sleep 0.5; echo survived > '"+marker+"') & wait")
	registry, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	command := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := registry.Execute(ctx, command, map[string]string{}); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Provider descendant survived cancellation", err)
	}
}

func loadFixture(ctx context.Context, paths, environ []string) (Host, error) {
	entries := map[string]Executable{}
	for index, path := range paths {
		id := "custom"
		if index > 0 {
			id += fmt.Sprint(index)
		}
		entries[id] = Executable{Path: path}
	}
	return newLoadedHost(ctx, entries, environ)
}

// SPEC-0000 REQ-008/019: literal prefix args work for every protocol invocation.
func TestExecutablePrefixArguments(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "two words;$(touch must-not-run)"
	source = bytes.Replace(source, []byte("#!/bin/sh\n"), []byte("#!/bin/sh\ntest \"$1\" = '"+prefix+"' || exit 8\nshift\n"), 1)
	if err = os.WriteFile(path, source, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := newLoadedHost(t.Context(), map[string]Executable{"custom": {Path: path, Args: []string{prefix}}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	command := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

	if _, err = registry.Execute(t.Context(), command, map[string]string{}); err != nil {
		t.Fatal(err)
	}
}

func newLoadedHost(ctx context.Context, configurations map[string]Executable, environ []string) (Host, error) {
	m := NewHost(environ)
	if err := m.Load(ctx, configurations); err != nil {
		return nil, err
	}
	return m, nil
}

// SPEC-0000 REQ-011: only current protocol and command fields are accepted.
func TestCommandProtocolVocabulary(t *testing.T) {
	for _, document := range []string{
		strings.Replace(fixtureInfo, `"description":`, `"argumentsSchema":{},"description":`, 1),
		strings.Replace(fixtureInfo, `"description":`, `"safety":"read_only","description":`, 1),
		strings.Replace(fixtureInfo, `"description":`, `"effects":["old"],"description":`, 1),
		strings.Replace(fixtureInfo, `"description":`, `"verification":"old","description":`, 1),
		strings.Replace(fixtureInfo, `"protocolVersion":1`, `"protocolVersion":2`, 1),
		strings.Replace(fixtureInfo, `"commands":`, `"operations":`, 1),
		strings.Replace(fixtureInfo, `"commands":`, `"operations":{},"commands":`, 1),
	} {
		path := executableFixture(t, printInfo(document), "exit 99")
		host, err := newLoadedHost(t.Context(), map[string]Executable{"invalid": {Path: path}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		entries := listEntries(t, host)
		if len(entries) != 1 || entries[0].Availability != Unavailable {
			t.Fatal("invalid metadata accepted", entries)
		}
		if _, err := host.Execute(t.Context(), Execution{ProviderID: "invalid", Command: "echo", Context: testScope(t)}, nil); err == nil {
			t.Fatal("offline command prepared")
		}
	}
	for _, document := range []string{
		`{"protocol":1,"settings":{},"operation":"echo"}`,
		`{"protocol":1,"settings":{},"operation":"echo","command":"echo"}`,
	} {
		var request invocationParams
		if err := decode([]byte(document), &request); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
