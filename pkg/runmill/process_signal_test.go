//go:build linux || darwin

package runmill

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SPEC-0000 REQ-003/027: explicit kill reaches descendants and completed handles reject reuse.
func TestLiveProcessGroupSignal(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	started := make(chan Process, 1)
	ended := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		err := runProcess(ctx, processCommand{Program: testShell, Args: []string{"-c", `(sleep 1; echo survived > "$1") & echo ready; wait`, "fixture", filepath.Join(root, "survivor")}, Stdout: writer,
			Process: &ProcessObserver{Started: func(p Process) error { started <- p; return nil }, Exited: func(code int) { ended <- code }},
		})
		_ = writer.Close()
		done <- err
	}()
	var process Process
	select {
	case process = <-started:
	case <-ctx.Done():
		t.Fatal("process not started")
	}
	ready := make(chan struct{})
	go func() { var data [6]byte; _, _ = io.ReadFull(reader, data[:]); close(ready) }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("child not ready")
	}
	if err := process.Signal("UNKNOWN"); err == nil {
		t.Fatal("invalid signal accepted")
	}
	if err := process.Signal("SIGKILL"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("killed process succeeded")
		}
	case <-ctx.Done():
		t.Fatal("process not reaped")
	}
	if code := <-ended; code != -1 {
		t.Fatal("missing signal exit", code)
	}
	if err := process.Signal("SIGINT"); !errors.Is(err, os.ErrProcessDone) {
		t.Fatal("completed process accepted a signal", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "survivor")); !os.IsNotExist(err) {
		t.Fatal("descendant survived group kill", err)
	}
}
