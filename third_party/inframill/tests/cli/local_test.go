package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	localExecCommand = "exec"
	recordPrefix     = "record-"
)

type localFixture struct{ root, blueprint, instance, workspace, bin string }

func localFiles(t *testing.T, mode string) localFixture {
	t.Helper()
	f := localFixture{root: t.TempDir()}
	f.blueprint = filepath.Join(f.root, "source")
	f.instance = filepath.Join(f.root, "instance.json")
	f.workspace = filepath.Join(f.root, defaultWorkspace)
	f.bin = filepath.Join(f.root, "bin")
	files := map[string]string{
		"source/manifest.yaml":              "api_version: inframill.dev/blueprint/v1\ndescription: local fixture\ndrivers:\n  debug: {}\n",
		"source/schema.json":                `{"type":"object","required":["mode","secret_ref"]}`,
		"source/template/debug/mode.gotmpl": "{{ .mode }}",
		"instance.json":                     fmt.Sprintf(`{"blueprint":"example.invalid/test:latest","values":{"mode":%q,"secret_ref":"test_token"}}`, mode),
		"bin/.keep":                         "",
	}
	for name, data := range files {
		path := filepath.Join(f.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeDataConfig(t, filepath.Join(f.root, ".inframill.yaml"), defaultWorkspace)
	return f
}

func (f localFixture) command(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, binary, args...)
	c.Dir = f.root
	c.Env = []string{"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH"), "INFRAMILL_SECRET_TEST_TOKEN=fixture-secret"}
	return c // No stdin and no TTY; child input is EOF.
}

func (f localFixture) args(command, result string) []string {
	a := []string{localExecCommand, command, "--blueprint-dir", f.blueprint, "--instance", f.instance}
	if result != "" {
		a = append(a, "--result-file", filepath.Join(f.root, result))
	}
	return a
}

func localRun(t *testing.T, c *exec.Cmd) invocation {
	t.Helper()
	var out, errout bytes.Buffer
	c.Stdout, c.Stderr = &out, &errout
	err := c.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return invocation{code: code, stdout: out.String(), stderr: errout.String()}
}

type localResult struct {
	Version  string          `json:"api_version"`
	Instance string          `json:"instance_id"`
	Command  string          `json:"command_id"`
	Status   string          `json:"status"`
	Result   json.RawMessage `json:"result"`
	Error    json.RawMessage `json:"error"`
}

func readLocalResult(t *testing.T, path string) localResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result localResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err, string(data))
	}
	if result.Version != "inframill.dev/local-result/v1" {
		t.Fatal(result)
	}
	return result
}

