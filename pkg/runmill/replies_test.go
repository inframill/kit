package runmill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// SPEC-0000 REQ-008/009: exercise actual subprocess outcomes,
// retained validated replies independently of process status.
// SPEC-0000 REQ-009/010: strings and error fields remain unchanged.
func TestHostReplies(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		failed       bool
		code         string
		records      int
	}{
		{"messages", `printf '%s\n' '{"type":"message","message":"hello"}' '{"type":"success","data":[1,true]}'`, false, "", 2},
		{"no_data", `printf '%s\n' '{"type":"success"}'`, false, "", 1},
		{"null", `printf '%s\n' '{"type":"success","data":null}'`, false, "", 1},
		{"typed_error", `printf '%s\n' '{"type":"error","code":"INVALID_ARGUMENT","message":"required"}';exit 7`, true, "INVALID_ARGUMENT", 1},
		{"zero_error", `printf '%s\n' '{"type":"error","code":"INVALID_ARGUMENT","message":"required"}'`, true, "", 1},
		{"nonzero_success", `printf '%s\n' '{"type":"success","data":true}';exit 7`, true, "", 1},
		{"duplicate", `printf '%s\n' '{"type":"success"}' '{"type":"success"}'`, true, "", 1},
		{"trailing_message", `printf '%s\n' '{"type":"success"}' '{"type":"message","message":"late"}'`, true, "", 1},
		{"message_only", `printf '%s\n' '{"type":"message","message":"progress"}'`, true, "", 1},
		{"partial", `printf '%s' '{"type":"success"}'`, true, "", 0},
		{"irrelevant", `printf '%s\n' '{"type":"message","message":"hello","data":null}'`, true, "", 0},
		{"null_message", `printf '%s\n' '{"type":"success","message":null}'`, true, "", 0},
		{"malformed", `printf 'not JSON\n'`, true, "", 0},
		{"unchanged_values", `printf '%s\n' '{"type":"message","message":"\u0073ensitive-value"}' '{"type":"success","data":{"sensitive-value":"\u0073ensitive-value"}}';printf sensitive- >&2;printf value >&2`, false, "", 2},
		{"unchanged_error", `printf '%s\n' '{"type":"error","code":"sensitive-value","message":"\u0073ensitive-value"}';exit 2`, true, "sensitive-value", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := rawReplyFixture(t, tc.script)
			host, err := newLoadedHost(t.Context(), map[string]Executable{"custom": {Path: path}}, append(os.Environ(), "INFRAMILL_SECRET_TEST=sensitive-value"))
			if err != nil {
				t.Fatal(err)
			}
			var replies, logs bytes.Buffer
			scope := testScope(t)
			scope.Replies = &replies
			scope.Diagnostics = &logs
			request := Execution{ProviderID: "custom", Command: "echo", Context: scope}

			value, err := host.Execute(t.Context(), request, map[string]string{})
			if (err != nil) != tc.failed {
				t.Fatal(value, err)
			}
			if tc.failed && value != nil {
				t.Fatal("failed process published result", value)
			}
			if tc.code != "" {
				var typed *ProviderError
				if !errors.As(err, &typed) || typed.Code != tc.code {
					t.Fatal(err)
				}
			}
			if strings.Count(replies.String(), "\n") != tc.records {
				t.Fatal(replies.String())
			}
			if tc.name == "unchanged_values" {
				object, ok := value.(map[string]any)
				var message Reply
				if err := json.Unmarshal(bytes.Split(replies.Bytes(), []byte("\n"))[0], &message); err != nil {
					t.Fatal(err)
				}
				if !ok || object["sensitive-value"] != "sensitive-value" || message.Message != "sensitive-value" || logs.String() != "sensitive-value" {
					t.Fatal(value, message, logs.String())
				}
			}
			if tc.name == "unchanged_error" {
				var typed *ProviderError
				if !errors.As(err, &typed) || typed.Message != "sensitive-value" {
					t.Fatal(err)
				}
			}
		})
	}
}
func rawReplyFixture(t *testing.T, run string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reply-provider")
	script := `#!/bin/sh
test "$#" = 0 || exit 8
request=$(cat)
case "$request" in
*'"method":"info"'*) ` + printInfo(fixtureInfo) + `;;
*'"method":"validate_command"'*) printf '%s\n' '{"type":"success"}';;
*'"method":"run"'*) ` + run + `;;
*) exit 8;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// SPEC-0000 REQ-008/009: exact encoded message bounds and write failures are sticky.
func TestReplyBoundsAndStreaming(t *testing.T) {
	base, _ := json.Marshal(Reply{Type: "message", Message: "x"})
	for _, extra := range []int{0, 1} {
		var sink bytes.Buffer
		w := &replyWriter{sink: &outputWriter{destination: &sink}, limit: MaxExecutionReplyBytes}
		err := w.message(strings.Repeat("x", MaxMessageBytes-len(base)+extra))
		if extra == 0 && (err != nil || sink.Len() != MaxMessageBytes) {
			t.Fatal(err, sink.Len())
		}
		if extra == 1 && (err == nil || w.final(Reply{Type: "success"}) == nil || sink.Len() != 0) {
			t.Fatal("message failure was not sticky")
		}
	}
	var sink bytes.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &replyReader{limit: MaxValidationReplyBytes, stop: cancel, live: &sink}
	for range 1000 {
		if _, err := output.Write([]byte("{\"type\":\"message\",\"message\":\"now\"}\n")); err != nil {
			t.Fatal(err)
		}
		if len(output.pending) != 0 {
			t.Fatal("message stream retained")
		}
	}
	if sink.Len() == 0 || ctx.Err() != nil {
		t.Fatal("messages were not delivered immediately")
	}
	if _, err := output.Write([]byte("{\"type\":\"success\"}\n")); err != nil {
		t.Fatal(err)
	}
	if err := output.finish(); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{MaxValidationReplyBytes, MaxExecutionReplyBytes} {
		for _, extra := range []int{0, 1} {
			raw := fmt.Sprintf(`{"type":"success","data":"%s"}`+"\n", strings.Repeat("x", limit-len(`{"type":"success","data":""}`)-1+extra))
			_, cancel := context.WithCancel(t.Context())
			o := &replyReader{limit: limit, stop: cancel}
			_, err := o.Write([]byte(raw))
			if (err != nil) != (extra == 1) {
				t.Fatal(limit, extra, err)
			}
			cancel()
		}
	}
}

// SPEC-0000 REQ-008/009: both OS pipes drain concurrently, and a message is
// delivered before the Provider is allowed to finish. Failed sinks never replay.
func TestConcurrentToolAndReplyStreams(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	script := `head -c 262144 /dev/zero >&2 &
child=$!
printf '%s\n' '{"type":"message","message":"ready"}'
while test ! -f '` + release + `'; do sleep 0.01; done
wait "$child"
printf '%s\n' '{"type":"success","data":true}'`
	path := rawReplyFixture(t, script)
	host, err := newLoadedHost(t.Context(), map[string]Executable{"custom": {Path: path}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	sink := &messageSignal{ready: make(chan struct{})}
	var logs bytes.Buffer
	scope := testScope(t)
	scope.Replies = sink
	scope.Diagnostics = &logs
	request := Execution{ProviderID: "custom", Command: "echo", Context: scope}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := host.Execute(ctx, request, map[string]string{}); done <- err }()
	select {
	case <-sink.ready:
	case err := <-done:
		t.Fatal("Provider ended before release", err)
	case <-ctx.Done():
		t.Fatal("message was buffered")
	}
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 262144 || strings.Count(sink.String(), "\n") != 2 {
		t.Fatal(logs.Len(), sink.String())
	}
	for _, failTools := range []bool{false, true} {
		path := rawReplyFixture(t, `printf 'tool output' >&2; printf '%s\n' '{"type":"message","message":"one"}' '{"type":"message","message":"two"}' '{"type":"success"}'`)
		host, err := newLoadedHost(t.Context(), map[string]Executable{"custom": {Path: path}}, os.Environ())
		if err != nil {
			t.Fatal(err)
		}
		sink := &failingServeWriter{short: true}
		scope := testScope(t)
		if failTools {
			scope.Diagnostics = sink
		} else {
			scope.Replies = sink
		}
		request := Execution{ProviderID: "custom", Command: "echo", Context: scope}

		if _, err := host.Execute(t.Context(), request, map[string]string{}); err == nil || sink.calls != 1 {
			t.Fatal(err, sink.calls)
		}
	}
}

type messageSignal struct {
	bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *messageSignal) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	w.once.Do(func() { close(w.ready) })
	return n, err
}

// SPEC-0000 REQ-009/010: known values retain JSON types and nested content.
func TestResultValuesAndTypes(t *testing.T) {
	path := rawReplyFixture(t, `printf '%s\n' '{"type":"success","data":{"123456789":[123456789,"123456789",{"marker":"[REDACTED]"}]}}'`)
	host, err := newLoadedHost(t.Context(), map[string]Executable{"custom": {Path: path}}, append(os.Environ(), "INFRAMILL_SECRET_TEST=123456789"))
	if err != nil {
		t.Fatal(err)
	}
	var replies bytes.Buffer
	scope := testScope(t)
	scope.Replies = &replies
	request := Execution{ProviderID: "custom", Command: "echo", Context: scope}

	value, err := host.Execute(t.Context(), request, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatal(value)
	}
	items, ok := object["123456789"].([]any)
	if !ok || len(items) != 3 || items[0] != json.Number("123456789") || items[1] != "123456789" {
		t.Fatal(value)
	}
	marker, ok := items[2].(map[string]any)
	if !ok || marker["marker"] != "[REDACTED]" {
		t.Fatal(value)
	}
	var reply Reply
	if err := json.Unmarshal(replies.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if string(reply.Data) != `{"123456789":[123456789,"123456789",{"marker":"[REDACTED]"}]}` {
		t.Fatal(string(reply.Data))
	}
}
