package connectadapter_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/sir-hassan/inframill/internal/backend"
	"github.com/sir-hassan/inframill/internal/blueprint"
	"github.com/sir-hassan/inframill/internal/connectadapter"
	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const testDriver = "test"

type fakeDriver struct{}

func newSystem() *fakeDriver { return &fakeDriver{} }

func (*fakeDriver) List(context.Context) ([]drivers.Entry, error) {
	return []drivers.Entry{{ID: testDriver, Status: drivers.Connected, Info: &drivers.Info{Commands: map[string]drivers.CommandDefinition{"run": {Description: "fake execution", ArgumentsSchema: map[string]any{"type": "object"}, Safety: drivers.Reversible, Effects: []string{"fake execution"}, Verification: "fake completes"}}}}}, nil
}
func (*fakeDriver) Validate(context.Context, drivers.ValidationRequest) error { return nil }

func (*fakeDriver) Prepare(_ context.Context, request drivers.CommandRequest) (drivers.PreparedCommand, error) {
	s, op, a := request.Scope, request.Command, request.Arguments
	if op != "run" {
		return nil, errors.New("unknown")
	}
	return fakeExecution{s: s, fail: a["fail"] == true}, nil
}

type fakeExecution struct {
	s    drivers.ExecutionContext
	fail bool
}

func (e fakeExecution) Execute(context.Context) (any, error) {
	if _, err := io.WriteString(e.s.Stdout, "hello stdout"); err != nil {
		return nil, err
	}
	if e.fail {
		return nil, errors.New("exit status 1")
	}
	return map[string]any{"ok": true}, nil
}
func (fakeExecution) Verify(context.Context, any) error { return nil }
func newEngine(t *testing.T, root string, system *fakeDriver) *inframill.Engine {
	t.Helper()
	sourceRoot := t.TempDir()
	dir := filepath.Join(sourceRoot, testDriver)
	if err := os.MkdirAll(filepath.Join(dir, "template/test"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"manifest.yaml": "api_version: inframill.dev/blueprint/v1\ndescription: test\ndrivers: {test: {}}\n", "schema.json": "{}"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, err := blueprint.New(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	b, err := backend.New(root, testDrivers(t, []*fakeDriver{system}), backend.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := inframill.New(t.Context(), source, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestGeneratedRPCLifecycle(t *testing.T) {
	e := newEngine(t, t.TempDir(), newSystem())
	server := httptest.NewServer(connectadapter.Handler(e))
	defer server.Close()
	client := inframillv1connect.NewEngineServiceClient(server.Client(), server.URL)
	ops, checkErr := client.ListCommandDefinitions(t.Context(), connect.NewRequest(&pb.ListCommandDefinitionsRequest{}))
	if checkErr != nil || len(ops.Msg.GetCommands()) != 2 {
		t.Fatal(ops, checkErr)
	}
	init, checkErr := client.InitInstance(t.Context(), connect.NewRequest(&pb.InitInstanceRequest{Name: testInstanceName, Instance: &pb.InstanceConfig{Blueprint: testDriver, Values: &structpb.Struct{}}}))
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	id := init.Msg.GetInstance().GetId()
	if !inframill.ValidUUID(id) {
		t.Fatal(id)
	}
	submit := &pb.SubmitCommandRequest{InstanceId: id, Command: "test.run", IdempotencyKey: "retry"}
	first, checkErr := client.SubmitCommand(t.Context(), connect.NewRequest(submit))
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	second, checkErr := client.SubmitCommand(t.Context(), connect.NewRequest(submit))
	if checkErr != nil || first.Msg.GetCommandId() != second.Msg.GetCommandId() {
		t.Fatal(second, checkErr)
	}
	commandID := first.Msg.GetCommandId()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := e.Wait(ctx, inframill.InstanceID(id), inframill.CommandID(commandID)); err != nil {
		t.Fatal(err)
	}
	got, checkErr := client.GetCommandExecution(t.Context(), connect.NewRequest(&pb.GetCommandExecutionRequest{InstanceId: id, CommandId: commandID}))
	if checkErr != nil || got.Msg.GetExecution().GetStatus() != pb.CommandStatus_COMMAND_STATUS_SUCCEEDED || got.Msg.GetExecution().GetResult().GetStructValue().GetFields()["ok"].GetBoolValue() != true {
		t.Fatal(got, checkErr)
	}
	page, checkErr := client.ListCommandExecutions(t.Context(), connect.NewRequest(&pb.ListCommandExecutionsRequest{InstanceId: id, Limit: 1}))
	if checkErr != nil || len(page.Msg.GetExecutions()) != 1 {
		t.Fatal(page, checkErr)
	}
	log, checkErr := client.ReadCommandLog(t.Context(), connect.NewRequest(&pb.ReadCommandLogRequest{InstanceId: id, CommandId: commandID, Stream: pb.LogStream_LOG_STREAM_STDOUT, Limit: 5}))
	if checkErr != nil || string(log.Msg.GetData()) != "hello" || log.Msg.GetEof() {
		t.Fatal(log, checkErr)
	}
	tail, checkErr := client.ReadCommandLog(t.Context(), connect.NewRequest(&pb.ReadCommandLogRequest{InstanceId: id, CommandId: commandID, Stream: pb.LogStream_LOG_STREAM_STDOUT, Offset: log.Msg.GetNextOffset()}))
	if checkErr != nil || string(tail.Msg.GetData()) != " stdout" || !tail.Msg.GetEof() {
		t.Fatal(tail, checkErr)
	}
	if _, err := client.GetCommandExecution(t.Context(), connect.NewRequest(&pb.GetCommandExecutionRequest{CommandId: commandID})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	if _, err := client.SubmitCommand(t.Context(), connect.NewRequest(&pb.SubmitCommandRequest{InstanceId: id, Command: "unknown.run"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	if _, err := client.InitInstance(t.Context(), connect.NewRequest(&pb.InitInstanceRequest{Name: testInstanceName, Instance: &pb.InstanceConfig{Blueprint: testDriver, Values: &structpb.Struct{}}})); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatal(err)
	}
}

func TestRPCErrorMapping(t *testing.T) {
	e := newEngine(t, t.TempDir(), newSystem())
	server := httptest.NewServer(connectadapter.Handler(e))
	defer server.Close()
	client := inframillv1connect.NewEngineServiceClient(server.Client(), server.URL)
	if _, err := client.GetInstance(t.Context(), connect.NewRequest(&pb.GetInstanceRequest{InstanceId: "00000000-0000-4000-8000-000000000001"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := client.InitInstance(t.Context(), connect.NewRequest(&pb.InitInstanceRequest{Name: testInstanceName})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	if err := e.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubmitCommand(t.Context(), connect.NewRequest(&pb.SubmitCommandRequest{})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
}

const (
	testInstanceName = "project/dev"
)

func testDrivers(t *testing.T, supplied []*fakeDriver) drivers.Manager {
	t.Helper()
	if len(supplied) != 1 {
		t.Fatal("fixture requires one Driver Manager")
	}
	return supplied[0]
}
