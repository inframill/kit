package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtureInfo = `{"name":"custom","version":"1.2.3","protocol":1,"commands":{"echo":{"description":"fixture","argumentsSchema":{"type":"object"},"safety":"read_only","effects":["reads fixture"],"verification":"fixture exits successfully"}}}`

func executableFixture(t *testing.T, info, run string) string {
	t.Helper()
	return validatedFixture(t, info, `cat >/dev/null; printf '{"valid":true}'`, run)
}

func validatedFixture(t *testing.T, info, validate, run string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "custom-driver")
	body := "#!/bin/sh\ncase \"$1\" in\ninfo) " + info + ";;\nvalidate) " + validate + ";;\nrun) " + run + ";;\n*) exit 2;;\nesac\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func printInfo(document string) string { return "printf '%s' '" + document + "'" }

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
	if err != nil || len(entries) != 1 || entries[0].Status != Offline || !strings.Contains(entries[0].Reason, "size limit") {
		t.Fatal(entries, err)
	}
	path = executableFixture(t, printInfo(fixtureInfo), "head -c 17000000 /dev/zero")
	registry, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	command, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: testScope(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.Execute(t.Context()); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatal(err)
	}
}

func testScope(t *testing.T) ExecutionContext {
	t.Helper()
	root := t.TempDir()
	return ExecutionContext{Binding: Binding{InstanceID: "00000000-0000-4000-8000-000000000001", Name: "test/custom"}, CommandID: "00000000-0000-4000-8000-000000000002", WorkspaceDirectory: root, ArtifactDirectory: root, DriverConfig: Config{}, Values: map[string]any{"value": "fixture"}}
}

// SPEC-0000 REQ-008/009: stdin is structured; output, logs and errors redact secrets.
func TestExecutableRunAndRedaction(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	registry, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	scope := testScope(t)
	scope.Stdout, scope.Stderr = &stdout, &stderr
	command, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: scope, Arguments: map[string]any{"message": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := command.Execute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok || object["commandID"] != scope.CommandID || object["workspaceDirectory"] != scope.WorkspaceDirectory || !json.Valid(stdout.Bytes()) {
		t.Fatal(value, stdout.String())
	}

	for index, run := range []string{
		`cat >/dev/null; printf '%s' "$INFRAMILL_SECRET_TEST" >&2; printf '{"value":"sensitive-value"}'`,
		`cat >/dev/null; printf '%s' '{"value":"\u0073ensitive-value"}'`,
		`cat >/dev/null; printf '%s' "$INFRAMILL_SECRET_TEST" >&2; exit 7`,
	} {
		path = executableFixture(t, printInfo(fixtureInfo), run)
		registry, err = loadFixture(t.Context(), []string{path}, append(os.Environ(), "INFRAMILL_SECRET_TEST=sensitive-value"))
		if err != nil {
			t.Fatal(err)
		}
		stdout.Reset()
		stderr.Reset()
		command, err = registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		value, err = command.Execute(t.Context())
		if (err != nil) != (index == 2) {
			t.Fatalf("unexpected result: %v, %v", value, err)
		}
		object, ok = value.(map[string]any)
		if index != 2 && (!ok || object["value"] != "[REDACTED]") {
			t.Fatal("result was not redacted", value)
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		combined := stdout.String() + stderr.String() + string(encoded)
		if err != nil {
			combined += err.Error()
		}
		if strings.Contains(combined, "sensitive-value") {
			t.Fatal("secret leaked", combined)
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
	if _, err = registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: testScope(t)}); err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatal("validation did not reject safely", err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("validation rejection ran effects", err)
	}
	path = validatedFixture(t, info, `cat >/dev/null; test -z "${INFRAMILL_SECRET_TEST+x}" || exit 8; printf '{"valid":true}'`, `cat >/dev/null; test "$INFRAMILL_SECRET_TEST" = runtime-only || exit 8; printf '{"received":true}'`)
	registry, err = loadFixture(t.Context(), []string{path}, append(os.Environ(), "INFRAMILL_SECRET_TEST=runtime-only"))
	if err != nil {
		t.Fatal(err)
	}
	command, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: testScope(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.Execute(t.Context()); err != nil {
		t.Fatal("runtime secret was not supplied", err)
	}
	scope := testScope(t)
	scope.Values["large"] = strings.Repeat("x", MaxRequest)
	if _, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: scope}); err == nil || !strings.Contains(err.Error(), "request exceeds size limit") {
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
		command, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: testScope(t)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := command.Execute(t.Context()); err == nil {
			t.Fatal("failed or malformed command succeeded")
		}
	}
	marker := filepath.Join(t.TempDir(), "survived")
	path := executableFixture(t, printInfo(fixtureInfo), "cat >/dev/null; (sleep 0.5; echo survived > '"+marker+"') & wait")
	registry, err := loadFixture(t.Context(), []string{path}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	command, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: testScope(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := command.Execute(ctx); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Driver descendant survived cancellation", err)
	}
}

func loadFixture(ctx context.Context, paths, environ []string) (Manager, error) {
	entries := Configurations{}
	for index, path := range paths {
		id := "custom"
		if index > 0 {
			id += fmt.Sprint(index)
		}
		entries[id] = Executable{Path: path}
	}
	return New(ctx, entries, environ)
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
	registry, err := New(t.Context(), Configurations{"custom": {Path: path, Args: []string{prefix}}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	command, err := registry.Prepare(t.Context(), CommandRequest{DriverID: "custom", Command: "echo", Scope: testScope(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
}
