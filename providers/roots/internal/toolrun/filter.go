package toolrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"sync"
)

func patterns(secrets map[string]string) [][]byte {
	var out [][]byte
	for _, value := range secrets {
		if value == "" {
			continue
		}
		out = append(out, []byte(value))
		b, _ := json.Marshal(value)
		out = append(out, b[1:len(b)-1])
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

type filter struct {
	mu       sync.Mutex
	sink     io.Writer
	patterns [][]byte
	pending  []byte
	keep     int
	err      error
	total    int
}

func newFilter(sink io.Writer, secrets map[string]string) *filter {
	f := &filter{sink: sink, patterns: patterns(secrets), keep: 1}
	for _, p := range f.patterns {
		if len(p) > f.keep {
			f.keep = len(p)
		}
	}
	return f
}
func (f *filter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.total += len(p)
	if f.total > Limit {
		f.err = errors.New("tool diagnostics exceed limit")
		return 0, f.err
	}
	f.pending = append(f.pending, p...)
	f.drain(false)
	return len(p), f.err
}
func (f *filter) drain(final bool) {
	var out bytes.Buffer
	for len(f.pending) > 0 && (final || len(f.pending) >= f.keep) {
		n := 0
		for _, pattern := range f.patterns {
			if bytes.HasPrefix(f.pending, pattern) {
				n = len(pattern)
				break
			}
		}
		if n > 0 {
			out.WriteString("[REDACTED]")
			f.pending = f.pending[n:]
		} else {
			out.WriteByte(f.pending[0])
			f.pending = f.pending[1:]
		}
	}
	if out.Len() > 0 {
		n, err := f.sink.Write(out.Bytes())
		if err == nil && n != out.Len() {
			err = io.ErrShortWrite
		}
		f.err = err
	}
}
func (f *filter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.drain(true)
	}
	return f.err
}
