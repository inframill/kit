package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	"github.com/sir-hassan/inframill/pkg/harness"
)

const testConfig = `main:
  port: 8080
logger:
  type: slog
  settings: {level: "${TEST_LOG_LEVEL}", format: json}
handlers:
  - type: connect-engine
    settings: {path: /inframill.v1.EngineService/}
workers: []
`

const (
	testLogEnvironment = "TEST_LOG_LEVEL=info"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// SPEC-0000 REQ-014: harness serves the started engine without auth.
func TestHarnessComposedServer(t *testing.T) {
	t.Chdir(t.TempDir())
	logs := &logCapture{addresses: make(chan string, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	environ := []string{testLogEnvironment}
	listener, port := reservePort(t)
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	config := strings.Replace(testConfig, "port: 8080", "port: "+port, 1)
	// SPEC-0006 REQ-003: an offline configured executable does not prevent serving.
	config += "engine: {drivers: {offline: {path: ./missing-driver}}}\n"
	app, err := build(ctx, environ, writeConfig(t, config), "integration", logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := app.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	environ[0] = "TEST_LOG_LEVEL=invalid"
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	var address string
	select {
	case address = <-logs.addresses:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}
	_, listeningPort, splitErr := net.SplitHostPort(address)
	if splitErr != nil || listeningPort != port {
		t.Fatal("configured port changed", address, splitErr)
	}
	address = net.JoinHostPort("127.0.0.1", port)
	client := inframillv1connect.NewEngineServiceClient(&http.Client{Timeout: time.Second}, "http://"+address)
	response, err := client.ListInstances(t.Context(), connect.NewRequest(&pb.ListInstancesRequest{}))
	if err != nil {
		t.Fatal("engine was not ready", err)
	}
	if len(response.Msg.GetInstances()) != 0 || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
		t.Fatal("unexpected RPC response", response)
	}
	if _, queryErr := client.ListInstances(t.Context(), connect.NewRequest(&pb.ListInstancesRequest{Limit: 101})); connect.CodeOf(queryErr) != connect.CodeInvalidArgument {
		t.Fatal("RPC error translation changed", queryErr)
	}
	commands, err := client.ListCommandDefinitions(t.Context(), connect.NewRequest(&pb.ListCommandDefinitionsRequest{}))
	if err != nil || len(commands.Msg.GetCommands()) != 1 || commands.Msg.GetCommands()[0].GetName() != "builtin.delete" {
		t.Fatal("offline Driver unexpectedly exposed commands", commands, err)
	}

	// SPEC-0007 REQ-002: daemon health includes unavailable configured Drivers.
	health, err := client.ListDrivers(t.Context(), connect.NewRequest(&pb.ListDriversRequest{}))
	if err != nil || len(health.Msg.GetDrivers()) != 1 {
		t.Fatal(health, err)
	}
	entry := health.Msg.Drivers[0]
	if entry.Id != "offline" || entry.Status != pb.DriverStatus_DRIVER_STATUS_OFFLINE || entry.Reason == "" || entry.CheckedAt == nil {
		t.Fatal(entry)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+"/inframill.v1.EngineService/InitInstance", strings.NewReader(strings.Repeat(" ", 1<<20)+"{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	oversized, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var limitError struct {
		Code string `json:"code"`
	}
	if decodeErr := json.NewDecoder(oversized.Body).Decode(&limitError); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if limitError.Code != "resource_exhausted" {
		t.Fatal("body limit error changed", limitError.Code)
	}
	if closeErr := oversized.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if oversized.StatusCode != http.StatusTooManyRequests {
		t.Fatal("body limit changed", oversized.StatusCode)
	}
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	content := logs.String()
	for _, fragment := range []string{`"service":"inframill"`, `"version":"integration"`, `"event":"http.request.completed"`, `"event":"process.stopped"`} {
		if !strings.Contains(content, fragment) {
			t.Fatalf("missing %s in logs", fragment)
		}
	}
}

func reservePort(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // May already be closed to hand the selected port to harness.
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return listener, port
}

// SPEC-0000 REQ-014: an occupied upstream port fails and invokes cleanup.
func TestListenFailureClosesApplication(t *testing.T) {
	t.Chdir(t.TempDir())
	_, port := reservePort(t)
	logs := &logCapture{addresses: make(chan string, 1)}
	config := strings.Replace(testConfig, "port: 8080", "port: "+port, 1)
	err := Serve(t.Context(), []string{testLogEnvironment}, writeConfig(t, config), "test", logs)
	if err == nil || !strings.Contains(err.Error(), "listen on port") {
		t.Fatal("listen conflict accepted", err)
	}
	if !strings.Contains(logs.String(), `"event":"process.stopped"`) {
		t.Fatal("listen failure did not clean up")
	}
}

func TestRejectInvalidPortBeforeStartup(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, port := range []int{0, -1, 65536} {
		config := strings.Replace(testConfig, "port: 8080", "port: "+strconv.Itoa(port), 1)
		if _, err := build(t.Context(), []string{testLogEnvironment}, writeConfig(t, config), "test", io.Discard); err == nil {
			t.Fatal("invalid port accepted", port)
		}
	}
	if _, err := os.Stat(".inframill"); !os.IsNotExist(err) {
		t.Fatal("invalid port created workspaces", err)
	}
}

func TestConfigurationFailsBeforeEngineStartup(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, config := range []string{strings.Replace(testConfig, "connect-engine", "unknown", 1), strings.Replace(testConfig, "/inframill.v1.EngineService/", "/wrong", 1), strings.Replace(testConfig, "format: json", "format: invalid", 1)} {
		if _, err := build(t.Context(), []string{testLogEnvironment}, writeConfig(t, config), "test", io.Discard); err == nil {
			t.Fatal("invalid config accepted")
		}
		if _, err := os.Stat(".inframill"); !os.IsNotExist(err) {
			t.Fatal("invalid config created workspaces", err)
		}
	}
}

// SPEC-0009 REQ-007: a rejected named layout preserves data and closes the app.
func TestStartupFailureClosesApplication(t *testing.T) {
	t.Chdir(t.TempDir())
	directory := filepath.Join(".inframill", "instances", "team", "dev", "runtime", "commands")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "invalid.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logCapture{addresses: make(chan string, 1)}
	_, err := build(t.Context(), []string{testLogEnvironment}, writeConfig(t, testConfig), "test", logs)
	if err == nil || !strings.Contains(err.Error(), "start engine") {
		t.Fatal("invalid persisted state accepted", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(directory, "invalid.json")); readErr != nil || string(data) != "{" {
		t.Fatal("legacy data changed", readErr)
	}
	if !strings.Contains(logs.String(), `"event":"process.stopped"`) {
		t.Fatal("startup failure did not clean up")
	}
}

// SPEC-0000 REQ-011/019: changing composition paths never migrates prior daemon data.
func TestPreviousStorageRootsPreserved(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, root := range []string{"packages", "workspaces"} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("existing data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app, err := build(t.Context(), []string{testLogEnvironment}, writeConfig(t, testConfig), "test", io.Discard)
	if err != nil {
		t.Fatal("old roots were discovered", err)
	}
	if err := app.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"packages", "workspaces"} {
		data, readErr := os.ReadFile(filepath.Join(root, "sentinel"))
		if readErr != nil || string(data) != "existing data" {
			t.Fatal("old storage changed", root, readErr)
		}
	}
}

func TestDefaultsAndEnvironmentSnapshot(t *testing.T) {
	input := []string{"NAME=first", "NAME=last", "VALUE=with=equals"}
	snapshot := snapshotEnvironment(input)
	input[1] = "NAME=changed"
	if snapshot["NAME"] != "last" || snapshot["VALUE"] != "with=equals" {
		t.Fatal("environment was not copied")
	}
	defaults := mainDefaults(harness.MainConfig{})
	if defaults.ReadTimeout != 30*time.Second || defaults.WriteTimeout != 150*time.Second {
		t.Fatal(defaults)
	}
	custom := harness.MainConfig{ReadTimeout: time.Second, WriteTimeout: 2 * time.Second}
	if mainDefaults(custom) != custom {
		t.Fatal("configured timeouts overwritten")
	}
}

type logCapture struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	addresses chan string
}

func (capture *logCapture) Write(content []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	var record struct {
		Event   string `json:"event"`
		Address string `json:"address"`
	}
	if json.Unmarshal(content, &record) == nil && record.Event == "server.listening" {
		capture.addresses <- record.Address
	}
	return capture.buffer.Write(content)
}

func (capture *logCapture) String() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.buffer.String()
}
