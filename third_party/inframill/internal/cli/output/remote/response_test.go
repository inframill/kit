package remote

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	clioutput "github.com/sir-hassan/inframill/internal/cli/output"
	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
)

// SPEC-0000 REQ-015: every remote response retains its meaning in both formats.
func TestResponseFormats(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	// SPEC-0008: table cells abbreviate, JSON below still round-trips exactly.
	const shortID = "00000000-0000-400..."
	const shortReference = "registry/cloud@sh..."
	const when = "2026-09-11T12:00:00Z"
	stamp := timestamppb.New(time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC))
	nested := &structpb.Struct{Fields: map[string]*structpb.Value{"nested_key": structpb.NewStringValue("preserved")}}
	blueprint := &pb.BlueprintInfo{Reference: "registry/cloud@sha256:full-digest", Repository: "registry/cloud", Description: exampleDescription, Tags: []string{"stable", "test"}, Digest: "sha256:full-digest", ApiVersion: "v1", Directory: "/package"}
	instance := &pb.Instance{Id: id, Name: exampleName, Blueprint: blueprint.GetReference(), Values: nested, Status: pb.InstanceStatus_INSTANCE_STATUS_FAILED, Failure: &pb.Failure{Code: "storage", Message: "disk failure", Path: "/workspace"}}
	definition := &pb.CommandDefinition{Name: "debug.secret", Safety: pb.SafetyClass_SAFETY_CLASS_MUTATING, Description: "Plan changes", Effects: []string{"writes plan"}, Verification: "checks exit", ArgumentsSchema: nested}
	command := &pb.CommandExecution{Request: &pb.CommandRequest{Id: id, Command: definition.GetName(), Args: nested, IdempotencyKey: "retry-key"}, Status: pb.CommandStatus_COMMAND_STATUS_FAILED, CreatedAt: stamp, StartedAt: stamp, EndedAt: stamp, Result: structpb.NewStructValue(nested), Error: &pb.Failure{Code: "command_failed", Message: "fixture failure", Path: "plan"}}
	deletion := &structpb.Struct{Fields: map[string]*structpb.Value{"instanceId": structpb.NewStringValue(id), "commandId": structpb.NewStringValue(id), "deleted": structpb.NewBoolValue(true)}}
	for _, test := range []struct {
		name     string
		response proto.Message
		want     []string
	}{
		{instancesPath, &pb.ListInstancesResponse{Instances: []*pb.Instance{instance}, NextCursor: "cursor+/=", ObservedAt: stamp}, []string{"NAME", shortID, exampleName, failedStatus, shortReference, "Next cursor: cursor+/=", "Observed at: " + when}},
		{"blueprints", &pb.ListBlueprintsResponse{Blueprints: []*pb.BlueprintInfo{blueprint}}, []string{"REFERENCE", shortReference, exampleDescription}},
		{"commands", &pb.ListCommandDefinitionsResponse{Commands: []*pb.CommandDefinition{definition}}, []string{"SAFETY", definition.GetName(), "mutating", "Plan changes"}},
		{"commands", &pb.ListCommandExecutionsResponse{Executions: []*pb.CommandExecution{command}, NextCursor: "command-cursor", ObservedAt: stamp}, []string{"COMMAND UUID", shortID, definition.GetName(), failedStatus, when, "Next cursor: command-cursor"}},
		{"instance", &pb.GetInstanceResponse{Instance: instance, ObservedAt: stamp}, []string{"FIELD", shortID, "disk failure", "/workspace", "Values:\n{\n  \"nested_key\": \"preserved\"\n}", when}},
		{"init", &pb.InitInstanceResponse{Instance: instance}, []string{shortID, exampleName, "Values:"}},
		{"fetch", &pb.FetchBlueprintResponse{Blueprint: blueprint}, []string{shortReference, "stable, test", "/package"}},
		{"blueprint", blueprint, []string{shortReference, "sha256:full-digest", exampleDescription}},
		{"definition", definition, []string{"Arguments schema:\n{", "writes plan", "checks exit", "mutating"}},
		{"admission", &pb.SubmitCommandResponse{CommandId: id}, []string{"Command UUID", shortID}},
		{"command", &pb.GetCommandExecutionResponse{Execution: command, ObservedAt: stamp}, []string{shortID, failedStatus, "retry-key", "fixture failure", "Args:\n{", "Result:\n{", when}},
		{"deletion", deletion, []string{shortID, "Deleted", "true"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var table, repeated, data bytes.Buffer
			if err := Write(&table, "", test.response); err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(table.String(), want) {
					t.Fatalf("missing %q:\n%s", want, &table)
				}
			}
			if err := Write(&repeated, clioutput.Table, test.response); err != nil {
				t.Fatal(err)
			}
			if table.String() != repeated.String() || strings.Contains(table.String(), "\x1b") {
				t.Fatal("unstable or colored table", table.String())
			}
			if err := Write(&data, clioutput.JSON, test.response); err != nil {
				t.Fatal(err)
			}
			decoded := test.response.ProtoReflect().Type().New().Interface()
			if err := protojson.Unmarshal(data.Bytes(), decoded); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(test.response, decoded) {
				t.Fatalf("JSON lost fields: %s", &data)
			}
		})
	}
}

