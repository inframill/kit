package runmill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type servingProvider struct {
	secrets         map[string]string
	description     string
	invalidMetadata bool
	cancel          context.CancelFunc
	calls           []string
	panicAt         string
	failAt          string
	value           any
	runErr          error
	scope           ExecutionContext
	messages        []string
	concurrent      bool
}

func (d *servingProvider) call(name string) error {
	d.calls = append(d.calls, name)
	if name == "execute" && d.cancel != nil {
		d.cancel()
	}
	if d.panicAt == name {
		panic("private-panic-sentinel")
	}
	if d.failAt == name {
		return errors.New("fixture rejection")
	}
	return nil
}
func (d *servingProvider) Definition() (ProviderDefinition, error) {
	if err := d.call("info"); err != nil {
		return ProviderDefinition{}, err
	}
	var info ProviderInfo
	_ = json.Unmarshal([]byte(fixtureInfo), &info)
	if d.invalidMetadata || d.description != "" {
		command := info.Commands["echo"]
		command.Description = d.description
		info.Commands["echo"] = command
	}
	return ProviderDefinition{Name: info.Name, Commands: info.Commands}, nil
}
func (d *servingProvider) ValidateSettings(context.Context, Settings) error {
	return d.call("settings")
}
func (d *servingProvider) Execute(_ context.Context, scope ExecutionContext, name string, args Arguments, secrets map[string]string) (any, error) {
	if name != "echo" {
		return nil, errors.New("unknown command")
	}
	if args == nil {
		return nil, errors.New("arguments were not normalized")
	}
	d.scope, d.secrets = scope, secrets
	if err := d.call("execute"); err != nil {
		return nil, err
	}
	for _, message := range d.messages {
		_ = d.scope.Message(message)
	}
	if d.concurrent {
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { _ = d.scope.Message("concurrent") })
		}
		wg.Wait()
	}
	// Deliberately ignore the write error to verify Serve's failure tracking.
	_, _ = io.WriteString(d.scope.Diagnostics, "progress\n")
	_, _ = io.WriteString(d.scope.Diagnostics, "finished\n")
	return d.value, d.runErr
}

func servingRequests() (settingsParams, invocationParams) {
	settings := settingsParams{Settings: Settings{}}
	return settings, invocationParams{settingsParams: settings, Command: "echo", WorkspaceDir: "/workspace", ArtifactDir: "/artifacts", ExecutionID: "00000000-0000-4000-8000-000000000002"}
}

// SPEC-0000 REQ-008/009/015/025: old field names cannot reach provider callbacks.
func TestRetiredInvocationFields(t *testing.T) {
	_, command := servingRequests()
	for _, fields := range [][2]string{{"command", "commandName"}, {"workspaceDir", "workspaceDirectory"}, {"artifactDir", "artifactDirectory"}} {
		for _, method := range []string{"run"} {
			t.Run(method+"/"+fields[1], func(t *testing.T) {
				input := strings.Replace(requestInput(t, method, command), `"`+fields[0]+`":`, `"`+fields[1]+`":`, 1)
				provider := &servingProvider{}
				var output bytes.Buffer
				code := Serve(t.Context(), provider, ServeOptions{Version: "1.2.3", Stdin: strings.NewReader(input), Stdout: &output, Stderr: io.Discard})
				if code == 0 || len(provider.calls) != 0 || !strings.Contains(output.String(), `"code":"PROVIDER_ERROR"`) {
					t.Fatal(code, provider.calls, output.String())
				}
			})
		}
	}
	var info ProviderInfo
	if err := decode([]byte(strings.Replace(fixtureInfo, `"protocolVersion":`, `"protocol":`, 1)), &info); err == nil {
		t.Fatal("retired discovery protocol field accepted")
	}
}
func serveInput(t *testing.T, input any) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func requestInput(t *testing.T, method string, params any) string {
	if method == "run" {
		if command, ok := params.(invocationParams); ok {
			params = runParams{invocationParams: command, Secrets: json.RawMessage(`{}`)}
		}
	}
	t.Helper()
	if params == nil {
		params = struct{}{}
	}
	return serveInput(t, requestEnvelope{ProtocolVersion: ProtocolVersion, Method: method, Params: json.RawMessage(serveInput(t, params))})
}
func serveFixture(t *testing.T, d *servingProvider, method string, params any) (int, []Reply, string) {
	t.Helper()
	var out, logs bytes.Buffer
	code := Serve(t.Context(), d, ServeOptions{Version: "0.1.0-dev", Stdin: strings.NewReader(requestInput(t, method, params)), Stdout: &out, Stderr: &logs})
	var replies []Reply
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var r Reply
		if err := decode([]byte(line), &r); err != nil {
			t.Fatal(err, out.String())
		}
		replies = append(replies, r)
	}
	return code, replies, logs.String()
}

