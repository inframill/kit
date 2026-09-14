package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/gofrs/flock"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type instance struct {
	backend        *Backend
	root           string
	mu             sync.Mutex
	info           inframill.InstanceInfo
	store          *commandStore
	owner          *flock.Flock
	owned          bool
	rootIdentity   os.FileInfo
	parentIdentity os.FileInfo
	configs        map[string]drivers.Config
	commands       chan inframill.CommandRequest
	done           chan struct{}
	finished       chan struct{}
	stopOnce       sync.Once
	finishOnce     sync.Once
	accepting      bool
	deleted        bool
	runCtx         context.Context
	cancel         context.CancelFunc
	closeErr       error
}

var _ inframill.Instance = (*instance)(nil)

func newInstance(b *Backend, root string, info inframill.InstanceInfo) *instance {
	ctx, cancel := context.WithCancel(context.Background())
	return &instance{backend: b, root: root, info: inframill.CloneInfo(info), commands: make(chan inframill.CommandRequest, b.capacity), done: make(chan struct{}), finished: make(chan struct{}), runCtx: ctx, cancel: cancel}
}

func (i *instance) Info() inframill.InstanceInfo {
	i.mu.Lock()
	defer i.mu.Unlock()
	return inframill.CloneInfo(i.info)
}
func (i *instance) Deleted() bool { i.mu.Lock(); defer i.mu.Unlock(); return i.deleted }
func (i *instance) publishLocked(status inframill.InstanceStatus) error {
	if err := i.checkIdentity(); err != nil {
		return err
	}
	next := inframill.CloneInfo(i.info)
	next.Status = status
	next.Failure = nil
	if err := writeInfo(i.root, next); err != nil {
		return err
	}
	i.info = next
	return nil
}

func (i *instance) failLocked(err error) {
	i.accepting = false
	i.info.Status = inframill.InstanceFailed
	i.info.Failure = &inframill.InstanceFailure{Code: inframill.StateFailed, Message: err.Error()}
	i.cancel()
	i.stopOnce.Do(func() { close(i.done) })
	if i.owned && !i.deleted && i.checkIdentity() == nil {
		_ = writeInfo(i.root, i.info) //nolint:errcheck // Best effort: the failed snapshot remains observable in memory.
	} // Best effort; memory remains observable when storage is unavailable.
}

func (i *instance) storageFailure(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deleted {
		return
	}
	select {
	case <-i.finished:
		return
	default:
	}
	i.failLocked(err)
}

func (i *instance) scope(command string, stdout, stderr io.Writer) (drivers.ExecutionContext, error) {
	name, _, ok := strings.Cut(command, ".")
	if !ok {
		return drivers.ExecutionContext{}, inframill.InvalidArgument("command", "qualified command is required")
	}
	config, ok := i.configs[name]
	if !ok {
		return drivers.ExecutionContext{}, inframill.InvalidArgument("command", "command is not supported by this Instance")
	}
	copied, err := inframill.Normalize(config)
	if err != nil {
		return drivers.ExecutionContext{}, err
	}
	return drivers.ExecutionContext{Stdout: stdout, Stderr: stderr, ArtifactDirectory: filepath.Join(i.root, "artifacts"), WorkspaceDirectory: filepath.Join(i.root, "workspace", name), Binding: binding(i.info), Values: inframill.CloneObject(i.info.Values), DriverConfig: copied}, nil
}

func (i *instance) prepare(ctx context.Context, r inframill.CommandRequest, stdout, stderr io.Writer) (drivers.PreparedCommand, error) {
	if r.Command == deleteCommand {
		if len(r.Args) != 0 {
			return nil, inframill.InvalidArgument("args", "builtin.delete takes no arguments")
		}
		return nil, nil //nolint:nilnil // builtin.delete executes through the Instance lifecycle.
	}
	scope, err := i.scope(r.Command, stdout, stderr)
	if err != nil {
		return nil, err
	}
	scope.CommandID = string(r.ID)
	id, command, _ := strings.Cut(r.Command, ".")
	return i.backend.drivers.Prepare(ctx, drivers.CommandRequest{DriverID: id, Command: command, Scope: scope, Arguments: r.Args})
}

