package runmill

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	err := runProcess(context.Background(), processCommand{
		Program: testShell, Args: []string{"-c", "exit 2"},
	})
	var processErr *processError
	if !errors.As(err, &processErr) || processErr.ExitCode != 2 {
		t.Fatalf("Run() = %#v", err)
	}
}

// SPEC-0000 REQ-009/010: explicit environment values reach execution unchanged.
func TestRunnerPreservesSecretValues(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("shell fixture is Unix-specific")
	}
	var output bytes.Buffer
	err := runProcess(context.Background(), processCommand{
		Program: testShell,
		Args:    []string{"-c", "printf '%s' \"$ORDINARY:$CREDENTIAL\""},
		Env:     map[string]string{"ORDINARY": "visible", "CREDENTIAL": "split-secret"},
		Stdout:  &oneByteWriter{destination: &output},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "visible:split-secret" {
		t.Fatalf("output = %q", got)
	}
}

type oneByteWriter struct{ destination *bytes.Buffer }

func (w *oneByteWriter) Write(value []byte) (int, error) {
	for _, item := range value {
		if err := w.destination.WriteByte(item); err != nil {
			return 0, err
		}
	}
	return len(value), nil
}

// SPEC-0000 REQ-009/010: file sinks retain output even when the process fails.
func TestRecordedStreamsPreserveValues(t *testing.T) {
	root := t.TempDir()
	stdout, err := os.Create(filepath.Join(root, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(root, "stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	runErr := runProcess(t.Context(), processCommand{Program: testShell, Args: []string{"-c", `printf 'out:%s' "$CREDENTIAL"; printf 'err:%s' "$CREDENTIAL" >&2; exit 1`}, Env: map[string]string{"CREDENTIAL": "known-secret"}, Stdout: stdout, Stderr: stderr})
	if runErr == nil {
		t.Fatal("expected process failure")
	}
	if err := errors.Join(stdout.Close(), stderr.Close()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"stdout.log": "out:known-secret", "stderr.log": "err:known-secret"} {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(content) != want {
			t.Fatalf("%s = %q %v", name, content, err)
		}
	}
}

type failingOutput struct {
	calls int
	err   error
}

func (w *failingOutput) Write(data []byte) (int, error) {
	w.calls++
	return len(data) - 1, w.err
}

// SPEC-0000 REQ-009/010: removing filtering retains output failure and nil-sink behavior.
func TestRunnerOutputFailures(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("shell fixture is Unix-specific")
	}
	for _, stream := range []string{"stdout", "stderr"} {
		for _, cause := range []error{nil, errors.New("fixture output failure")} {
			writer := &failingOutput{err: cause}
			command := processCommand{Program: testShell, Args: []string{"-c", "printf output; printf diagnostics >&2"}}
			if stream == "stdout" {
				command.Stdout = writer
			} else {
				command.Stderr = writer
			}
			want := cause
			if want == nil {
				want = io.ErrShortWrite
			}
			if err := runProcess(t.Context(), command); !errors.Is(err, want) || writer.calls != 1 {
				t.Fatal(stream, err, writer.calls)
			}
		}
	}
	if err := runProcess(t.Context(), processCommand{Program: testShell, Args: []string{"-c", "printf output; printf diagnostics >&2"}}); err != nil {
		t.Fatal(err)
	}
}