// SPEC-0000 REQ-008/009: common methods, phase isolation, typed results.
// SPEC-0000 REQ-009/010: validation and execution need only execution inputs.
func TestServePhases(t *testing.T) {
	settings, command := servingRequests()
	for _, tc := range []struct {
		method string
		params any
		calls  []string
	}{
		{"info", nil, []string{"info"}}, {"validate_settings", settings, []string{"settings"}},
		{"run", command, []string{"execute"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			d := &servingProvider{value: map[string]any{"ok": true}}
			code, replies, logs := serveFixture(t, d, tc.method, tc.params)
			if code != 0 || len(replies) != 1 || replies[0].Type != "success" || !reflect.DeepEqual(d.calls, tc.calls) {
				t.Fatal(code, replies, d.calls)
			}
			if tc.method == "run" && (string(replies[0].Data) != `{"ok":true}` || logs != "progress\nfinished\n") {
				t.Fatal(replies, logs)
			}
			if tc.method == "validate_command" || tc.method == "run" {
				if d.scope.ExecutionID != command.ExecutionID || d.scope.WorkspaceDir != command.WorkspaceDir ||
					d.scope.ArtifactDir != command.ArtifactDir || !reflect.DeepEqual(d.scope.Settings, command.Settings) {
					t.Fatal("execution context changed", d.scope)
				}
			}
		})
	}
	for _, value := range []any{nil, true, 12, "text", []int{1, 2}, struct {
		OK bool `json:"ok"`
	}{true}} {
		code, replies, _ := serveFixture(t, &servingProvider{value: value}, "run", command)
		if code != 0 || replies[0].Type != "success" {
			t.Fatal(code, replies)
		}
	}
}

// SPEC-0000 REQ-009/010: removed identity fields reject before any callbacks.
func TestServeRejectsDomainMetadata(t *testing.T) {
	_, command := servingRequests()
	for _, method := range []string{"run"} {
		for name, field := range map[string]string{
			"binding-object":      `"binding":{"instanceId":"00000000-0000-4000-8000-000000000001","namespace":"test","name":"example","blueprintReference":"ghcr.io/test/example:1"}`,
			"binding-null":        `"binding":null`,
			"instance-id":         `"instanceId":"00000000-0000-4000-8000-000000000001"`,
			"namespace":           `"namespace":"test"`,
			"name":                `"name":"example"`,
			"blueprint-reference": `"blueprintReference":"ghcr.io/test/example:1"`,
		} {
			t.Run(method+"/"+name, func(t *testing.T) {
				input := strings.Replace(requestInput(t, method, command), `"settings":{}`, `"settings":{},`+field, 1)
				d := &servingProvider{}
				var out, logs bytes.Buffer
				code := Serve(t.Context(), d, ServeOptions{Stdin: strings.NewReader(input), Stdout: &out, Stderr: &logs})
				if code == 0 || len(d.calls) != 0 || !strings.Contains(out.String(), `"type":"error"`) || logs.Len() != 0 {
					t.Fatal(code, d.calls, out.String(), logs.String())
				}
			})
		}
	}
}

// SPEC-0090 AC-005: command validation is removed and run requires an execution ID.
func TestServeExecutionIDRequiredForRun(t *testing.T) {
	_, command := servingRequests()
	command.ExecutionID = ""
	d := &servingProvider{}
	if code, replies, _ := serveFixture(t, d, "validate_command", command); code == 0 || len(d.calls) != 0 {
		t.Fatal(code, replies, d.calls)
	}
	d = &servingProvider{}
	if code, replies, _ := serveFixture(t, d, "run", command); code == 0 || len(d.calls) != 0 || replies[0].Type != "error" {
		t.Fatal(code, replies, d.calls)
	}
}

