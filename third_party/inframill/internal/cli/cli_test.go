package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

type testClient struct {
	inframillv1connect.EngineServiceClient
	requests    []*pb.ListInstancesRequest
	submissions []*pb.SubmitCommandRequest
	endpoint    string
	calls       int
	failure     error
	logs        []byte
}

func (c *testClient) ListInstances(_ context.Context, r *connect.Request[pb.ListInstancesRequest]) (*connect.Response[pb.ListInstancesResponse], error) {
	c.calls++
	c.requests = append(c.requests, r.Msg)
	if c.failure != nil {
		return nil, c.failure
	}
	return connect.NewResponse(&pb.ListInstancesResponse{Instances: []*pb.Instance{{Id: "full-uuid", Name: "test/dev", Status: pb.InstanceStatus_INSTANCE_STATUS_IDLE}}, NextCursor: "next-full-cursor"}), nil
}

func (c *testClient) SubmitCommand(_ context.Context, r *connect.Request[pb.SubmitCommandRequest]) (*connect.Response[pb.SubmitCommandResponse], error) {
	c.calls++
	c.submissions = append(c.submissions, r.Msg)
	return connect.NewResponse(&pb.SubmitCommandResponse{CommandId: "command-uuid"}), nil
}

func (c *testClient) ReadCommandLog(_ context.Context, _ *connect.Request[pb.ReadCommandLogRequest]) (*connect.Response[pb.ReadCommandLogResponse], error) {
	c.calls++
	return connect.NewResponse(&pb.ReadCommandLogResponse{Data: c.logs}), nil
}

func invoke(t *testing.T, client *testClient, env []string, terminal bool, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), args, Dependencies{Environment: env, Stdout: &stdout, Stderr: &stderr, Version: "1.2.3", Client: func(endpoint string) inframillv1connect.EngineServiceClient {
		client.endpoint = endpoint
		return client
	}, Terminal: func(io.Writer) bool { return terminal }})
	return code, stdout.String(), stderr.String()
}

func config(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// SPEC-0002 REQ-002/003 / AC-002: help and static completion never initialize a client.
func TestREQ003OfflineUtilities(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{helpFlag}, {instanceCommand, listCommand, helpFlag}, {versionCommand}, {"--version"}, {completionCommand, bashShell}, {completionCommand, zshShell}, {completionCommand, fishShell}, {completionCommand, powershellShell}, {"__complete", instanceCommand, listCommand, "--output", ""}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			client := &testClient{}
			code, out, errout := invoke(t, client, []string{"INFRAMILL_ENDPOINT=invalid", "INFRAMILL_TIMEOUT=invalid"}, false, args...)
			if code != 0 || out == "" || client.endpoint != "" {
				t.Fatalf("%d %s %s client=%s", code, out, errout, client.endpoint)
			}
			if args[0] == versionCommand || args[0] == "--version" {
				if out != "1.2.3\n" {
					t.Fatal(out)
				}
			}
		})
	}
	client := &testClient{}
	for _, args := range [][]string{nil, {privateSentinel}, {instanceCommand, privateSentinel}, {instanceCommand, listCommand, "--limit=private-sentinel"}, {instanceCommand, listCommand, "--private-sentinel"}} {
		code, _, errout := invoke(t, client, nil, false, args...)
		if code != 2 || strings.Contains(errout, privateSentinel) || client.endpoint != "" {
			t.Fatal(code, errout)
		}
	}
}

// SPEC-0002 REQ-004/006/008/013 / AC-003/005/009; SPEC-0010 REQ-004: remote isolation.
func TestREQ004PrecedenceAndColumns(t *testing.T) {
	t.Chdir(t.TempDir())
	path := config(t, "handlers: [{settings: {private: '${MISSING}'}}]\nengine: {dataDir: '${MISSING_ENGINE}', drivers: invalid}\ncli: {endpoint: 'http://config.test', timeout: invalid, output: table, color: auto}")
	client := &testClient{}
	code, out, errout := invoke(t, client, []string{"INFRAMILL_ENDPOINT=http://env.test", "INFRAMILL_TIMEOUT=bad"}, false, instanceCommand, "--config", path, "--endpoint", "http://flag.test", listCommand, "--timeout=1m", "--project=demo", "--limit=7", "--columns=id,name")
	if code != 0 || errout != "" || client.endpoint != "http://flag.test" || len(client.requests) != 1 || client.requests[0].GetLimit() != 7 || client.requests[0].GetProject() != "demo" {
		t.Fatal(code, out, errout, client)
	}
	if strings.Index(out, "INSTANCE UUID") > strings.Index(out, "NAME") || strings.Contains(out, "STATUS") || !strings.Contains(out, "next-full-cursor") {
		t.Fatal(out)
	}
	for _, flags := range [][]string{{"--columns="}, {"--columns=name,name"}, {"--columns=unknown"}, {"--columns=name", "-o=json"}, {"--timeout="}, {"--endpoint="}, {"--output="}, {"--color=invalid"}, {"--config="}, {"--config=missing"}, {"--confirm"}, {"--pager=cat"}, {"--pager-arg=argument"}} {
		before := client.calls
		args := append([]string{instanceCommand, listCommand}, flags...)
		code, _, errout = invoke(t, client, nil, false, args...)
		if code != 2 || client.calls != before {
			t.Fatal(flags, code, errout, client.calls)
		}
	}
	for _, tc := range []struct {
		env  []string
		path string
		want string
		code int
	}{
		{nil, config(t, "cli: {endpoint: 'http://config.test'}"), "http://config.test", 0},
		{[]string{"INFRAMILL_ENDPOINT=http://env.test"}, config(t, "cli: {endpoint: invalid}"), "http://env.test", 0},
		{[]string{"INFRAMILL_ENDPOINT="}, config(t, "cli: {}"), "", 2},
		{nil, config(t, "cli: {output: 42}"), "", 2},
		{nil, config(t, "cli: {output: json, output: table}"), "", 2},
	} {
		client = &testClient{}
		code, _, errout = invoke(t, client, tc.env, false, instanceCommand, listCommand, "--config", tc.path)
		if code != tc.code || client.endpoint != tc.want {
			t.Fatal(code, errout, client.endpoint)
		}
	}
}

