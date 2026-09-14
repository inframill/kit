package debugdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	nameKey     = "name"
	waitCommand = "wait"
	contentKey  = "content"
	modeKey     = "mode"
	durationKey = "durationMs"
)

type memoryArtifacts map[string][]byte

func (m memoryArtifacts) ReadFile(_ context.Context, name string) ([]byte, error) {
	data, ok := m[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return bytes.Clone(data), nil
}

func (m memoryArtifacts) WriteFile(_ context.Context, name string, data []byte) error {
	m[name] = bytes.Clone(data)
	return nil
}

func scopeFixture(t *testing.T) ExecutionContext {
	t.Helper()
	return ExecutionContext{CommandID: "00000000-0000-4000-8000-000000000001", WorkspaceDirectory: t.TempDir(), Artifacts: memoryArtifacts{}, Values: map[string]any{}, Binding: drivers.Binding{Name: "tests/debug"}, DriverConfig: drivers.Config{}}
}

// SPEC-0000 REQ-010/013: catalog and preparation are complete and effect-free.
func TestCatalogAndValidation(t *testing.T) {
	d := Driver{}
	definition, err := d.Definition()
	if err != nil || definition.Name != "debug" || len(definition.Commands) != 8 {
		t.Fatal(definition, err)
	}
	scope := scopeFixture(t)
	for _, tc := range []struct {
		name string
		args inframill.Arguments
	}{
		{"unknown", nil},
		{writeArtifact, nil},
		{writeArtifact, inframill.Arguments{nameKey: "../escape", contentKey: "x"}},
		{"echo", inframill.Arguments{"message": 42}},
		{"echo", inframill.Arguments{"unknown": true}},
		{"record", inframill.Arguments{modeKey: ""}},
		{"record", inframill.Arguments{modeKey: "invalid"}},
		{waitCommand, inframill.Arguments{durationKey: -1}},
		{waitCommand, inframill.Arguments{durationKey: 60001}},
		{waitCommand, inframill.Arguments{durationKey: 0.5}},
		{"logs", inframill.Arguments{"stdout": nil}},
	} {
		if _, err = d.Prepare(t.Context(), scope, tc.name, tc.args); err == nil {
			t.Fatal("invalid arguments accepted", tc)
		}
	}
	op, err := d.Prepare(t.Context(), scope, writeArtifact, inframill.Arguments{nameKey: "hello.txt", contentKey: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Artifacts.ReadFile(t.Context(), "hello.txt"); !os.IsNotExist(err) {
		t.Fatal("preparation wrote artifact")
	}
	if _, err = op.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	read, err := d.Prepare(t.Context(), scope, readArtifact, inframill.Arguments{nameKey: "hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := read.Execute(t.Context())
	result, ok := value.(ArtifactResult)
	if err != nil || !ok || result.Content != "hello" {
		t.Fatal(value, err)
	}
}

// SPEC-0000 REQ-010: failed execution records effects exactly once.
func TestRecordBeforeFailureAndDuplicateRejection(t *testing.T) {
	scope := scopeFixture(t)
	op, err := (Driver{}).Prepare(t.Context(), scope, "record", inframill.Arguments{modeKey: failCommand, "message": "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = op.Execute(t.Context()); err == nil || !strings.Contains(err.Error(), "after recording") {
		t.Fatal(err)
	}
	data, err := scope.Artifacts.ReadFile(t.Context(), "record-"+scope.CommandID+".json")
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.CommandID != scope.CommandID || record.Binding.Name != scope.Binding.Name || record.Arguments[messageKey] != "attempt" {
		t.Fatal(record)
	}
	if _, err = op.Execute(t.Context()); err == nil || !strings.Contains(err.Error(), "already recorded") {
		t.Fatal("duplicate overwritten", err)
	}
}

// SPEC-0000 REQ-009/010: cancellation is bounded and raw secrets cannot be artifacts.
func TestCancellationAndArtifactSecretRejection(t *testing.T) {
	scope := scopeFixture(t)
	op, err := (Driver{}).Prepare(t.Context(), scope, waitCommand, inframill.Arguments{durationKey: 60000})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err = op.Execute(ctx); err == nil {
		t.Fatal("cancelled wait succeeded")
	}
	t.Setenv("INFRAMILL_SECRET_DEBUG", "synthetic-private-value")
	op, err = (Driver{}).Prepare(t.Context(), scope, writeArtifact, inframill.Arguments{nameKey: "secret.txt", contentKey: "synthetic-private-value"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = op.Execute(t.Context()); err == nil || strings.Contains(err.Error(), "synthetic-private-value") {
		t.Fatal("secret artifact accepted or leaked", err)
	}
	if _, err = scope.Artifacts.ReadFile(t.Context(), "secret.txt"); !os.IsNotExist(err) {
		t.Fatal("secret persisted")
	}
}

// SPEC-0012 REQ-002: unsupported protocol and request fields cannot execute a command.
func TestREQ002RejectUnsupportedCommandProtocol(t *testing.T) {
	for _, document := range []string{
		`{"protocol":2,"config":{}}`,
		`{"protocol":1,"config":{},"operation":"echo"}`,
		`{"protocol":1,"config":{},"operation":"echo","command":"echo"}`,
	} {
		var out, diagnostics bytes.Buffer
		code := Serve(t.Context(), Driver{}, "test", []string{"validate"}, strings.NewReader(document), &out, &diagnostics)
		if code == 0 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatal(code, out.String(), diagnostics.String())
		}
	}
}