// SPEC-0000 REQ-008/009: strict envelopes and removed fields reject before callbacks.
func TestServeRejectsInvalidInput(t *testing.T) {
	settings, command := servingRequests()
	valid := requestInput(t, "run", command)
	documents := []string{
		// SPEC-0000 REQ-009: removed method/field names never invoke callbacks.
		requestInput(t, "validate_manifest", settings),
		requestInput(t, "validate_settings", map[string]any{"config": map[string]any{}}),
		requestInput(t, "validate_settings", map[string]any{"settings": map[string]any{}, "config": map[string]any{}}),
		strings.Replace(requestInput(t, "validate_command", command), `"settings":`, `"config":`, 1),
		strings.Replace(valid, `"settings":`, `"config":`, 1),
		strings.Replace(valid, `"settings":{}`, `"settings":{},"config":{}`, 1),
		`{}`, `null`, `{"protocol":1,"method":"info","params":null}`, `{"protocol":2,"method":"info","params":{}}`,
		`{"protocol":1,"method":"unknown","params":{}}`, `{"protocol":1,"method":"info","params":{"extra":true}}`,
		requestInput(t, "validate_settings", map[string]any{}), requestInput(t, "validate_settings", map[string]any{"settings": nil}),
		requestInput(t, "validate_settings", map[string]any{"settings": map[string]any{}, "values": map[string]any{}}),
		requestInput(t, "validate_settings", command), requestInput(t, "run", settings),
		strings.Replace(valid, `"settings":{}`, `"settings":{},"values":{}`, 1),
		strings.Replace(valid, `"settings":{}`, `"settings":null`, 1),
		strings.Replace(valid, `"command":"echo"`, `"command":""`, 1),
		strings.Replace(valid, "/artifacts", "", 1),
		strings.Replace(valid, command.ExecutionID, "", 1),
		strings.Replace(valid, "/workspace", "", 1), valid + " {}", "{", strings.Repeat(" ", MaxRequestBytes+1),
	}
	for index, input := range documents {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			d := &servingProvider{}
			var out, logs bytes.Buffer
			code := Serve(t.Context(), d, ServeOptions{Stdin: strings.NewReader(input), Stdout: &out, Stderr: &logs})
			if code == 0 || len(d.calls) != 0 || !strings.Contains(out.String(), `"type":"error"`) || logs.Len() != 0 {
				t.Fatal(code, d.calls, out.String(), logs.String())
			}
		})
	}
	d := &servingProvider{}
	if Serve(t.Context(), d, ServeOptions{Args: []string{"info"}, Stdin: strings.NewReader(requestInput(t, "info", nil))}) == 0 || len(d.calls) != 0 {
		t.Fatal("argv dispatch accepted")
	}
}

type failingServeWriter struct {
	calls int
	short bool
}

func (w *failingServeWriter) Write([]byte) (int, error) {
	w.calls++
	if w.short {
		return 0, nil
	}
	return 0, errors.New("writer failed")
}

// SPEC-0000 REQ-008/009: typed errors, safe panics, concurrent messages and sticky output failure.
func TestServeFailures(t *testing.T) {
	settings, command := servingRequests()
	for _, phase := range []string{"info", "settings", "execute"} {
		method, params := "run", any(command)
		if phase == "info" {
			method, params = "info", nil
		}
		if phase == "settings" {
			method, params = "validate_settings", settings
		}
		for _, panics := range []bool{false, true} {
			d := &servingProvider{}
			if panics {
				d.panicAt = phase
			} else {
				d.failAt = phase
			}
			code, replies, _ := serveFixture(t, d, method, params)
			if code == 0 || replies[0].Type != "error" || strings.Contains(replies[0].Message, "private-panic-sentinel") {
				t.Fatal(code, replies)
			}
		}
	}
	for _, d := range []*servingProvider{{value: make(chan int)}, {value: strings.Repeat("x", MaxExecutionReplyBytes)}, {invalidMetadata: true}} {
		method, params := "run", any(command)
		if d.invalidMetadata {
			method, params = "info", nil
		}
		code, replies, _ := serveFixture(t, d, method, params)
		if code == 0 || replies[0].Type != "error" {
			t.Fatal(code, replies)
		}
	}
	code, replies, _ := serveFixture(t, &servingProvider{runErr: fmt.Errorf("wrapped: %w", &ProviderError{Code: "INVALID_ARGUMENT", Message: "region is required"})}, "run", command)
	if code == 0 || replies[0].Code != "INVALID_ARGUMENT" || replies[0].Message != "region is required" {
		t.Fatal(code, replies)
	}
	code, replies, _ = serveFixture(t, &servingProvider{messages: []string{"first"}, concurrent: true}, "run", command)
	if code != 0 || len(replies) != 22 || replies[0].Message != "first" || replies[21].Type != "success" {
		t.Fatal(code, replies)
	}
	for _, short := range []bool{false, true} {
		sink := &failingServeWriter{short: short}
		code := Serve(t.Context(), &servingProvider{messages: []string{"one", "two"}}, ServeOptions{Stdin: strings.NewReader(requestInput(t, "run", command)), Stdout: sink})
		if code == 0 || sink.calls != 1 {
			t.Fatal(code, sink.calls)
		}
	}
	var out bytes.Buffer
	code = Serve(t.Context(), &servingProvider{messages: []string{strings.Repeat("x", MaxMessageBytes)}}, ServeOptions{Stdin: strings.NewReader(requestInput(t, "run", command)), Stdout: &out})
	if code == 0 || strings.Contains(out.String(), `"type":"success"`) {
		t.Fatal(code, out.Len())
	}
}

