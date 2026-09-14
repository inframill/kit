//go:build linux || darwin

package process

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancellationKillsChildProcessGroup(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close() //nolint:errcheck // Disposable test pipe.
	done := make(chan error, 1)
	go func() {
		err := (Runner{}).Run(ctx, Command{Program: "/bin/sh", Args: []string{"-c", `(sleep 1; echo survived > "$1") & echo ready; wait`, "fixture", filepath.Join(root, "survivor")}, Stdout: writer})
		_ = writer.Close() //nolint:errcheck // Unblock test reader.
		done <- err
	}()
	ready := make(chan struct{})
	go func() { var b [6]byte; _, _ = io.ReadFull(reader, b[:]); close(ready) }() //nolint:errcheck // Deadline and cancellation assertions below detect missing startup.
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child cleanup exceeded deadline")
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "survivor")); !os.IsNotExist(err) {
		t.Fatal("child survived group cancellation", err)
	}
}
