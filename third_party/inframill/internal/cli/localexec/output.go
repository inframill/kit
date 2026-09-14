package localexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// liveWriter permits at most one outstanding write per stream. Cancellation or
// a stalled consumer cannot hold the Instance worker indefinitely. The blocked
// writer, if any, is abandoned on process exit; no further writes are launched.
type liveWriter struct {
	ctx         context.Context
	cancel      context.CancelFunc
	destination io.Writer
	mu          sync.Mutex
	err         error
}

func (w *liveWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	payload := bytes.Clone(data)
	done := make(chan error, 1)
	go func() {
		n, err := w.destination.Write(payload)
		if err == nil && n != len(payload) {
			err = io.ErrShortWrite
		}
		done <- err
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case w.err = <-done:
	case <-w.ctx.Done():
		w.err = w.ctx.Err()
	case <-timer.C:
		w.err = errors.New("terminal output stalled")
	}
	if w.err != nil {
		w.cancel()
		return 0, w.err
	}
	return len(data), nil
}

func (w *liveWriter) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}
