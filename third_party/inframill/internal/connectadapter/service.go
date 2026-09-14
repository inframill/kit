package connectadapter

import (
	"context"
	"errors"
	"io"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func (s *service) InitInstance(ctx context.Context, r *connect.Request[pb.InitInstanceRequest]) (*connect.Response[pb.InitInstanceResponse], error) {
	input := r.Msg.GetInstance()
	if input == nil || input.GetValues() == nil {
		return nil, invalid("instance with values is required")
	}
	info, err := s.engine.InitInstance(ctx, r.Msg.GetName(), inframill.InstanceConfig{Blueprint: input.GetBlueprint(), Values: input.GetValues().AsMap()})
	if err != nil {
		return nil, rpcError(err)
	}
	out, err := instanceProto(info)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&pb.InitInstanceResponse{Instance: out}), nil
}

func (s *service) SubmitCommand(ctx context.Context, r *connect.Request[pb.SubmitCommandRequest]) (*connect.Response[pb.SubmitCommandResponse], error) {
	id, err := s.engine.SubmitCommand(ctx, inframill.InstanceID(r.Msg.GetInstanceId()), r.Msg.GetCommand(), r.Msg.GetArgs().AsMap(), r.Msg.GetIdempotencyKey())
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&pb.SubmitCommandResponse{CommandId: string(id)}), nil
}

func (s *service) GetCommandExecution(ctx context.Context, r *connect.Request[pb.GetCommandExecutionRequest]) (*connect.Response[pb.GetCommandExecutionResponse], error) {
	c, err := s.engine.GetCommandExecution(ctx, inframill.InstanceID(r.Msg.GetInstanceId()), inframill.CommandID(r.Msg.GetCommandId()))
	if err != nil {
		return nil, rpcError(err)
	}
	out, err := commandProto(c)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&pb.GetCommandExecutionResponse{Execution: out, ObservedAt: timestamppb.Now()}), nil
}

func (s *service) ListCommandExecutions(ctx context.Context, r *connect.Request[pb.ListCommandExecutionsRequest]) (*connect.Response[pb.ListCommandExecutionsResponse], error) {
	status, err := statusDomain(r.Msg.GetStatus())
	if err != nil {
		return nil, err
	}
	page, err := s.engine.ListCommandExecutions(ctx, inframill.InstanceID(r.Msg.GetInstanceId()), inframill.CommandExecutionQuery{Status: status, Limit: int(r.Msg.GetLimit()), Cursor: r.Msg.GetCursor()})
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListCommandExecutionsResponse{NextCursor: page.NextCursor, ObservedAt: timestamp(page.ObservedAt)}
	for _, c := range page.Executions {
		v, err := commandProto(c)
		if err != nil {
			return nil, rpcError(err)
		}
		out.Executions = append(out.Executions, v)
	}
	return connect.NewResponse(out), nil
}

func (s *service) GetInstance(ctx context.Context, r *connect.Request[pb.GetInstanceRequest]) (*connect.Response[pb.GetInstanceResponse], error) {
	info, err := s.engine.GetInstance(ctx, inframill.InstanceID(r.Msg.GetInstanceId()))
	if err != nil {
		return nil, rpcError(err)
	}
	out, err := instanceProto(info)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&pb.GetInstanceResponse{Instance: out, ObservedAt: timestamp(time.Now().UTC())}), nil
}

func (s *service) ListInstances(ctx context.Context, r *connect.Request[pb.ListInstancesRequest]) (*connect.Response[pb.ListInstancesResponse], error) {
	page, err := s.engine.ListInstances(ctx, inframill.InstanceQuery{Project: r.Msg.GetProject(), Limit: int(r.Msg.GetLimit()), Cursor: r.Msg.GetCursor()})
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListInstancesResponse{NextCursor: page.NextCursor, ObservedAt: timestamp(page.ObservedAt)}
	for _, info := range page.Instances {
		v, err := instanceProto(info)
		if err != nil {
			return nil, rpcError(err)
		}
		out.Instances = append(out.Instances, v)
	}
	return connect.NewResponse(out), nil
}

