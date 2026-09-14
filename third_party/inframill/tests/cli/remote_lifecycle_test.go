package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

// SPEC-0002 REQ-007/008 / AC-006: interrupting a real CLI wait leaves exactly one
// admitted command running in the disposable daemon; deletion proves absence.
func verifyRemoteWaitCancellation(t *testing.T, ctx context.Context, f localFixture, client inframillv1connect.EngineServiceClient, endpoint, id string) {
	t.Helper()
	command := f.command(ctx, argInstance, localExecCommand, id, "debug.wait", "--wait", "--idempotency-key=single-admission", endpointFlag, endpoint)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() }) //nolint:errcheck // The child normally exited after the explicit interruption.
	var admitted string
	for admitted == "" {
		page, err := client.ListCommandExecutions(ctx, connect.NewRequest(&pb.ListCommandExecutionsRequest{InstanceId: id}))
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Msg.GetExecutions() {
			if item.GetRequest().GetCommand() == "debug.wait" && item.GetStatus() == pb.CommandStatus_COMMAND_STATUS_RUNNING {
				admitted = item.GetRequest().GetId()
			}
		}
		if admitted != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("wait command did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitErr := command.Wait()
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 130 {
		t.Fatal(waitErr, stdout.String(), stderr.String())
	}
	running, err := client.GetCommandExecution(ctx, connect.NewRequest(&pb.GetCommandExecutionRequest{InstanceId: id, CommandId: admitted}))
	if err != nil || running.Msg.GetExecution().GetStatus() != pb.CommandStatus_COMMAND_STATUS_RUNNING {
		t.Fatal(running, err)
	}
	release := filepath.Join(f.workspace, "instances", id, "workspace", "debug", "release")
	if writeErr := os.WriteFile(release, []byte("release"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	completed := localRun(t, f.command(ctx, argCommand, "execution", "wait", id, admitted, endpointFlag, endpoint, outputFlag, jsonFormat))
	if completed.code != 0 || !strings.Contains(completed.stdout, "COMMAND_STATUS_SUCCEEDED") {
		t.Fatal(completed)
	}
	page, err := client.ListCommandExecutions(ctx, connect.NewRequest(&pb.ListCommandExecutionsRequest{InstanceId: id}))
	if err != nil || len(page.Msg.GetExecutions()) != 2 {
		t.Fatal("submission repeated", page, err)
	}
	deleted := localRun(t, f.command(ctx, argInstance, "delete", id, "--wait", endpointFlag, endpoint, outputFlag, jsonFormat))
	if deleted.code != 0 || !strings.Contains(deleted.stdout, `"deleted":true`) {
		t.Fatal(deleted)
	}
	if _, getErr := client.GetInstance(ctx, connect.NewRequest(&pb.GetInstanceRequest{InstanceId: id})); connect.CodeOf(getErr) != connect.CodeNotFound {
		t.Fatal("deletion did not verify absence", getErr)
	}
}
