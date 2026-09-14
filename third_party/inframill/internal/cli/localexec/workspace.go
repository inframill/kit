package localexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/sir-hassan/inframill/internal/backend"
	"github.com/sir-hassan/inframill/internal/blueprint"
	driver "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func lockWorkspace(path string, create bool) (*flock.Flock, error) {
	entries, err := os.ReadDir(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("workspace must be a readable directory")
	}
	for _, entry := range entries {
		if inframill.ValidUUID(entry.Name()) {
			return nil, errors.New("legacy flat local workspace layout; existing data preserved; use a new empty dataDir")
		}
	}
	if create {
		if mkdirErr := os.MkdirAll(path, 0o700); mkdirErr != nil {
			return nil, mkdirErr
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return nil, errors.New("workspace must be a directory")
	}
	lockPath := filepath.Join(path, ".local.lock")
	if info, lockErr := os.Lstat(lockPath); lockErr == nil && !info.Mode().IsRegular() {
		return nil, errors.New("invalid workspace lock")
	} else if lockErr != nil && !os.IsNotExist(lockErr) {
		return nil, lockErr
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = lock.Close() //nolint:errcheck // Failed acquisition; no workspace ownership.
		return nil, errors.New("workspace is busy or cannot be locked")
	}
	if err := os.Chmod(lockPath, 0o600); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}

func runWorkspace(ctx context.Context, mode string, o options, input execInput, environ []string, stdout, stderr io.Writer) (r result, resultErr error) {
	if !inframill.ValidInstanceName(o.name) {
		return r, invalid("name must be project/name")
	}
	if mode == historyMode && (o.limit < 1 || o.limit > 100 || (o.commandID != "" && !inframill.ValidUUID(o.commandID))) {
		return r, invalid("invalid history command ID or page limit")
	}
	values := input.config.Values
	args := inframill.Arguments{}
	var source inframill.BlueprintSource
	if mode == execMode {
		var err error
		environ, err = loadEnvFile(o, environ)
		if err != nil {
			return r, err
		}
		if o.argsFile != "" {
			args, err = readObject(o.argsFile)
			if err != nil {
				return r, err
			}
		}
		source = input.source
		if source == nil {
			snapshot, err := blueprint.SnapshotLocal(ctx, o.blueprint)
			if err != nil {
				return r, invalid("invalid local Blueprint: " + err.Error())
			}
			defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
			source = snapshot
		}
	} else {
		local, err := blueprint.New(o.workspace)
		if err != nil {
			return r, err
		}
		source = local // History never resolves or loads a Blueprint.
	}
	catalog, err := driver.New(ctx, o.drivers, environ)
	if err != nil {
		return r, err
	}
	if mode == execMode && o.command != "builtin.delete" {
		id, _, _ := strings.Cut(o.command, ".")
		entries, err := catalog.List(ctx)
		if err != nil {
			return r, err
		}
		found := false
		for _, entry := range entries {
			if entry.ID != id {
				continue
			}
			found = true
			if entry.Status != driver.Connected {
				return r, fmt.Errorf("drivers.%s: offline: %s", id, entry.Reason)
			}
		}
		if !found {
			return r, errors.New("unknown configured Driver")
		}
	}
	lock, err := lockWorkspace(o.workspace, mode == execMode)
	if err != nil {
		return r, err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()

	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &liveWriter{ctx: executionCtx, cancel: cancel, destination: stdout}
	errout := &liveWriter{ctx: executionCtx, cancel: cancel, destination: stderr}
	b, err := backend.New(filepath.Join(o.workspace, "instances"), catalog, backend.Options{Stdout: out, Stderr: errout})
	if err != nil {
		return r, err
	}
	ids, err := b.GetAllIDs(ctx)
	if err != nil {
		return r, err
	}
	if mode == execMode && input.source != nil {
		// Acquire packages only after validating the Instance layout, under the local lock.
		selected, sourceErr := source.Resolve(ctx, input.config.Blueprint)
		if failure, ok := errors.AsType[*inframill.Error](sourceErr); ok && failure.Code == inframill.BlueprintNotFound {
			selected, sourceErr = source.Fetch(ctx, input.config.Blueprint)
		}
		if sourceErr != nil {
			return r, sourceErr
		}
		input.config.Blueprint = selected.Reference
	}
	e, err := inframill.New(ctx, source, b)
	if err != nil {
		return r, err
	}
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		resultErr = errors.Join(resultErr, e.Shutdown(shutdown))
	}()
	if startErr := e.Start(ctx); startErr != nil {
		return r, startErr
	}
	if len(e.StartupFailures()) != 0 {
		return r, errors.New("cannot open local Instance; existing data preserved")
	}
	var info inframill.InstanceInfo
	for _, id := range ids {
		candidate, lookupErr := e.GetInstance(ctx, id)
		if lookupErr != nil {
			return r, lookupErr
		}
		if candidate.Name == o.name {
			info = candidate
			break
		}
	}
	if info.ID == "" {
		if mode == historyMode {
			return r, errors.New("named Instance not found in workspace")
		}
		info, err = e.InitInstance(ctx, o.name, input.config)
	}
	if err != nil {
		return r, err
	}
	r.InstanceID = info.ID
	if info.Status == inframill.InstanceFailed {
		return r, errors.New("local Instance failed or is owned by another process")
	}
	if mode == historyMode {
		return r, history(ctx, e, info.ID, o, stdout)
	}
	selected, err := source.Resolve(ctx, input.config.Blueprint)
	if err != nil {
		return r, err
	}
	normalized, err := inframill.Normalize(values)
	if err != nil {
		return r, err
	}
	if selected.Reference != info.Blueprint || !reflect.DeepEqual(normalized, info.Values) {
		return r, invalid("Blueprint or Values changed; use a new name or workspace (refresh is not supported)")
	}
	runner := commandRunner{engine: e, backend: b, stdout: out, stderr: errout}
	return runner.execute(ctx, executionCtx, info.ID, o.command, args)
}
