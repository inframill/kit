package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type fakeDriver struct {
	run func(context.Context, drivers.ExecutionContext, inframill.Arguments) (any, error)
}

func (fakeDriver) List(context.Context) ([]drivers.Entry, error) {
	return []drivers.Entry{{ID: testDriverName, Status: drivers.Connected, Info: &drivers.Info{Commands: map[string]drivers.CommandDefinition{"run": {Description: "fake execution", ArgumentsSchema: map[string]any{"type": "object"}, Safety: drivers.Reversible, Effects: []string{"fake execution"}, Verification: "fake completes"}}}}}, nil
}
func (fakeDriver) Validate(context.Context, drivers.ValidationRequest) error { return nil }

func (s fakeDriver) Prepare(_ context.Context, request drivers.CommandRequest) (drivers.PreparedCommand, error) {
	scope, op, args := request.Scope, request.Command, inframill.Arguments(request.Arguments)
	if op != "run" {
		return nil, errors.New("unknown command")
	}
	for key := range args {
		if key != labelArgument && key != "fail" {
			return nil, errors.New("unknown argument")
		}
	}
	return fakeExecution{fn: func(ctx context.Context) (any, error) {
		if s.run != nil {
			return s.run(ctx, scope, args)
		}
		if args["fail"] == true {
			return nil, errors.New("exit status 1")
		}
		return map[string]any{"ok": true}, nil
	}}, nil
}

type fakeExecution struct {
	fn func(context.Context) (any, error)
}

