package process

import (
	"bytes"
	"errors"
	"testing"
)

type failingStream struct {
	bytes.Buffer
	writes int
}

func (w *failingStream) Write(data []byte) (int, error) {
	w.writes++
	n, _ := w.Buffer.Write(data)
	return n, errors.New("downstream closed")
}

func TestRedactionDoesNotFlushEmptyOrReplayFailedOutput(t *testing.T) {
	stream := &failingStream{}
	r := newRedactor(stream, [][]byte{[]byte("test-secret")})
	if _, err := r.Write([]byte("test-")); err != nil {
		t.Fatal(err)
	}
	if stream.writes != 0 {
		t.Fatal("empty redaction buffer was forwarded")
	}
	if _, err := r.Write([]byte("secret\n")); err == nil {
		t.Fatal("downstream failure lost")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if stream.writes != 1 || stream.String() != "[REDACTED]\n" {
		t.Fatal(stream.writes, stream.String())
	}
}
