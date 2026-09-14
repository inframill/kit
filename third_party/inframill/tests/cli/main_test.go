package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	argBuild    = "build"
	argServer   = "server"
	argPackage  = "package"
	argSystem   = "command"
	argList     = "list"
	argInstance = "instance"
	argCommand  = "command"
)

var binary, repository string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "inframill-command-tests-")
	if err != nil {
		panic(err)
	}
	repository, err = filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(directory, "inframill")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	build := exec.CommandContext(ctx, "go", argBuild, "-o", binary, "./cmd")
	build.Dir = repository
	output, err := build.CombinedOutput()
	cancel()
	if err != nil {
		panic(fmt.Sprintf("build command: %v\n%s", err, output))
	}
	code := m.Run()
	if err := os.RemoveAll(directory); err != nil {
		panic(err)
	}
	os.Exit(code)
}

type invocation struct {
	code           int
	stdout, stderr string
}

func invoke(t *testing.T, ctx context.Context, directory, input string, args ...string) invocation {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return invocation{code, stdout.String(), stderr.String()}
}

func TestLocalDispatchAndInputErrors(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{}, {"unknown"}, {argPackage, argBuild}, {argServer, "--config", "missing", "extra"}} {
		got := invoke(t, t.Context(), root, "", args...)
		if got.code != 2 || got.stdout != "" {
			t.Fatalf("%v: %+v", args, got)
		}
	}
	for _, args := range [][]string{{"--help"}, {argSystem, argList, "--help"}} {
		got := invoke(t, t.Context(), root, "", args...)
		if got.code != 0 || !strings.Contains(got.stdout, "Usage:") || got.stderr != "" {
			t.Fatalf("help: %+v", got)
		}
	}
	got := invoke(t, t.Context(), root, "", argServer, "--config", filepath.Join(root, "missing.yaml"))
	if got.code != 3 || !strings.Contains(got.stderr, "load application configuration") {
		t.Fatalf("server dispatch: %+v", got)
	}
}

type fixture struct {
	inframillv1connect.UnimplementedEngineServiceHandler
	mu       sync.Mutex
	requests []proto.Message
	failed   bool
	deleted  bool
}

func (f *fixture) record(m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, proto.Clone(m))
}

func (f *fixture) ListCommandDefinitions(_ context.Context, r *connect.Request[pb.ListCommandDefinitionsRequest]) (*connect.Response[pb.ListCommandDefinitionsResponse], error) {
	f.record(r.Msg)
	return connect.NewResponse(&pb.ListCommandDefinitionsResponse{Commands: []*pb.CommandDefinition{{Name: qualifiedPlan}}}), nil
}

func (f *fixture) InitInstance(_ context.Context, r *connect.Request[pb.InitInstanceRequest]) (*connect.Response[pb.InitInstanceResponse], error) {
	f.record(r.Msg)
	return connect.NewResponse(&pb.InitInstanceResponse{Instance: &pb.Instance{Id: instanceID, Name: r.Msg.GetName()}}), nil
}

func (f *fixture) SubmitCommand(_ context.Context, r *connect.Request[pb.SubmitCommandRequest]) (*connect.Response[pb.SubmitCommandResponse], error) {
	f.record(r.Msg)
	return connect.NewResponse(&pb.SubmitCommandResponse{CommandId: commandID}), nil
}

func (f *fixture) GetCommandExecution(_ context.Context, r *connect.Request[pb.GetCommandExecutionRequest]) (*connect.Response[pb.GetCommandExecutionResponse], error) {
	f.record(r.Msg)
	if f.deleted {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("deleted"))
	}
	status := pb.CommandStatus_COMMAND_STATUS_SUCCEEDED
	var failure *pb.Failure
	if f.failed {
		status = pb.CommandStatus_COMMAND_STATUS_FAILED
		failure = &pb.Failure{Code: "command_failed", Message: "fixture failure"}
	}
	return connect.NewResponse(&pb.GetCommandExecutionResponse{Execution: &pb.CommandExecution{Request: &pb.CommandRequest{Id: commandID, Command: qualifiedPlan}, Status: status, Error: failure}}), nil
}

// SPEC-0011 REQ-003: execution listing is distinct from the command catalog.
func (f *fixture) ListCommandExecutions(_ context.Context, r *connect.Request[pb.ListCommandExecutionsRequest]) (*connect.Response[pb.ListCommandExecutionsResponse], error) {
	f.record(r.Msg)
	return connect.NewResponse(&pb.ListCommandExecutionsResponse{Executions: []*pb.CommandExecution{{Request: &pb.CommandRequest{Id: commandID, Command: qualifiedPlan}, Status: pb.CommandStatus_COMMAND_STATUS_SUCCEEDED}}}), nil
}

func (f *fixture) GetInstance(_ context.Context, r *connect.Request[pb.GetInstanceRequest]) (*connect.Response[pb.GetInstanceResponse], error) {
	f.record(r.Msg)
	if f.deleted {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("deleted"))
	}
	return connect.NewResponse(&pb.GetInstanceResponse{Instance: &pb.Instance{Id: instanceID, Status: pb.InstanceStatus_INSTANCE_STATUS_IDLE}}), nil
}

