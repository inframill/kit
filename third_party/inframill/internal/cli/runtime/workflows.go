package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

var ErrUnsuccessful = errors.New("command did not succeed")

func ReadJSON(path string, stdin io.Reader, target proto.Message) error {
	reader := stdin
	if path != "-" {
		file, err := os.Open(path) //nolint:gosec // The CLI user explicitly selected this local input file.
		if err != nil {
			return InputError{"cannot open input JSON file"}
		}
		defer file.Close() //nolint:errcheck // Closing a read-only input does not affect its decoded contents.
		reader = file
	}
	const maxInput = 1 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxInput+1))
	if err != nil {
		return InputError{"cannot read input JSON"}
	}
	if len(data) > maxInput {
		return InputError{"input JSON exceeds 1 MiB"}
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return InputError{"input JSON must be an object"}
	}
	if err := protojson.Unmarshal(data, target); err != nil {
		return InputError{"invalid input JSON"}
	}
	return nil
}

// WaitForCommand verifies Instance absence; a disappearing Command is not a receipt.
func WaitForCommand(ctx context.Context, client inframillv1connect.EngineServiceClient, instanceID, id string, deleting bool) (proto.Message, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if deleting {
			instance, err := client.GetInstance(ctx, connect.NewRequest(&pb.GetInstanceRequest{InstanceId: instanceID}))
			if connect.CodeOf(err) == connect.CodeNotFound {
				return structpb.NewStruct(map[string]any{"instanceId": instanceID, "commandId": id, "deleted": true})
			}
			if err != nil {
				return nil, err
			}
			if instance.Msg.GetInstance().GetStatus() == pb.InstanceStatus_INSTANCE_STATUS_FAILED {
				return instance.Msg, ErrUnsuccessful
			}
		}
		response, err := client.GetCommandExecution(ctx, connect.NewRequest(&pb.GetCommandExecutionRequest{InstanceId: instanceID, CommandId: id}))
		if err != nil && !deleting {
			return nil, err
		}
		if err == nil {
			if response.Msg.GetExecution().GetRequest().GetCommand() == DeleteCommand {
				deleting = true
			}
			switch response.Msg.GetExecution().GetStatus() {
			case pb.CommandStatus_COMMAND_STATUS_SUCCEEDED:
				if !deleting {
					return response.Msg, nil
				}
			case pb.CommandStatus_COMMAND_STATUS_FAILED, pb.CommandStatus_COMMAND_STATUS_INTERRUPTED:
				return response.Msg, ErrUnsuccessful
			case pb.CommandStatus_COMMAND_STATUS_UNSPECIFIED, pb.CommandStatus_COMMAND_STATUS_QUEUED, pb.CommandStatus_COMMAND_STATUS_RUNNING:
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

const (
	commandStdout = "command execution stdout"
	commandStderr = "command execution stderr"
	DeleteCommand = "builtin.delete"
)
