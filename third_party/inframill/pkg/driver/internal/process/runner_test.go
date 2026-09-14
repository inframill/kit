package process

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	windowsOS = "windows"
	testShell = "/bin/sh"
)

func TestRunnerRejectsNonzeroExit(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("shell fixture is Unix-specific")
	}
	err := Runner{}.Run(context.Background(), Command{
		Program: testShell, Args: []string{"-c", "exit 2"},
	})
	var processErr *Error
	if !errors.As(err, &processErr) || processErr.ExitCode != 2 {
		t.Fatalf("Run() = %#v", err)
	}
}

func TestRunnerUsesTypedSecretsAndRedactsStream(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("shell fixture is Unix-specific")
	}
	var output bytes.Buffer
	err := Runner{}.Run(context.Background(), Command{
		Program:   testShell,
		Args:      []string{"-c", "printf '%s' \"$ORDINARY:$CREDENTIAL\""},
		Env:       map[string]string{"ORDINARY": "visible"},
		SecretEnv: map[string]Secret{"CREDENTIAL": stringSecret("split-secret")},
		Stdout:    &oneByteWriter{destination: &output},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "visible:[REDACTED]" || strings.Contains(got, "split-secret") {
		t.Fatalf("output = %q", got)
	}
}

func TestRedactorProtectsSecretsSplitAcrossWrites(t *testing.T) {
	var output bytes.Buffer
	writer := newRedactor(&output, [][]byte{[]byte("split-secret")})
	for _, part := range []string{"visible:split", "-sec", "ret:done"} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "visible:[REDACTED]:done" {
		t.Fatalf("redacted output = %q", got)
	}
}

type stringSecret string

func (s stringSecret) Reveal() string { return string(s) }

type oneByteWriter struct{ destination *bytes.Buffer }

func (w *oneByteWriter) Write(value []byte) (int, error) {
	for _, item := range value {
		if err := w.destination.WriteByte(item); err != nil {
			return 0, err
		}
	}
	return len(value), nil
}

func TestRecordedStreamsRedactBeforePersistence(t *testing.T) {
	root := t.TempDir()
	stdout, err := os.Create(filepath.Join(root, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(root, "stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	runErr := Runner{}.Run(t.Context(), Command{Program: testShell, Args: []string{"-c", `printf 'out:%s' "$CREDENTIAL"; printf 'err:%s' "$CREDENTIAL" >&2; exit 1`}, SecretEnv: map[string]Secret{"CREDENTIAL": stringSecret("known-secret")}, Stdout: stdout, Stderr: stderr})
	if runErr == nil {
		t.Fatal("expected process failure")
	}
	if err := errors.Join(stdout.Close(), stderr.Close()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"stdout.log": "out:[REDACTED]", "stderr.log": "err:[REDACTED]"} {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(content) != want {
			t.Fatalf("%s = %q %v", name, content, err)
		}
	}
}
