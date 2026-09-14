package connectadapter

import (
	"encoding/json"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func objectProto(value any) (*structpb.Struct, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	return structpb.NewStruct(object)
}

func timestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}

func commandProto(c inframill.CommandExecution) (*pb.CommandExecution, error) {
	args, err := objectProto(c.Request.Args)
	if err != nil {
		return nil, err
	}
	result := &pb.CommandExecution{Request: &pb.CommandRequest{Id: string(c.Request.ID), Command: c.Request.Command, Args: args, IdempotencyKey: c.Request.IdempotencyKey}, Status: statusProto(c.Status), CreatedAt: timestamp(c.CreatedAt)}
	if c.StartedAt != nil {
		result.StartedAt = timestamp(*c.StartedAt)
	}
	if c.EndedAt != nil {
		result.EndedAt = timestamp(*c.EndedAt)
	}
	if c.Error != nil {
		result.Error = &pb.Failure{Code: string(c.Error.Code), Message: c.Error.Message, Path: c.Error.Path}
	}
	if len(c.Result) > 0 {
		var value any
		if checkErr := json.Unmarshal(c.Result, &value); checkErr != nil {
			return nil, checkErr
		}
		result.Result, err = structpb.NewValue(value)
	}
	return result, err
}

func instanceProto(info inframill.InstanceInfo) (*pb.Instance, error) {
	values, err := objectProto(info.Values)
	if err != nil {
		return nil, err
	}
	result := &pb.Instance{Id: string(info.ID), Name: info.Name, Blueprint: info.Blueprint, Values: values, Status: instanceStatusProto(info.Status)}
	if info.Failure != nil {
		result.Failure = &pb.Failure{Code: string(info.Failure.Code), Message: info.Failure.Message}
	}
	return result, nil
}

func blueprintProto(b inframill.Blueprint) *pb.BlueprintInfo {
	return &pb.BlueprintInfo{Repository: b.Repository, Reference: b.Reference, Digest: b.Digest, Tags: b.Tags, ApiVersion: b.APIVersion, Description: b.Description, Directory: b.Directory}
}

var statuses = map[inframill.CommandStatus]pb.CommandStatus{
	inframill.Queued:      pb.CommandStatus_COMMAND_STATUS_QUEUED,
	inframill.Running:     pb.CommandStatus_COMMAND_STATUS_RUNNING,
	inframill.Succeeded:   pb.CommandStatus_COMMAND_STATUS_SUCCEEDED,
	inframill.Failed:      pb.CommandStatus_COMMAND_STATUS_FAILED,
	inframill.Interrupted: pb.CommandStatus_COMMAND_STATUS_INTERRUPTED,
}

func statusProto(s inframill.CommandStatus) pb.CommandStatus { return statuses[s] }
func statusDomain(s pb.CommandStatus) (inframill.CommandStatus, error) {
	if s == pb.CommandStatus_COMMAND_STATUS_UNSPECIFIED {
		return "", nil
	}
	for k, v := range statuses {
		if v == s {
			return k, nil
		}
	}
	return "", invalid("unknown command status")
}

func instanceStatusProto(s inframill.InstanceStatus) pb.InstanceStatus {
	switch s {
	case inframill.InstanceIdle:
		return pb.InstanceStatus_INSTANCE_STATUS_IDLE
	case inframill.InstanceRunning:
		return pb.InstanceStatus_INSTANCE_STATUS_RUNNING
	case inframill.InstanceFailed:
		return pb.InstanceStatus_INSTANCE_STATUS_FAILED
	default:
		return pb.InstanceStatus_INSTANCE_STATUS_UNSPECIFIED
	}
}

func safetyProto(s inframill.SafetyClass) pb.SafetyClass {
	switch s {
	case inframill.ReadOnly:
		return pb.SafetyClass_SAFETY_CLASS_READ_ONLY
	case inframill.Reversible:
		return pb.SafetyClass_SAFETY_CLASS_REVERSIBLE
	case inframill.Mutating:
		return pb.SafetyClass_SAFETY_CLASS_MUTATING
	case inframill.Destructive:
		return pb.SafetyClass_SAFETY_CLASS_DESTRUCTIVE
	default:
		return pb.SafetyClass_SAFETY_CLASS_UNSPECIFIED
	}
}

func driverStatusProto(status inframill.DriverStatus) pb.DriverStatus {
	switch status {
	case inframill.DriverConnected:
		return pb.DriverStatus_DRIVER_STATUS_CONNECTED
	case inframill.DriverOffline:
		return pb.DriverStatus_DRIVER_STATUS_OFFLINE
	default:
		return pb.DriverStatus_DRIVER_STATUS_UNSPECIFIED
	}
}