// SPEC-0000 REQ-008/009: full encoded record bounds, removed validation method, cancellation.
func TestServeMetadataAndCancellation(t *testing.T) {
	_, command := servingRequests()
	command.ExecutionID = ""
	code, _, _ := serveFixture(t, &servingProvider{}, "validate_command", command)
	if code == 0 {
		t.Fatal(code)
	}
	command.ExecutionID = "00000000-0000-4000-8000-000000000002"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	code = Serve(ctx, &servingProvider{cancel: cancel, value: "unpublished"}, ServeOptions{Stdin: strings.NewReader(requestInput(t, "run", command)), Stdout: &out})
	if code == 0 || strings.Contains(out.String(), "unpublished") {
		t.Fatal(code, out.String())
	}
	for _, limit := range []int{MaxValidationReplyBytes, MaxExecutionReplyBytes} {
		var out bytes.Buffer
		w := &replyWriter{sink: &outputWriter{destination: &out}, limit: limit}
		base, _ := json.Marshal(Reply{Type: "success", Message: "x"})
		if err := w.final(Reply{Type: "success", Message: strings.Repeat("x", limit-len(base))}); err != nil || out.Len() != limit {
			t.Fatal(err, out.Len(), limit)
		}
	}
}

// SPEC-0000 REQ-008/009: the complete stdin request is accepted at exactly 4 MiB.
func TestRequestBoundary(t *testing.T) {
	input := requestInput(t, "info", nil)
	input += strings.Repeat(" ", MaxRequestBytes-len(input))
	for _, extra := range []string{"", " "} {
		d := &servingProvider{}
		var out bytes.Buffer
		code := Serve(t.Context(), d, ServeOptions{Version: "test", Stdin: strings.NewReader(input + extra), Stdout: &out})
		if extra == "" && (code != 0 || len(d.calls) != 1) {
			t.Fatal(code, d.calls)
		}
		if extra != "" && (code == 0 || len(d.calls) != 0) {
			t.Fatal(code, d.calls)
		}
	}
}

// SPEC-0000 REQ-020: secrets exist only in run and decode without coercion.
func TestRunSecrets(t *testing.T) {
	_, command := servingRequests()
	valid := requestInput(t, "run", command)
	for _, input := range []string{
		strings.Replace(valid, `,"secrets":{}`, "", 1),
		strings.Replace(valid, `"secrets":{}`, `"secrets":null`, 1),
		strings.Replace(valid, `"secrets":{}`, `"secrets":{"bad/name":"synthetic"}`, 1),
		strings.Replace(valid, `"secrets":{}`, `"secrets":{"token":1}`, 1),
		strings.Replace(valid, `"secrets":{}`, `"secrets":{"token":"a","token":"b"}`, 1),
		strings.Replace(valid, `"secrets":{}`, `"secrets":{"token":"`+strings.Repeat("x", 1<<20)+`"}`, 1),
		requestInput(t, "info", map[string]any{"secrets": map[string]string{}}),
		requestInput(t, "validate_settings", map[string]any{"settings": map[string]any{}, "secrets": map[string]string{}}),
		requestInput(t, "validate_command", runParams{invocationParams: command, Secrets: json.RawMessage(`{}`)}),
	} {
		d := &servingProvider{}
		var out bytes.Buffer
		code := Serve(t.Context(), d, ServeOptions{Stdin: strings.NewReader(input), Stdout: &out})
		if code == 0 || len(d.calls) != 0 || strings.Contains(out.String(), "synthetic") {
			t.Fatal("invalid secrets reached callback", code, d.calls, out.String())
		}
	}
	want := map[string]string{"token": "  first\nsecond\n", "empty": "", "escaped": "\\\""}
	d := &servingProvider{}
	code, _, _ := serveFixture(t, d, "run", runParams{invocationParams: command, Secrets: encodedSecrets(t, want)})
	if code != 0 || !reflect.DeepEqual(d.secrets, want) || !reflect.DeepEqual(d.calls, []string{"execute"}) {
		t.Fatal(code, d.calls)
	}
}

func encodedSecrets(t *testing.T, set map[string]string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// SPEC-0000 REQ-008/009/025: correlation identifiers have no UUID/path semantics.
func TestOpaqueExecutionIDs(t *testing.T) {
	_, request := servingRequests()
	for _, id := range []string{"job-42", "queue/job:42", "correlation with spaces", "日本語"} {
		for _, method := range []string{"run"} {
			t.Run(method+"/"+id, func(t *testing.T) {
				request.ExecutionID = id
				d := &servingProvider{}
				code, replies, _ := serveFixture(t, d, method, request)
				if code != 0 || d.scope.ExecutionID != id {
					t.Fatal(code, replies, d.scope.ExecutionID)
				}
			})
		}
	}
}