func (e fakeExecution) Execute(ctx context.Context) (any, error) { return e.fn(ctx) }
func (fakeExecution) Verify(context.Context, any) error          { return nil }
func testID(n int) inframill.InstanceID {
	return inframill.InstanceID(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
}

func request(n int, label string) inframill.CommandRequest {
	return inframill.CommandRequest{ID: inframill.CommandID(testID(n)), Command: "test.run", Args: inframill.Arguments{labelArgument: label}}
}

func testBlueprint(t *testing.T) inframill.Blueprint {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"manifest.yaml":              "api_version: inframill.dev/blueprint/v1\ndescription: Test\ndrivers:\n  test: {}\n",
		"schema.json":                `{"type":"object","required":["message"],"properties":{"message":{"type":"string"}},"additionalProperties":false}`,
		"template/test/input.gotmpl": "{{ .message }}",
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return inframill.Blueprint{Directory: root, Reference: "registry.example/test@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Repository: testDriverName, APIVersion: "inframill.dev/blueprint/v1"}
}

func fixture(t *testing.T, capacity int, run func(context.Context, drivers.ExecutionContext, inframill.Arguments) (any, error)) (*Backend, *instance) {
	t.Helper()
	b, err := New(t.TempDir(), testDrivers(t, []fakeDriver{fakeDriver{run: run}}), Options{QueueCapacity: capacity})
	if err != nil {
		t.Fatal(err)
	}
	if localErr := b.InitInstance(t.Context(), testID(1), "project/dev", testBlueprint(t), inframill.InstanceValues{messageKey: helloMessage}); localErr != nil {
		t.Fatal(localErr)
	}
	obj, err := b.SpawnInstance(t.Context(), testID(1))
	if err != nil {
		t.Fatal(err)
	}
	i, ok := obj.(*instance)
	if !ok {
		t.Fatal("wrong implementation")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := i.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return b, i
}

func waitCommand(t *testing.T, i inframill.Instance, id inframill.CommandID) inframill.CommandExecution {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		c, err := i.GetCommandExecution(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if inframill.Terminal(c.Status) {
			return c
		}
		select {
		case <-deadline:
			t.Fatal("command did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}

func waitFinished(t *testing.T, i *instance) {
	t.Helper()
	select {
	case <-i.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestCreationAndSnapshots(t *testing.T) {
	b, i := fixture(t, 2, nil)
	if !inframill.ValidUUID(string(i.Info().ID)) || i.Info().Name != "project/dev" {
		t.Fatal(i.Info())
	}
	data, checkErr := os.ReadFile(filepath.Join(i.root, "workspace/test/input"))
	if checkErr != nil || string(data) != helloMessage {
		t.Fatalf("render: %q %v", data, checkErr)
	}
	info := i.Info()
	info.Values[messageKey] = "changed"
	if i.Info().Values[messageKey] != helloMessage {
		t.Fatal("shared Values")
	}
	if validationErr := b.InitInstance(t.Context(), testID(2), "project/dev", testBlueprint(t), inframill.InstanceValues{messageKey: "x"}); !errors.Is(validationErr, inframill.ErrAlreadyExists) {
		t.Fatal(validationErr)
	}
	if err := b.InitInstance(t.Context(), testID(1), "project/other", testBlueprint(t), inframill.InstanceValues{messageKey: "x"}); !errors.Is(err, inframill.ErrAlreadyExists) {
		t.Fatal(err)
	}
	// SPEC-0000 REQ-002/004/005/011: directory names cannot supply an unreadable UUID.
	if err := os.WriteFile(filepath.Join(i.root, "instance.json"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids, checkErr := b.GetAllIDs(t.Context())
	if checkErr == nil || len(ids) != 0 {
		t.Fatalf("corrupt identity was enumerated: %v %v", ids, checkErr)
	}
}

func TestFIFOQueueFullAndFailureContinues(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	_, i := fixture(t, 2, func(ctx context.Context, _ drivers.ExecutionContext, args inframill.Arguments) (any, error) {
		label, ok := args[labelArgument].(string)
		if !ok {
			return nil, errors.New("label must be a string")
		}
		started <- label
		if label == firstLabel {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if label == secondLabel {
			return nil, errors.New("exit status 1")
		}
		return map[string]any{labelArgument: label}, nil
	})
	for n, label := range []string{firstLabel, secondLabel, "third"} {
		if _, err := i.SubmitCommand(t.Context(), request(n+10, label)); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			if got := <-started; got != firstLabel {
				t.Fatal(got)
			}
		}
	}
	if _, err := i.SubmitCommand(t.Context(), request(20, "overflow")); !errors.Is(err, inframill.ErrQueueFull) {
		t.Fatal(err)
	}
	page, err := i.ListCommandExecutions(t.Context(), inframill.CommandExecutionQuery{})
	if err != nil || len(page.Executions) != 3 {
		t.Fatalf("%d %v", len(page.Executions), err)
	}
	close(release)
	if c := waitCommand(t, i, inframill.CommandID(testID(12))); c.Status != inframill.Succeeded {
		t.Fatal(c)
	}
	if got := []string{<-started, <-started}; !reflect.DeepEqual(got, []string{secondLabel, "third"}) {
		t.Fatal(got)
	}
	c := waitCommand(t, i, inframill.CommandID(testID(11)))
	if c.Status != inframill.Failed || c.Error == nil || i.Info().Status == inframill.InstanceFailed {
		t.Fatal(c, i.Info())
	}
	if _, err := i.GetCommandExecution(t.Context(), inframill.CommandID(testID(20))); !errors.Is(err, inframill.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestConcurrentIdempotencyAndFullQueueRetry(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	_, i := fixture(t, 1, func(ctx context.Context, _ drivers.ExecutionContext, _ inframill.Arguments) (any, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	r := request(10, "same")
	r.IdempotencyKey = retryKey
	original, err := i.SubmitCommand(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := i.SubmitCommand(t.Context(), request(30, "queued")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := range 12 {
		wg.Go(func() {
			other := request(100+n, "same")
			other.IdempotencyKey = retryKey
			id, err := i.SubmitCommand(t.Context(), other)
			if err != nil || id != original {
				t.Errorf("duplicate: %s %v", id, err)
			}
		})
	}
	wg.Wait()
	changed := request(50, "changed")
	changed.IdempotencyKey = retryKey
	if _, err := i.SubmitCommand(t.Context(), changed); !errors.Is(err, inframill.ErrConflict) {
		t.Fatal(err)
	}
	close(release)
	waitCommand(t, i, inframill.CommandID(testID(30)))
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}

func TestStorageFailureFailsOnlyInstance(t *testing.T) {
	_, i := fixture(t, 2, nil)
	if err := i.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := i.SubmitCommand(t.Context(), request(10, "x")); !errors.Is(err, inframill.ErrInstanceFailed) {
		t.Fatal(err)
	}
	info := i.Info()
	if info.Status != inframill.InstanceFailed || info.Failure == nil {
		t.Fatal(info)
	}
	if _, err := i.SubmitCommand(t.Context(), inframill.CommandRequest{ID: inframill.CommandID(testID(11)), Command: deleteCommand}); !errors.Is(err, inframill.ErrInstanceFailed) {
		t.Fatal(err)
	}
	waitFinished(t, i)
}

func TestPaginationAndBoundedLogSnapshots(t *testing.T) {
	written := make(chan struct{})
	release := make(chan struct{})
	_, i := fixture(t, 4, func(ctx context.Context, s drivers.ExecutionContext, args inframill.Arguments) (any, error) {
		if args[labelArgument] == "log" {
			if _, err := io.WriteString(s.Stdout, helloMessage); err != nil {
				return nil, err
			}
			close(written)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if _, err := io.WriteString(s.Stdout, "world"); err != nil {
				return nil, err
			}
		}
		return map[string]any{"ok": true}, nil
	})
	id, err := i.SubmitCommand(t.Context(), request(10, "log"))
	if err != nil {
		t.Fatal(err)
	}
	<-written
	reader, err := i.Stdout(id)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close() //nolint:errcheck // Read-only snapshot.
	close(release)
	waitCommand(t, i, id)
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != helloMessage {
		t.Fatalf("%q %v", data, err)
	}
	for n := 11; n < 14; n++ {
		nextID, localErr := i.SubmitCommand(t.Context(), request(n, "other"))
		if localErr != nil {
			t.Fatal(localErr)
		}
		waitCommand(t, i, nextID)
	}
	first, err := i.ListCommandExecutions(t.Context(), inframill.CommandExecutionQuery{Limit: 2})
	if err != nil || len(first.Executions) != 2 || first.NextCursor == "" {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := i.ListCommandExecutions(t.Context(), inframill.CommandExecutionQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Executions) != 2 || second.NextCursor != "" {
		t.Fatalf("%+v %v", second, err)
	}
	if first.Executions[0].Request.ID != inframill.CommandID(testID(13)) || second.Executions[1].Request.ID != id {
		t.Fatal("wrong order")
	}
	if _, err := i.ListCommandExecutions(t.Context(), inframill.CommandExecutionQuery{Status: inframill.Failed, Cursor: first.NextCursor}); err == nil {
		t.Fatal("filter cursor accepted")
	}
}

func TestDeadlineAndREQ009RestartNeverReplay(t *testing.T) {
	entered := make(chan struct{})
	b, i := fixture(t, 2, func(ctx context.Context, _ drivers.ExecutionContext, _ inframill.Arguments) (any, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	first, err := i.SubmitCommand(t.Context(), request(10, "run"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	second, err := i.SubmitCommand(t.Context(), request(11, "queued"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if checkErr := i.Close(ctx); !errors.Is(checkErr, context.DeadlineExceeded) {
		t.Fatal(checkErr)
	}
	waitFinished(t, i)
	if checkErr := i.Close(context.Background()); checkErr != nil {
		t.Fatal(checkErr)
	}
	next, err := New(b.root, testDrivers(t, []fakeDriver{fakeDriver{run: func(context.Context, drivers.ExecutionContext, inframill.Arguments) (any, error) {
		t.Error("replayed work")
		return true, nil
	}}}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := next.SpawnInstance(t.Context(), testID(1))
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close(context.Background()) //nolint:errcheck // Fixture teardown.
	for _, id := range []inframill.CommandID{first, second} {
		c, err := obj.GetCommandExecution(t.Context(), id)
		if err != nil || c.Status != inframill.Interrupted {
			t.Fatalf("%+v %v", c, err)
		}
	}
	if obj.Info().Status != inframill.InstanceIdle {
		t.Fatal(obj.Info())
	}
}

func TestCorruptSQLiteAndExclusiveOwner(t *testing.T) {
	b, i := fixture(t, 1, nil)
	other, checkErr := New(b.root, testDrivers(t, []fakeDriver{fakeDriver{}}), Options{})
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	duplicate, checkErr := other.SpawnInstance(t.Context(), testID(1))
	if checkErr != nil || duplicate.Info().Status != inframill.InstanceFailed {
		t.Fatalf("%v %v", duplicate, checkErr)
	}
	persisted, checkErr := readInfo(i.root, testID(1))
	if checkErr != nil || persisted.Status != inframill.InstanceIdle {
		t.Fatalf("other owner modified metadata: %+v %v", persisted, checkErr)
	}
	if err := i.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(i.root, "commands.sqlite"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, checkErr := other.SpawnInstance(t.Context(), testID(1))
	if checkErr != nil || failed.Info().Status != inframill.InstanceFailed || failed.Info().Name != "project/dev" || failed.Info().Failure == nil {
		t.Fatalf("%+v %v", failed.Info(), checkErr)
	}
}

func TestQueuedDeletion(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	_, i := fixture(t, 2, func(ctx context.Context, _ drivers.ExecutionContext, _ inframill.Arguments) (any, error) {
		close(entered)
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if _, err := i.SubmitCommand(t.Context(), request(10, "run")); err != nil {
		t.Fatal(err)
	}
	<-entered
	id, err := i.SubmitCommand(t.Context(), inframill.CommandRequest{ID: inframill.CommandID(testID(11)), Command: deleteCommand})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i.SubmitCommand(t.Context(), request(12, "later")); !errors.Is(err, inframill.ErrClosed) {
		t.Fatal(err)
	}
	if i.Deleted() {
		t.Fatal("deleted before earlier work finished")
	}
	close(release)
	waitFinished(t, i)
	if !i.Deleted() {
		t.Fatal("missing deletion observation")
	}
	if _, err := os.Stat(i.root); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := i.GetCommandExecution(t.Context(), id); !errors.Is(err, inframill.ErrNotFound) {
		t.Fatal(err)
	}
}

const (
	testDriverName = "test"
	labelArgument  = "label"
	helloMessage   = "hello"
	firstLabel     = "first"
	secondLabel    = "second"
	retryKey       = "retry"
)

const messageKey = "message"

func testDrivers(t *testing.T, supplied []fakeDriver) drivers.Manager {
	t.Helper()
	if len(supplied) != 1 {
		t.Fatal("fixture requires one Driver Manager")
	}
	return supplied[0]
}