func (i *instance) SubmitCommand(ctx context.Context, r inframill.CommandRequest) (inframill.CommandID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !inframill.ValidUUID(string(r.ID)) {
		return "", inframill.InvalidArgument("command_id", "command_id must be a UUID")
	}
	if len(r.IdempotencyKey) > 200 {
		return "", inframill.InvalidArgument("idempotency_key", "idempotency key exceeds 200 characters")
	}
	args, err := inframill.NormalizeArguments(r.Args)
	if err != nil {
		return "", inframill.InvalidArgument("args", "arguments must be JSON data")
	}
	r.Args = args
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.info.Status == inframill.InstanceFailed {
		return "", inframill.ErrInstanceFailed
	}
	if i.deleted {
		return "", inframill.ErrNotFound
	}
	select {
	case <-i.done:
		return "", inframill.ErrClosed
	default:
	}
	select {
	case <-i.finished:
		return "", inframill.ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := i.checkIdentity(); err != nil {
		i.failLocked(stateError("verify Instance before admission", err))
		return "", inframill.ErrInstanceFailed
	}
	if r.IdempotencyKey != "" && i.store != nil {
		previous, err := i.store.byKey(ctx, r.IdempotencyKey)
		if err == nil {
			if previous.Request.Command != r.Command || !reflect.DeepEqual(previous.Request.Args, r.Args) {
				return "", inframill.ErrConflict
			}
			return previous.Request.ID, nil
		}
		if !errors.Is(err, inframill.ErrNotFound) {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			i.failLocked(stateError("lookup idempotency key", err))
			return "", inframill.ErrInstanceFailed
		}
	}
	if !i.accepting {
		return "", inframill.ErrClosed
	}
	if len(i.commands) == cap(i.commands) {
		return "", inframill.ErrQueueFull
	}
	if _, err := i.prepare(ctx, r, io.Discard, io.Discard); err != nil {
		return "", inframill.InvalidArgument("command", err.Error())
	}
	if _, err := i.store.get(ctx, r.ID); err == nil {
		return "", inframill.ErrConflict
	} else if !errors.Is(err, inframill.ErrNotFound) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		i.failLocked(stateError("lookup command", err))
		return "", inframill.ErrInstanceFailed
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c := inframill.CommandExecution{Request: r, Status: inframill.Queued, CreatedAt: i.backend.clock().UTC()}
	// After this boundary caller cancellation cannot undo or abandon durable work.
	if err := i.store.insert(context.WithoutCancel(ctx), c); err != nil {
		i.failLocked(stateError("persist admission", err))
		return "", inframill.ErrInstanceFailed
	}
	if r.Command == deleteCommand {
		i.accepting = false
	}
	// Only this mutex's holder can send, and only the worker can reduce occupancy.
	i.commands <- r
	return r.ID, nil
}

func (i *instance) Close(ctx context.Context) error {
	// Signal without waiting for storage holding mu. The worker synchronizes with
	// any admission already in progress before deciding that its queue is drained.
	i.stopOnce.Do(func() { close(i.done) })
	select {
	case <-i.finished:
		return i.closeErr
	case <-ctx.Done():
		i.cancel()
		return ctx.Err()
	}
}

func (i *instance) finish() {
	i.finishOnce.Do(func() {
		i.cancel()
		i.mu.Lock()
		defer i.mu.Unlock()
		if i.store != nil {
			i.closeErr = errors.Join(i.closeErr, i.store.db.Close())
		}
		if i.owner != nil {
			i.closeErr = errors.Join(i.closeErr, i.owner.Close())
		}
		if i.closeErr != nil && !i.deleted {
			i.info.Status = inframill.InstanceFailed
			i.info.Failure = &inframill.InstanceFailure{Code: inframill.StateFailed, Message: i.closeErr.Error()}
			if i.owned && i.checkIdentity() == nil {
				_ = writeInfo(i.root, i.info) //nolint:errcheck // Best effort: the failed snapshot remains observable in memory.
			}
		}
		i.accepting = false
		close(i.finished)
	})
}

func (i *instance) work() {
	defer i.finish()
	defer func() {
		if recover() != nil {
			i.mu.Lock()
			i.failLocked(errors.New("instance worker panicked"))
			i.mu.Unlock()
		}
	}()
	for {
		select {
		case <-i.done:
			i.mu.Lock()
			i.accepting = false
			i.mu.Unlock()
			for {
				select {
				case r := <-i.commands:
					if !i.execute(r) {
						i.interruptRemaining()
						return
					}
				default:
					return
				}
			}
		case r := <-i.commands:
			if !i.execute(r) {
				i.interruptRemaining()
				return
			}
		}
	}
}

func (i *instance) interruptRemaining() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deleted || i.info.Status == inframill.InstanceFailed || i.store == nil {
		return
	}
	if err := i.store.interrupt(context.Background(), i.backend.clock().UTC()); err != nil {
		i.failLocked(stateError("interrupt commands", err))
		return
	}
	if err := i.publishLocked(inframill.InstanceIdle); err != nil {
		i.failLocked(stateError("persist Instance", err))
	}
}

