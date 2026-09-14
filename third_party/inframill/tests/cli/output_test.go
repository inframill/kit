package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/sir-hassan/inframill/internal/cli/localexec"
	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
)

// SPEC-0000 REQ-015: flags select output under captured stdout without a TTY.
func TestCLIOutputSelection(t *testing.T) {
	f := &fixture{}
	endpoint := serveFixture(t, f)
	for _, flags := range [][]string{nil, {outputFlag, "table"}, {"-o=table"}, {"--output=json"}, {"-o", jsonFormat}} {
		args := append([]string{argSystem, argList, endpointFlag, endpoint}, flags...)
		got := invoke(t, t.Context(), t.TempDir(), "", args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatal(got)
		}
		if strings.Contains(strings.Join(flags, " "), jsonFormat) {
			var response pb.ListCommandDefinitionsResponse
			if err := protojson.Unmarshal([]byte(got.stdout), &response); err != nil || len(response.GetCommands()) != 1 || response.GetCommands()[0].GetName() != qualifiedPlan {
				t.Fatal(got, err)
			}
		} else if !strings.Contains(got.stdout, "| NAME") || !strings.Contains(got.stdout, "SAFETY") || !strings.Contains(got.stdout, qualifiedPlan) {
			t.Fatal(got)
		}
	}
	got := invoke(t, t.Context(), t.TempDir(), "", argCommand, "execution", "get", "-o", jsonFormat, instanceID, endpointFlag, endpoint, commandID)
	var response pb.GetCommandExecutionResponse
	if err := protojson.Unmarshal([]byte(got.stdout), &response); err != nil || got.code != 0 || response.GetExecution().GetRequest().GetId() != commandID {
		t.Fatal(got, err)
	}
}

// SPEC-0000 REQ-015: invalid output cannot admit work or open workspaces.
func TestOutputRejectedBeforeEffects(t *testing.T) {
	f := &fixture{}
	endpoint := serveFixture(t, f)
	for _, flags := range [][]string{{outputFlag, "yaml"}, {"-o", ""}, {"--output="}, {"--json"}, {outputFlag}} {
		args := append([]string{argInstance, localExecCommand, instanceID, qualifiedPlan, endpointFlag, endpoint}, flags...)
		got := invoke(t, t.Context(), t.TempDir(), "", args...)
		if got.code != 2 || got.stdout != "" {
			t.Fatal(got)
		}
	}
	got := invoke(t, t.Context(), t.TempDir(), "", argCommand, "execution", stdoutArgument, instanceID, commandID, endpointFlag, endpoint, outputFlag, "table")
	if got.code != 2 || got.stdout != "" {
		t.Fatal(got)
	}
	f.mu.Lock()
	count := len(f.requests)
	f.mu.Unlock()
	if count != 0 {
		t.Fatal("invalid output reached RPC", count)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "unopened")
	for _, args := range [][]string{
		{historyArgument, workspaceFlag, workspace, "-o", "yaml"},
		{historyArgument, workspaceFlag, workspace, "--output="},
		{historyArgument, workspaceFlag, workspace, "--json"},
		{localExecCommand, qualifiedPlan, "-o", jsonFormat},
		{argServer, outputFlag, jsonFormat},
		{argPackage, argBuild, "source", "output", outputFlag, jsonFormat},
	} {
		got = invoke(t, t.Context(), root, "", args...)
		if got.code != 2 || got.stdout != "" {
			t.Fatal(got)
		}
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("invalid output opened workspace", err)
	}
}

// SPEC-0000 REQ-015: log readers preserve raw bytes and the complete JSON envelope.
func TestLogOutputFormats(t *testing.T) {
	endpoint := serveFixture(t, &fixture{})
	for _, stream := range []string{stdoutArgument, "stderr"} {
		args := make([]string, 0, 8)
		args = append(args, argCommand, "execution", stream, instanceID, commandID, endpointFlag, endpoint)
		got := invoke(t, t.Context(), t.TempDir(), "", args...)
		if got.code != 0 || got.stdout != fixtureLogText || got.stderr != "" {
			t.Fatal(got)
		}
		got = invoke(t, t.Context(), t.TempDir(), "", append(args, "-o", jsonFormat)...)
		var response pb.ReadCommandLogResponse
		if err := protojson.Unmarshal([]byte(got.stdout), &response); err != nil || got.code != 0 || string(response.GetData()) != fixtureLogText || response.GetNextOffset() != 11 || !response.GetEof() {
			t.Fatal(got, err)
		}
	}
}