// SPEC-0000 REQ-015: empty pages are explicit and source ordering is preserved.
func TestEmptyPagesAndOrder(t *testing.T) {
	for _, test := range []struct {
		response proto.Message
		want     string
	}{
		{&pb.ListInstancesResponse{}, "No instances found."},
		{&pb.ListBlueprintsResponse{}, "No blueprints found."},
		{&pb.ListCommandDefinitionsResponse{}, "No commands found."},
		{&pb.ListCommandExecutionsResponse{}, "No command executions found."},
	} {
		var out bytes.Buffer
		if err := Write(&out, "", test.response); err != nil || out.String() != test.want+"\n" {
			t.Fatal(out.String(), err)
		}
	}
	var out bytes.Buffer
	response := &pb.ListCommandDefinitionsResponse{Commands: []*pb.CommandDefinition{{Name: "z-first"}, {Name: "a-second"}}}
	if err := Write(&out, "", response); err != nil {
		t.Fatal(err)
	}
	if strings.Index(out.String(), "z-first") >= strings.Index(out.String(), "a-second") {
		t.Fatal(out.String())
	}
}

type failedWriter struct{ err error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.err }

// SPEC-0000 REQ-015: failed writers are reported in both formats.
func TestOutputFailures(t *testing.T) {
	want := errors.New("closed output")
	for _, format := range []string{clioutput.Table, clioutput.JSON} {
		if err := Write(failedWriter{want}, format, &pb.SubmitCommandResponse{CommandId: "command"}); !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}

const (
	exampleDescription = "Example cloud"
	exampleName        = "project/dev"
	failedStatus       = "failed"
)

// SPEC-0002 REQ-006 / AC-005: stale paths and formats reject before rendering.
func TestREQ006DescriptorValidation(t *testing.T) {
	descriptor := (&pb.ListInstancesResponse{}).ProtoReflect().Descriptor()
	for _, definition := range []Definition{
		{Rows: "absent"},
		{Rows: "next_cursor"},
		{Rows: instancesPath, Fields: []Field{{Key: badColumn, Path: "absent"}}},
		{Rows: instancesPath, Fields: []Field{{Key: badColumn, Path: namePath, Format: Timestamp}}},
		{Rows: instancesPath, Fields: []Field{{Key: badColumn, Path: "values"}}},
		{Rows: instancesPath, Fields: []Field{{Key: badColumn, Path: namePath, Format: Enum}}},
		{Rows: instancesPath, Fields: []Field{{Key: badColumn, Path: namePath, Format: Lines}}},
		{Rows: instancesPath, Sections: []Section{{Path: namePath}}},
		{NextCursor: "observed_at"},
		{ObservedAt: "next_cursor"},
	} {
		if err := definition.Validate(descriptor); err == nil {
			t.Fatal("invalid definition accepted", definition)
		}
	}
	response := &pb.ListInstancesResponse{Instances: []*pb.Instance{{Name: "test/dev", Id: "full-id"}}}
	for _, columns := range []string{"", "name,name", "unknown"} {
		if err := Validate(response, columns, true); err == nil {
			t.Fatal(columns)
		}
	}
	var out bytes.Buffer
	if err := WriteColumns(&out, clioutput.JSON, response, "unknown", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "test/dev") {
		t.Fatal(out.String())
	}
	out.Reset()
	// Unconfigured message shape still retains every descriptor field.
	if err := Write(&out, clioutput.Table, &pb.ReadCommandLogResponse{Data: []byte{0xff}, NextOffset: 9, Eof: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/w==", "nextOffset", "9", "eof", "true"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(out.String())
		}
	}
}

const (
	instancesPath = "instances"
	badColumn     = "bad"
)