func (i *instance) execute(r inframill.CommandRequest) bool {
	i.mu.Lock()
	if i.info.Status == inframill.InstanceFailed || i.runCtx.Err() != nil {
		i.mu.Unlock()
		return false
	}
	c, checkErr := i.store.get(i.runCtx, r.ID)
	if checkErr != nil {
		if i.runCtx.Err() != nil {
			i.mu.Unlock()
			return false
		}
		i.failLocked(stateError("load command", checkErr))
		i.mu.Unlock()
		return false
	}
	now := i.backend.clock().UTC()
	c.Status = inframill.Running
	c.StartedAt = &now
	if err := i.store.update(context.Background(), c); err != nil {
		i.failLocked(stateError("persist running command", err))
		i.mu.Unlock()
		return false
	}
	if err := i.publishLocked(inframill.InstanceRunning); err != nil {
		i.failLocked(stateError("persist running Instance", err))
		i.mu.Unlock()
		return false
	}
	i.mu.Unlock()
	if r.Command == deleteCommand {
		return i.executeDelete()
	}
	stdout, stderr, checkErr := i.outputs(r.ID)
	if checkErr != nil {
		i.storageFailure(stateError("open command logs", checkErr))
		return false
	}
	i.mu.Lock()
	prepared, prepareErr := i.prepare(i.runCtx, r, stdout, stderr)
	i.mu.Unlock()
	var value any
	commandErr := prepareErr
	if commandErr == nil {
		value, commandErr = prepared.Execute(i.runCtx)
	}
	logErr := errors.Join(stdout.finish(), stderr.finish())
	if logErr != nil {
		i.storageFailure(stateError("persist command logs", logErr))
		return false
	}
	c.Result = nil
	if commandErr == nil {
		c.Result, commandErr = json.Marshal(value)
	}
	now = i.backend.clock().UTC()
	c.EndedAt = &now
	c.Status = inframill.Succeeded
	if commandErr != nil {
		c.Status = inframill.Failed
		c.Error = &inframill.CommandError{Code: inframill.CommandFailed, Message: commandErr.Error()}
		if detail, ok := errors.AsType[*inframill.Error](commandErr); ok {
			c.Error = &inframill.CommandError{Code: detail.Code, Message: detail.Message, Path: detail.Path}
		}
	}
	if i.runCtx.Err() != nil {
		c.Status = inframill.Interrupted
		c.Error = &inframill.CommandError{Code: inframill.CommandInterrupted, Message: "command interrupted during shutdown"}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.info.Status == inframill.InstanceFailed {
		return false
	}
	if err := i.store.update(context.Background(), c); err != nil {
		i.failLocked(stateError("persist command result", err))
		return false
	}
	if err := i.publishLocked(inframill.InstanceIdle); err != nil {
		i.failLocked(stateError("persist Instance", err))
		return false
	}
	return i.runCtx.Err() == nil
}

func (i *instance) executeDelete() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.runCtx.Err() != nil {
		return false
	}
	lock, err := i.backend.lockNamespace(i.runCtx)
	if err != nil {
		i.failLocked(stateError("lock Instance namespace", err))
		return false
	}
	defer lock.Close()
	if err := i.checkIdentity(); err != nil {
		i.failLocked(err)
		return false
	}
	namespace, err := os.OpenRoot(i.backend.root)
	if err != nil {
		i.failLocked(err)
		return false
	}
	defer namespace.Close()
	parent, err := namespace.Stat(".")
	if err != nil || !os.SameFile(parent, i.parentIdentity) {
		i.failLocked(errors.New("Instance namespace replaced; existing data preserved"))
		return false
	}
	if err := i.store.db.Close(); err != nil {
		i.failLocked(stateError("close command database", err))
		return false
	}
	if err := i.checkIdentity(); err != nil {
		i.failLocked(err)
		return false
	}
	if err := namespace.RemoveAll(string(i.info.ID)); err != nil {
		i.failLocked(stateError("delete Instance workspace", err))
		return false
	}
	i.deleted = true
	i.accepting = false
	return false
}

type logWriter struct {
	live io.Writer
	file *os.File
	mu   sync.Mutex
	err  error
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.file.Write(p)
	w.err = errors.Join(w.err, err)
	if err == nil && w.live != nil {
		return w.live.Write(p)
	}
	return n, err
}

func (w *logWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.err, w.file.Sync(), w.file.Close())
}

func (i *instance) outputs(id inframill.CommandID) (*logWriter, *logWriter, error) {
	root := filepath.Join(i.root, "logs")
	if err := directory(root); err != nil {
		return nil, nil, err
	}
	commandRoot := filepath.Join(root, string(id))
	if err := os.Mkdir(commandRoot, 0o700); err != nil {
		return nil, nil, err
	}
	//nolint:gosec // UUID command directory was exclusively created within the owned workspace.
	out, err := os.OpenFile(filepath.Join(commandRoot, "stdout.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	//nolint:gosec // UUID command directory was exclusively created within the owned workspace.
	stderr, err := os.OpenFile(filepath.Join(commandRoot, "stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, errors.Join(err, out.Close())
	}
	return &logWriter{file: out, live: i.backend.stdout}, &logWriter{file: stderr, live: i.backend.stderr}, nil
}
