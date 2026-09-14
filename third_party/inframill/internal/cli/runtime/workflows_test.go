package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

type waitClient struct {
	inframillv1connect.EngineServiceClient
	getCommandExecution func(context.Context) (*connect.Response[pb.GetCommandExecutionResponse], error)
	getInstance         func(context.Context) (*connect.Response[pb.GetInstanceResponse], error)
}

func (c waitClient) GetCommandExecution(ctx context.Context, _ *connect.Request[pb.GetCommandExecutionRequest]) (*connect.Response[pb.GetCommandExecutionResponse], error) {
	return c.getCommandExecution(ctx)
}

func (c waitClient) GetInstance(ctx context.Context, _ *connect.Request[pb.GetInstanceRequest]) (*connect.Response[pb.GetInstanceResponse], error) {
	return c.getInstance(ctx)
}

// SPEC-0002 REQ-007/008 / AC-006: a missing Command is not deletion proof.
func TestREQ007WaitOutcomes(t *testing.T) {
	for _, status := range []pb.CommandStatus{pb.CommandStatus_COMMAND_STATUS_SUCCEEDED, pb.CommandStatus_COMMAND_STATUS_FAILED, pb.CommandStatus_COMMAND_STATUS_INTERRUPTED} {
		client := waitClient{getCommandExecution: func(context.Context) (*connect.Response[pb.GetCommandExecutionResponse], error) {
			return connect.NewResponse(&pb.GetCommandExecutionResponse{Execution: &pb.CommandExecution{Status: status}}), nil
		}}
		result, err := WaitForCommand(t.Context(), client, "instance", "command", false)
		if result == nil || (status == pb.CommandStatus_COMMAND_STATUS_SUCCEEDED) != (err == nil) {
			t.Fatal(result, err)
		}
		if status != pb.CommandStatus_COMMAND_STATUS_SUCCEEDED && !errors.Is(err, ErrUnsuccessful) {
			t.Fatal(err)
		}
	}
	client := waitClient{
		getInstance: func(context.Context) (*connect.Response[pb.GetInstanceResponse], error) {
			return connect.NewResponse(&pb.GetInstanceResponse{Instance: &pb.Instance{Status: pb.InstanceStatus_INSTANCE_STATUS_IDLE}}), nil
		},
		getCommandExecution: func(context.Context) (*connect.Response[pb.GetCommandExecutionResponse], error) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("missing command"))
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	result, err := WaitForCommand(ctx, client, "instance", "command", true)
	if result != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(result, err)
	}
	client.getInstance = func(context.Context) (*connect.Response[pb.GetInstanceResponse], error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("absent"))
	}
	result, err = WaitForCommand(t.Context(), client, "instance", "command", true)
	confirmation, ok := result.(*structpb.Struct)
	if err != nil || !ok || !confirmation.GetFields()["deleted"].GetBoolValue() {
		t.Fatal(result, err)
	}
	unavailable := connect.NewError(connect.CodeUnavailable, errors.New("unavailable"))
	client.getInstance = func(context.Context) (*connect.Response[pb.GetInstanceResponse], error) { return nil, unavailable }
	if _, err = WaitForCommand(t.Context(), client, "instance", "command", true); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	client.getCommandExecution = func(ctx context.Context) (*connect.Response[pb.GetCommandExecutionResponse], error) {
		return nil, ctx.Err()
	}
	if _, err = WaitForCommand(ctx, client, "instance", "command", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// SPEC-0002 REQ-002: the existing explicit-empty optional status filter is retained.
func TestREQ002EmptyEnumFilter(t *testing.T) {
	selected := int32(9)
	value := enumValue{set: func(number int32) { selected = number }, labels: map[string]int32{"running": 2}}
	if err := value.Set(""); err != nil || selected != 0 {
		t.Fatal(selected, err)
	}
	if err := value.Set("RUNNING"); err != nil || selected != 2 {
		t.Fatal(selected, err)
	}
	if err := value.Set("secret-sentinel"); err == nil || err.Error() != "invalid enum value" {
		t.Fatal(err)
	}
}