func TestLocalExecReuseAndResults(t *testing.T) {
	f := localFiles(t, "success")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	first := localRun(t, f.command(ctx, f.args("debug.record", "first.json")...))
	if first.code != 0 || !json.Valid([]byte(first.stdout)) || !strings.Contains(first.stdout, `"recorded":true`) || first.stderr != "recorded\n" {
		t.Fatalf("%+v", first)
	}
	r := readLocalResult(t, filepath.Join(f.root, "first.json"))
	if r.Status != localSucceeded || r.Command == "" || r.Instance == "" {
		t.Fatal(r)
	}
	instanceRoot := filepath.Join(f.workspace, "instances", r.Instance)
	metadata, metadataErr := os.ReadFile(filepath.Join(instanceRoot, "instance.json"))
	if metadataErr != nil || !bytes.Contains(metadata, []byte("local/default")) {
		t.Fatal(string(metadata), metadataErr)
	}
	for name, want := range map[string]string{"stdout.log": first.stdout, "stderr.log": first.stderr} {
		got, err := os.ReadFile(filepath.Join(instanceRoot, "logs", r.Command, name))
		if err != nil || string(got) != want {
			t.Fatal(string(got), err)
		}
	}
	second := localRun(t, f.command(ctx, f.args("debug.echo", "second.json")...))
	if second.code != 0 {
		t.Fatalf("%+v", second)
	}
	next := readLocalResult(t, filepath.Join(f.root, "second.json"))
	if next.Instance != r.Instance || next.Command == r.Command {
		t.Fatal(next)
	}
	artifact := filepath.Join(instanceRoot, "artifacts", recordPrefix+r.Command+".json")
	before, artifactErr := os.ReadFile(artifact)
	if artifactErr != nil {
		t.Fatal(artifactErr)
	}
	// Normalized Values comparison ignores object key order and whitespace.
	if err := os.WriteFile(f.instance, []byte(`{"values": {"secret_ref":"test_token", "mode":"success"}, "blueprint":"ignored.invalid/override:changed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := localRun(t, f.command(ctx, f.args("debug.record", "third.json")...)); got.code != 0 {
		t.Fatalf("%+v", got)
	}
	artifactArgs := filepath.Join(f.root, "write.args.json")
	if err := os.WriteFile(artifactArgs, []byte(`{"name":"example.txt","content":"roundtrip"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := localRun(t, f.command(ctx, append(f.args("debug.write-artifact", "artifact.json"), "--args-file", artifactArgs)...)); got.code != 0 {
		t.Fatal(got)
	}
	if err := os.WriteFile(artifactArgs, []byte(`{"name":"example.txt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := localRun(t, f.command(ctx, append(f.args("debug.read-artifact", "read.json"), "--args-file", artifactArgs)...)); got.code != 0 || !strings.Contains(got.stdout, "roundtrip") {
		t.Fatal(got)
	}
	for _, change := range []string{"values", "blueprint"} {
		t.Run(change, func(t *testing.T) {
			args := f.args("debug.record", change+"-result.json")
			switch change {
			case "values":
				if err := os.WriteFile(f.instance, []byte(`{"blueprint":"example.invalid/test:latest","values":{"secret_ref":"test_token","mode":"different"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "blueprint":
				if err := os.WriteFile(f.instance, []byte(`{"blueprint":"example.invalid/test:latest","values":{"secret_ref":"test_token","mode":"success"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.blueprint, "template/debug/new"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := localRun(t, f.command(ctx, args...))
			if got.code != 2 || got.stdout != "" {
				t.Fatalf("%+v", got)
			}
			if rejected := readLocalResult(t, filepath.Join(f.root, change+"-result.json")); rejected.Status != localRejected || rejected.Command != "" {
				t.Fatal(rejected)
			}
		})
	}
	after, err := os.ReadFile(artifact)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("artifact changed on rejection", err)
	}
	assertRecordCount(t, instanceRoot, 2)
	history := localRun(t, f.command(ctx, "history", "--workspace", f.workspace, "-o", "json"))
	if history.code != 0 || !strings.Contains(history.stdout, r.Command) {
		t.Fatalf("%+v", history)
	}
	// Never overwrite an existing result file or execute its command.
	if got := localRun(t, f.command(ctx, f.args("debug.record", "first.json")...)); got.code != 3 {
		t.Fatalf("%+v", got)
	}
}

func TestLocalFailureAndInputErrors(t *testing.T) {
	f := localFiles(t, "fail")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	args := append(f.args("debug.record", "failed.json"), "--name", "demo/dev")
	got := localRun(t, f.command(ctx, args...))
	// SPEC-0000 REQ-009/018: print the stored subprocess cause while retaining redaction.
	if got.code != 1 || !strings.Contains(got.stderr, "deliberate debug failure after recording") || strings.Contains(got.stderr, "fixture-secret") {
		t.Fatalf("%+v", got)
	}
	r := readLocalResult(t, filepath.Join(f.root, "failed.json"))
	if r.Status != "failed" || len(r.Error) == 0 {
		t.Fatal(r)
	}
	assertRecordCount(t, filepath.Join(f.workspace, "instances", r.Instance), 1)
	for _, args := range [][]string{
		{localExecCommand, "debug.record"},
		{localExecCommand, "debug.record", "--unknown"},
		append(f.args("debug.record", ""), "--args-file", "missing"),
		f.args("debug.unknown", ""),
	} {
		if got := localRun(t, f.command(ctx, args...)); got.code != 2 {
			t.Fatalf("%v: %+v", args, got)
		}
	}
}

func waitLine(t *testing.T, ctx context.Context, reader io.Reader, want ...string) {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		remaining := make(map[string]bool, len(want))
		for _, line := range want {
			remaining[line] = true
		}
		s := bufio.NewScanner(reader)
		for s.Scan() {
			delete(remaining, s.Text())
			if len(remaining) == 0 {
				done <- true
				return
			}
		}
		done <- false
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatalf("missing live line %q", want)
		}
	case <-ctx.Done():
		t.Fatal("live output deadline", ctx.Err())
	}
}

func onlyInstanceRoot(t *testing.T, workspace string) string {
	t.Helper()
	workspace = filepath.Join(workspace, "instances")
	var found string
	err := filepath.WalkDir(workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == "instance.json" && !entry.IsDir() {
			if found != "" {
				t.Fatal("multiple local Instances")
			}
			found = filepath.Dir(path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != "" {
		return found
	}
	t.Fatal("missing local Instance")
	return ""
}

func TestLocalLiveOutputBusyAndInterrupt(t *testing.T) {
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := localFiles(t, "block")
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			c := f.command(ctx, f.args("debug.record", "interrupted.json")...)
			errout, err := c.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if startErr := c.Start(); startErr != nil {
				t.Fatal(startErr)
			}
			t.Cleanup(func() { _ = c.Process.Kill() }) //nolint:errcheck // Process may already have exited.
			waitLine(t, ctx, errout, "waiting for release")
			busy := localRun(t, f.command(ctx, f.args("debug.record", "busy.json")...))
			if busy.code != 3 || !strings.Contains(busy.stderr, "busy") {
				t.Fatalf("%+v", busy)
			}
			instanceRoot := onlyInstanceRoot(t, f.workspace)
			if signalErr := c.Process.Signal(signal); signalErr != nil {
				t.Fatal(signalErr)
			}
			err = c.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 130 {
				t.Fatal(err)
			}
			r := readLocalResult(t, filepath.Join(f.root, "interrupted.json"))
			if r.Status != "interrupted" || r.Command == "" {
				t.Fatal(r)
			}
			history := localRun(t, f.command(ctx, "history", "--workspace", f.workspace, "--command", r.Command))
			if history.code != 0 || !strings.Contains(history.stdout, "interrupted") {
				t.Fatalf("%+v", history)
			}
			assertRecordCount(t, instanceRoot, 1)
		})
	}
}

func TestLocalBrokenPipeReturnsStructuredFailure(t *testing.T) {
	f := localFiles(t, "success")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	defer writer.Close() //nolint:errcheck // Disposable pipe.
	c := f.command(ctx, f.args("debug.record", "pipe.json")...)
	c.Stdout = writer
	var diagnostics bytes.Buffer
	c.Stderr = &diagnostics
	err = c.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatal(err, diagnostics.String())
	}
	r := readLocalResult(t, filepath.Join(f.root, "pipe.json"))
	if r.Command == "" || r.Status == "succeeded" || r.Status == "rejected" {
		t.Fatal(r)
	}
	log, err := os.ReadFile(filepath.Join(f.workspace, "instances", r.Instance, "logs", r.Command, "stdout.log"))
	if err != nil || !json.Valid(log) || bytes.Contains(log, []byte("fixture-secret")) {
		t.Fatal(string(log), err)
	}
}

func TestLocalAbruptExitHistoryRecovery(t *testing.T) {
	f := localFiles(t, "block")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	c := f.command(ctx, f.args("debug.record", "")...)
	out, err := c.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill() }) //nolint:errcheck // Process may already have exited.
	waitLine(t, ctx, out, "waiting for release")
	root := onlyInstanceRoot(t, f.workspace)
	// Release the disposable fake after killing its owner so no orphan is left.
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = c.Wait() //nolint:errcheck // SIGKILL intentionally makes the child unsuccessful.
	if err := os.WriteFile(filepath.Join(root, "workspace/debug/release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	history := localRun(t, f.command(ctx, "history", "--workspace", f.workspace))
	if history.code != 0 || !strings.Contains(history.stdout, "interrupted") {
		t.Fatalf("%+v", history)
	}
	assertRecordCount(t, root, 1)
}

func assertRecordCount(t *testing.T, root string, want int) {
	t.Helper()
	records, err := filepath.Glob(filepath.Join(root, "artifacts", recordPrefix+"*.json"))
	if err != nil || len(records) != want {
		t.Fatal("unexpected execution count", len(records), want, err)
	}
}