// SPEC-0008 REQ-001: abbreviated IDs, failed waits and verified deletion remain visible in table mode.
func TestWaitTables(t *testing.T) {
	endpoint := serveFixture(t, &fixture{failed: true})
	for _, args := range [][]string{{argCommand, "execution", "wait", instanceID, commandID}, {argInstance, localExecCommand, instanceID, qualifiedPlan, "--wait"}} {
		got := invoke(t, t.Context(), t.TempDir(), "", append(args, endpointFlag, endpoint)...)
		if got.code != 1 || !strings.Contains(got.stdout, commandID[:17]+"...") || !strings.Contains(got.stdout, "fixture failure") || !strings.Contains(got.stdout, "failed") {
			t.Fatal(got)
		}
	}
	endpoint = serveFixture(t, &fixture{deleted: true})
	got := invoke(t, t.Context(), t.TempDir(), "", argInstance, "delete", instanceID, "--wait", endpointFlag, endpoint)
	if got.code != 0 || !strings.Contains(got.stdout, instanceID[:17]+"...") || !strings.Contains(got.stdout, commandID[:17]+"...") || !strings.Contains(got.stdout, "Deleted") || !strings.Contains(got.stdout, "true") {
		t.Fatal(got)
	}
}

type outputFailure struct{}

func (outputFailure) Write([]byte) (int, error) { return 0, errors.New("closed output") }

// SPEC-0000 REQ-015: local history formats, pagination and errors.
func TestLocalHistoryOutput(t *testing.T) {
	f := localFiles(t, "success")
	for range 2 {
		got := localRun(t, f.command(t.Context(), f.args("debug.echo", "")...))
		if got.code != 0 {
			t.Fatal(got)
		}
	}
	args := []string{historyArgument, workspaceFlag, f.workspace, "--limit", "1"}
	got := localRun(t, f.command(t.Context(), append(args, "-o", jsonFormat)...))
	var page struct {
		Executions []localResult `json:"executions"`
		NextCursor string        `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &page); err != nil || got.code != 0 || len(page.Executions) != 1 || page.NextCursor == "" {
		t.Fatal(got, err)
	}
	first := page.Executions[0]
	if first.Version != "inframill.dev/local-result/v1" || first.Instance == "" || first.Command == "" || len(first.Result) == 0 {
		t.Fatal(first)
	}
	got = localRun(t, f.command(t.Context(), args...))
	if got.code != 0 || !strings.Contains(got.stdout, first.Command[:17]+"...") || !strings.Contains(got.stdout, "Next cursor: "+page.NextCursor) || !strings.Contains(got.stdout, "COMMAND UUID") {
		t.Fatal(got)
	}
	got = localRun(t, f.command(t.Context(), append(args, "--cursor", page.NextCursor, "--output=table")...))
	if got.code != 0 || strings.Contains(got.stdout, first.Command[:17]+"...") || strings.Contains(got.stdout, "Next cursor:") {
		t.Fatal(got)
	}
	detail := make([]string, 0, 7)
	detail = append(detail, historyArgument, workspaceFlag, f.workspace, "--command", first.Command)
	got = localRun(t, f.command(t.Context(), detail...))
	if got.code != 0 || !strings.Contains(got.stdout, first.Instance[:17]+"...") || !strings.Contains(got.stdout, "Result:\n{") || !strings.Contains(got.stdout, "values") {
		t.Fatal(got)
	}
	got = localRun(t, f.command(t.Context(), append(detail, outputFlag, jsonFormat)...))
	var record localResult
	if err := json.Unmarshal([]byte(got.stdout), &record); err != nil || got.code != 0 || record.Command != first.Command || record.Version != first.Version {
		t.Fatal(got, err)
	}
	for _, format := range []string{"table", jsonFormat} {
		err := localexec.History(t.Context(), localexec.HistoryOptions{Workspace: f.workspace, Name: "local/default", Limit: 50, Output: format}, outputFailure{}, io.Discard)
		var failure *localexec.ExitError
		if !errors.As(err, &failure) || failure.Code != 3 {
			t.Fatal("output failure exit", err)
		}
	}
}

// SPEC-0000 REQ-015: exercise actual list dispatch as well as the renderer.
func (f *fixture) ListInstances(_ context.Context, r *connect.Request[pb.ListInstancesRequest]) (*connect.Response[pb.ListInstancesResponse], error) {
	f.record(r.Msg)
	return connect.NewResponse(&pb.ListInstancesResponse{Instances: []*pb.Instance{{Id: instanceID, Name: testInstanceName}}, NextCursor: "next-page"}), nil
}

func TestInstanceListTable(t *testing.T) {
	endpoint := serveFixture(t, &fixture{})
	got := invoke(t, t.Context(), t.TempDir(), "", argInstance, argList, endpointFlag, endpoint)
	if got.code != 0 || !strings.Contains(got.stdout, instanceID[:17]+"...") || !strings.Contains(got.stdout, testInstanceName) || !strings.Contains(got.stdout, "Next cursor: next-page") {
		t.Fatal(got)
	}
}

const (
	jsonFormat      = "json"
	historyArgument = "history"
	workspaceFlag   = "--workspace"
	outputFlag      = "--output"
	endpointFlag    = "--endpoint"
	fixtureLogText  = "fixture log"
	stdoutArgument  = "stdout"
)
