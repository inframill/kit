package runmill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

const MaxMessageBytes = 64 << 10

type requestEnvelope struct {
	ProtocolVersion int             `json:"protocol"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params"`
}

// Reply is one JSON Lines response. Only success carries Data, only error carries
// Code, and message/error require human text.
type Reply struct {
	Type    string          `json:"type"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ProviderError is a Provider-reported failure, preserved through wrapped Go errors.
type ProviderError struct {
	Code    string
	Message string
}

func (e *ProviderError) Error() string { return e.Code + ": " + e.Message }

func (r Reply) validate() error {
	switch r.Type {
	case "message":
		if r.Message != "" && r.Code == "" && r.Data == nil {
			return nil
		}
	case "success":
		if r.Code == "" {
			return nil
		}
	case "error":
		if r.Message != "" && r.Code != "" && r.Data == nil {
			return nil
		}
	}
	return errors.New("invalid Provider reply envelope")
}

type replyWriter struct {
	mu    sync.Mutex
	sink  *outputWriter
	limit int
	ended bool
	err   error
}

func (w *replyWriter) message(text string) error {
	return w.write(Reply{Type: "message", Message: text}, false)
}
func (w *replyWriter) final(reply Reply) error { return w.write(reply, true) }
func (w *replyWriter) failure() error          { w.mu.Lock(); defer w.mu.Unlock(); return w.err }
func (w *replyWriter) write(reply Reply, final bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.ended {
		w.err = errors.New("reply after final outcome")
		return w.err
	}
	if err := reply.validate(); err != nil {
		w.err = err
		return err
	}
	data, err := json.Marshal(reply)
	limit := w.limit
	if !final {
		limit = MaxMessageBytes
	}
	if err == nil && len(data)+1 > limit {
		err = errors.New("provider response exceeds size limit")
	}
	// Encoding/bounds failures before final output permit one bounded error reply.
	if err != nil {
		if !final {
			w.err = err
		}
		return err
	}
	if final {
		w.ended = true
	}
	_, w.err = w.sink.Write(append(data, '\n'))
	return w.err
}

// replyReader validates each bounded JSON line before forwarding it. It never
// buffers the whole stream or publishes malformed/raw protocol bytes.
type replyReader struct {
	pending []byte
	limit   int
	stop    context.CancelFunc
	live    io.Writer
	final   *Reply
	err     error
}

func (o *replyReader) fail(err error) error { o.err = err; o.stop(); return err }
func (o *replyReader) Write(data []byte) (int, error) {
	if o.err != nil {
		return 0, o.err
	}
	original := len(data)
	for len(data) > 0 {
		n := bytes.IndexByte(data, '\n')
		end := n + 1
		if n < 0 {
			end = len(data)
		}
		if len(o.pending)+end > o.limit {
			return 0, o.fail(errors.New("provider output exceeds size limit"))
		}
		o.pending = append(o.pending, data[:end]...)
		data = data[end:]
		if n >= 0 {
			if err := o.record(o.pending); err != nil {
				return 0, o.fail(err)
			}
			o.pending = nil
		}
	}
	return original, nil
}
func (o *replyReader) record(data []byte) error {
	if o.final != nil {
		return errors.New("Provider reply after final outcome")
	}
	var r Reply
	if err := decode(data, &r); err != nil {
		return err
	}
	if err := r.validate(); err != nil {
		return err
	}
	// Enforce field presence as well as values (e.g. data:null on an error).
	var fields map[string]json.RawMessage
	if err := decode(data, &fields); err != nil {
		return err
	}
	if message, ok := fields["message"]; ok && (len(message) == 0 || message[0] != '"') {
		return errors.New("reply message must be a string")
	}
	if _, ok := fields["data"]; ok && r.Type != "success" {
		return errors.New("data requires a success reply")
	}
	if _, ok := fields["code"]; ok && r.Type != "error" {
		return errors.New("code requires an error reply")
	}
	if r.Type == "message" && len(data) > MaxMessageBytes {
		return errors.New("provider message exceeds size limit")
	}
	if o.live != nil {
		encoded, err := json.Marshal(r)
		if err != nil {
			return err
		}
		encoded = append(encoded, '\n')
		n, err := o.live.Write(encoded)
		if err == nil && n != len(encoded) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return err
		}
	}
	if r.Type != "message" {
		o.final = &r
	}
	return nil
}
func (o *replyReader) finish() error {
	if o.err != nil {
		return o.err
	}
	if len(o.pending) != 0 {
		return errors.New("incomplete Provider JSON Lines reply")
	}
	if o.final == nil {
		return errors.New("missing final Provider reply")
	}
	return nil
}