func (s *service) FetchBlueprint(ctx context.Context, r *connect.Request[pb.FetchBlueprintRequest]) (*connect.Response[pb.FetchBlueprintResponse], error) {
	b, err := s.engine.FetchBlueprint(ctx, r.Msg.GetReference())
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&pb.FetchBlueprintResponse{Blueprint: blueprintProto(b)}), nil
}

func (s *service) ListBlueprints(ctx context.Context, _ *connect.Request[pb.ListBlueprintsRequest]) (*connect.Response[pb.ListBlueprintsResponse], error) {
	entries, err := s.engine.Blueprints(ctx)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListBlueprintsResponse{}
	for _, b := range entries {
		out.Blueprints = append(out.Blueprints, blueprintProto(b))
	}
	return connect.NewResponse(out), nil
}

func (s *service) ListCommandDefinitions(ctx context.Context, _ *connect.Request[pb.ListCommandDefinitionsRequest]) (*connect.Response[pb.ListCommandDefinitionsResponse], error) {
	if err := ctx.Err(); err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListCommandDefinitionsResponse{}
	for _, d := range s.engine.ListCommandDefinitions() {
		schema, err := objectProto(d.ArgumentsSchema)
		if err != nil {
			return nil, rpcError(err)
		}
		out.Commands = append(out.Commands, &pb.CommandDefinition{Name: d.Name, Description: d.Description, ArgumentsSchema: schema, Safety: safetyProto(d.Safety), Effects: d.Effects, Verification: d.Verification})
	}
	return connect.NewResponse(out), nil
}

func (s *service) ReadCommandLog(ctx context.Context, r *connect.Request[pb.ReadCommandLogRequest]) (*connect.Response[pb.ReadCommandLogResponse], error) {
	if err := ctx.Err(); err != nil {
		return nil, rpcError(err)
	}
	limit := r.Msg.GetLimit()
	if limit == 0 {
		limit = 64 << 10
	}
	if limit < 1 || limit > 1<<20 || r.Msg.GetOffset() < 0 {
		return nil, invalid("invalid log offset or limit")
	}
	var reader io.ReadCloser
	var err error
	switch r.Msg.GetStream() {
	case pb.LogStream_LOG_STREAM_UNSPECIFIED:
		return nil, invalid("log stream is required")
	case pb.LogStream_LOG_STREAM_STDOUT:
		reader, err = s.engine.Stdout(inframill.InstanceID(r.Msg.GetInstanceId()), inframill.CommandID(r.Msg.GetCommandId()))
	case pb.LogStream_LOG_STREAM_STDERR:
		reader, err = s.engine.Stderr(inframill.InstanceID(r.Msg.GetInstanceId()), inframill.CommandID(r.Msg.GetCommandId()))
	default:
		return nil, invalid("log stream is required")
	}
	if err != nil {
		return nil, rpcError(err)
	}
	defer reader.Close() //nolint:errcheck // Read-only log snapshot.
	if _, checkErr := io.CopyN(io.Discard, reader, r.Msg.GetOffset()); checkErr != nil {
		if errors.Is(checkErr, io.EOF) {
			return nil, invalid("offset exceeds current log size")
		}
		return nil, rpcError(checkErr)
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, rpcError(err)
	}
	eof := len(data) <= int(limit)
	if !eof {
		data = data[:limit]
	}
	return connect.NewResponse(&pb.ReadCommandLogResponse{Data: data, NextOffset: r.Msg.GetOffset() + int64(len(data)), Eof: eof}), nil
}

func (s *service) ListDrivers(ctx context.Context, _ *connect.Request[pb.ListDriversRequest]) (*connect.Response[pb.ListDriversResponse], error) {
	entries, err := s.engine.ListDrivers(ctx)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListDriversResponse{}
	for _, entry := range entries {
		out.Drivers = append(out.Drivers, &pb.DriverInfo{Id: entry.ID, Name: entry.Name, Version: entry.Version, Status: driverStatusProto(entry.Status), Reason: entry.Reason, CheckedAt: timestamp(entry.CheckedAt)})
	}
	return connect.NewResponse(out), nil
}
