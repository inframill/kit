package localexec

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestBlockedOutputRespondsToCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close() //nolint:errcheck // Releases the intentionally blocked write.
	defer writer.Close() //nolint:errcheck // Test pipe cleanup.
	live := &liveWriter{ctx: ctx, cancel: cancel, destination: writer}
	done := make(chan error, 1)
	go func() { _, err := live.Write([]byte("blocked")); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked output succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked output ignored cancellation")
	}
}