func (f *fixture) ReadCommandLog(_ context.Context, r *connect.Request[pb.ReadCommandLogRequest]) (*connect.Response[pb.ReadCommandLogResponse], error) {
	f.record(r.Msg)
	return connect.NewResponse(&pb.ReadCommandLogResponse{Data: []byte("fixture log"), NextOffset: 11, Eof: true}), nil
}

func serveFixture(t *testing.T, f *fixture) string {
	t.Helper()
	_, handler := inframillv1connect.NewEngineServiceHandler(f)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

const (
	instanceID = "00000000-0000-4000-8000-000000000001"
	commandID  = "00000000-0000-4000-8000-000000000002"
)

func TestClientCommands(t *testing.T) {
	f := &fixture{}
	endpoint := serveFixture(t, f)
	root := t.TempDir()
	for _, test := range []struct {
		args  []string
		input string
		want  string
	}{
		{[]string{"command", "list"}, "", qualifiedPlan},
		{[]string{"command", "show", qualifiedPlan}, "", qualifiedPlan},
		{[]string{"command", "--output=json", "execution", "list", "--instance-id", instanceID, "--status=succeeded", "--limit=7"}, "", `"executions"`},
		{[]string{argInstance, "init", testInstanceName, "--file", "-"}, `{"blueprint":"registry.example/cloud","values":{"size":"small"}}`, instanceID[:17] + "..."},
		{[]string{argInstance, localExecCommand, instanceID, qualifiedPlan, "--args-file", "-", "--idempotency-key", "retry"}, `{"destroy":true}`, commandID[:17] + "..."},
		{[]string{argCommand, "execution", "get", instanceID, commandID}, "", string(inframill.Succeeded)},
		{[]string{argCommand, "execution", "wait", instanceID, commandID}, "", string(inframill.Succeeded)},
		{[]string{argCommand, "execution", "stdout", instanceID, commandID}, "", "fixture log"},
	} {
		args := append(append([]string{}, test.args...), "--endpoint", endpoint)
		got := invoke(t, t.Context(), root, test.input, args...)
		if got.code != 0 || !strings.Contains(got.stdout, test.want) || got.stderr != "" {
			t.Fatalf("%v: %+v", args, got)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var init *pb.InitInstanceRequest
	var submit *pb.SubmitCommandRequest
	var query *pb.ListCommandExecutionsRequest
	for _, r := range f.requests {
		switch r := r.(type) {
		case *pb.InitInstanceRequest:
			init = r
		case *pb.SubmitCommandRequest:
			submit = r
		case *pb.ListCommandExecutionsRequest:
			query = r
		}
	}
	if query == nil || query.GetInstanceId() != instanceID || query.GetLimit() != 7 || query.GetStatus() != pb.CommandStatus_COMMAND_STATUS_SUCCEEDED {
		t.Fatal(query)
	}
	if init == nil || init.GetName() != testInstanceName || init.GetInstance().GetValues().GetFields()["size"].GetStringValue() != "small" {
		t.Fatal(init)
	}
	if submit == nil || submit.GetInstanceId() != instanceID || submit.GetCommand() != qualifiedPlan || !submit.GetArgs().GetFields()["destroy"].GetBoolValue() || submit.GetIdempotencyKey() != "retry" {
		t.Fatal(submit)
	}
}

func TestClientValidationAndFailures(t *testing.T) {
	for _, args := range [][]string{{argInstance, localExecCommand, instanceID, "debug", "plan"}, {argInstance, "init", testInstanceName}, {argCommand, "execution", "get", commandID}, {argCommand, "execution", "list"}, {argCommand, "execution", "stdout", instanceID, commandID, "--limit", "-1"}} {
		got := invoke(t, t.Context(), t.TempDir(), "", args...)
		if got.code != 2 || got.stdout == "" && got.stderr == "" {
			t.Fatalf("%v: %+v", args, got)
		}
	}
	f := &fixture{failed: true}
	endpoint := serveFixture(t, f)
	got := invoke(t, t.Context(), t.TempDir(), "", argCommand, "execution", "wait", instanceID, commandID, "--endpoint", endpoint, "--output", "json")
	if got.code != 1 || !strings.Contains(got.stdout, "COMMAND_STATUS_FAILED") {
		t.Fatal(got)
	}
}

func TestClientWaitForDeletion(t *testing.T) {
	endpoint := serveFixture(t, &fixture{deleted: true})
	got := invoke(t, t.Context(), t.TempDir(), "", argInstance, "delete", instanceID, "--wait", "--endpoint", endpoint, "--output", "json")
	if got.code != 0 || !strings.Contains(got.stdout, `"deleted":true`) {
		t.Fatal(got)
	}
}

const (
	qualifiedPlan = "debug.secret"
)

const testInstanceName = "project/dev"

// SPEC-0003 REQ-001: removed packaging commands never touch the requested output.
func TestPackageCommandRemoved(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "layout")
	for _, args := range [][]string{{argPackage}, {argPackage, argBuild, "missing-source", destination}, {"help", argPackage}} {
		got := invoke(t, t.Context(), root, "", args...)
		if got.code != 2 || got.stdout != "" {
			t.Fatal(args, got)
		}
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("removed command created output", err)
	}
}