// SPEC-0002 REQ-008/010/011/012 / AC-008: style is destination-specific and never enters machine streams.
func TestREQ010ColorAndMachineStreams(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		tty   bool
		env   []string
		flags []string
		color bool
	}{
		{true, nil, nil, true},
		{false, nil, nil, false},
		{true, []string{"NO_COLOR=1"}, nil, false},
		{true, []string{"TERM=dumb"}, nil, false},
		{false, []string{"NO_COLOR=1"}, []string{alwaysColorFlag}, true},
		{true, nil, []string{"--color=never"}, false},
		{true, nil, []string{alwaysColorFlag, "-o=json"}, false},
	} {
		code, out, errout := invoke(t, &testClient{}, tc.env, tc.tty, append([]string{instanceCommand, listCommand}, tc.flags...)...)
		if code != 0 || strings.Contains(out, "\x1b") != tc.color {
			t.Fatal(tc, code, out, errout)
		}
	}
	client := &testClient{logs: []byte{0xff, 0, 13, 10}}
	code, out, errout := invoke(t, client, nil, true, "command", "execution", "stdout", instanceCommand, "command", alwaysColorFlag)
	if code != 0 || out != string(client.logs) {
		t.Fatal(code, []byte(out), errout)
	}
	for _, shell := range []string{bashShell, zshShell, fishShell, powershellShell} {
		code, out, errout = invoke(t, client, nil, true, completionCommand, shell, alwaysColorFlag)
		if code != 0 || strings.Contains(out, "\x1b") {
			t.Fatal(code, out, errout)
		}
	}
	for _, flags := range [][]string{{"--confirm"}, {"--pager=cat"}, {"--pager-arg=x"}, {"--columns=name"}} {
		before := client.calls
		code, _, errout = invoke(t, client, []string{"PAGER=private-sentinel"}, true, append([]string{instanceCommand, "delete", instanceCommand}, flags...)...)
		if code != 2 || client.calls != before {
			t.Fatal(code, errout)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken writer") }

// SPEC-0002 REQ-008: output failure wins, while Connect errors retain runtime classification.
func TestREQ008Failures(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{helpFlag}, {versionCommand}, {completionCommand, bashShell}, {instanceCommand, listCommand}} {
		code := Run(t.Context(), args, Dependencies{Stdout: brokenWriter{}, Stderr: io.Discard, Client: func(string) inframillv1connect.EngineServiceClient { return &testClient{} }})
		if code != 3 {
			t.Fatal(args, code)
		}
	}
	client := &testClient{failure: connect.NewError(connect.CodeInvalidArgument, errors.New("secret-sentinel"))}
	code, _, errout := invoke(t, client, nil, false, instanceCommand, listCommand)
	if code != 3 || strings.Contains(errout, "secret-sentinel") || strings.Count(errout, "inframill:") != 1 {
		t.Fatal(code, errout)
	}
}

const (
	helpFlag = "--help"
)

const (
	bashShell = "bash"
)

const completionCommand = "completion"

const (
	instanceCommand = "instance"
	listCommand     = "list"
	versionCommand  = "version"
	privateSentinel = "private-sentinel"
	alwaysColorFlag = "--color=always"
)

const (
	fishShell       = "fish"
	zshShell        = "zsh"
	powershellShell = "powershell"
)

// SPEC-0003 REQ-001: command removal is enforced before client initialization.
func TestPackageCommandAbsent(t *testing.T) {
	t.Chdir(t.TempDir())
	client := &testClient{}
	for _, args := range [][]string{{removedPackageCommand}, {removedPackageCommand, "build", "source", "destination"}, {"help", removedPackageCommand}} {
		code, out, errout := invoke(t, client, nil, false, args...)
		if code != 2 || out != "" || client.endpoint != "" {
			t.Fatal(args, code, out, errout)
		}
	}
	code, out, errout := invoke(t, client, nil, false, "__complete", "pac")
	if code != 0 || strings.Contains(out, removedPackageCommand) {
		t.Fatal(code, out, errout)
	}
}

const removedPackageCommand = "package"

// SPEC-0011 REQ-003: retired paths reject before creating a client.
func TestREQ003RetiredCommandPaths(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"operation", "list"}, {"operation", "show", "debug.echo"},
		{"command", "get", "instance", "execution"}, {"command", "wait", "instance", "execution"},
		{"command", "stdout", "instance", "execution"}, {"command", "stderr", "instance", "execution"},
		{"command", "list", "--instance-id=instance"},
	} {
		client := &testClient{}
		code, _, errout := invoke(t, client, nil, false, args...)
		if code != 2 || client.endpoint != "" {
			t.Fatal(args, code, errout, client.endpoint)
		}
	}
}
