// Package inframill defines application contracts and manages Blueprint metadata
// and live Instance objects. Concrete execution and persistence remain internal.
package inframill

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	blueprints   BlueprintSource
	backend      Backend
	mu           sync.Mutex
	creation     sync.Mutex
	instances    map[InstanceID]Instance
	started      bool
	stopping     bool
	startup      []StartupFailure
	lifetime     context.Context
	cancel       context.CancelFunc
	initializing sync.WaitGroup
}
type StartupFailure struct {
	ID      InstanceID
	Message string
}

func New(ctx context.Context, source BlueprintSource, backend Backend) (*Engine, error) {
	if ctx == nil || IsNil(source) || IsNil(backend) {
		return nil, MissingArgument("engine dependencies")
	}
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &Engine{blueprints: source, backend: backend, instances: map[InstanceID]Instance{}, lifetime: lifetime, cancel: cancel}, nil
}

func (e *Engine) Start(ctx context.Context) error {
	e.creation.Lock()
	defer e.creation.Unlock()
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return ErrClosed
	}
	if e.started {
		e.mu.Unlock()
		return nil
	}
	e.initializing.Add(1)
	e.mu.Unlock()
	defer e.initializing.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.lifetime, cancel)
	defer stop()
	ids, err := e.backend.GetAllIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		instance, err := e.backend.SpawnInstance(ctx, id)
		e.mu.Lock()
		switch {
		case err != nil:
			e.startup = append(e.startup, StartupFailure{ID: id, Message: err.Error()})
		case IsNil(instance):
			e.startup = append(e.startup, StartupFailure{ID: id, Message: "backend returned no Instance"})
		default:
			e.instances[id] = instance
		}
		stopping := e.stopping
		e.mu.Unlock()
		if stopping && !IsNil(instance) {
			return errors.Join(ErrClosed, instance.Close(context.Background()))
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return ErrClosed
	}
	e.started = true
	return nil
}

func (e *Engine) StartupFailures() []StartupFailure {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]StartupFailure{}, e.startup...)
}

func (e *Engine) ready() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return ErrClosed
	}
	if !e.started {
		return ErrNotStarted
	}
	return nil
}

func (e *Engine) snapshot() map[InstanceID]Instance {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[InstanceID]Instance, len(e.instances))
	maps.Copy(out, e.instances)
	return out
}

func (e *Engine) removeDeleted(id InstanceID, instance Instance) bool {
	if !instance.Deleted() {
		return false
	}
	e.mu.Lock()
	delete(e.instances, id)
	e.mu.Unlock()
	return true
}

func (e *Engine) lookup(id InstanceID) (Instance, error) {
	if !ValidUUID(string(id)) {
		return nil, InvalidArgument("instance_id", "instance_id must be a UUID")
	}
	e.mu.Lock()
	i, ok := e.instances[id]
	e.mu.Unlock()
	if !ok || e.removeDeleted(id, i) {
		return nil, ErrNotFound
	}
	return i, nil
}

func (e *Engine) GetInstance(ctx context.Context, id InstanceID) (InstanceInfo, error) {
	if err := ctx.Err(); err != nil {
		return InstanceInfo{}, err
	}
	i, err := e.lookup(id)
	if err != nil {
		return InstanceInfo{}, err
	}
	return CloneInfo(i.Info()), nil
}

func (e *Engine) ListInstances(ctx context.Context, q InstanceQuery) (InstancePage, error) {
	page := InstancePage{ObservedAt: time.Now().UTC(), Instances: []InstanceInfo{}}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if q.Project != "" && !ValidName(q.Project) {
		return page, InvalidArgument("project", "invalid project")
	}
	limit := q.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return page, InvalidArgument("limit", "limit must be 1..100")
	}
	infos := []InstanceInfo{}
	for id, i := range e.snapshot() {
		if e.removeDeleted(id, i) {
			continue
		}
		info := CloneInfo(i.Info())
		project, _, _ := strings.Cut(info.Name, "/")
		if q.Project == "" || project == q.Project {
			infos = append(infos, info)
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	start := 0
	if q.Cursor != "" {
		found := false
		for n, i := range infos {
			if string(i.ID) == q.Cursor {
				start = n + 1
				found = true
				break
			}
		}
		if !found {
			return page, InvalidArgument("cursor", "cursor is not in the selected Instances")
		}
	}
	end := min(start+limit, len(infos))
	page.Instances = infos[start:end]
	if end < len(infos) {
		page.NextCursor = string(infos[end-1].ID)
	}
	return page, nil
}

func (e *Engine) InitInstance(ctx context.Context, name string, config InstanceConfig) (InstanceInfo, error) {
	if err := e.ready(); err != nil {
		return InstanceInfo{}, err
	}
	if !ValidInstanceName(name) {
		return InstanceInfo{}, InvalidArgument("name", "name must be project/name")
	}
	if config.Values == nil {
		return InstanceInfo{}, MissingArgument("values")
	}
	values, err := Normalize(config.Values)
	if err != nil {
		return InstanceInfo{}, InvalidArgument("values", "values must contain JSON data")
	}
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return InstanceInfo{}, ErrClosed
	}
	e.initializing.Add(1)
	e.mu.Unlock()
	defer e.initializing.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.lifetime, cancel)
	defer stop()
	e.creation.Lock()
	defer e.creation.Unlock()
	if checkErr := ctx.Err(); checkErr != nil {
		return InstanceInfo{}, checkErr
	}
	for id, i := range e.snapshot() {
		if !e.removeDeleted(id, i) && i.Info().Name == name {
			return InstanceInfo{}, ErrAlreadyExists
		}
	}
	blueprint, err := e.blueprints.Resolve(ctx, config.Blueprint)
	if err != nil {
		return InstanceInfo{}, err
	}
	if checkErr := e.blueprints.ValidateValues(ctx, blueprint, values); checkErr != nil {
		return InstanceInfo{}, checkErr
	}
	rawID, err := newUUID()
	if err != nil {
		return InstanceInfo{}, err
	}
	id := InstanceID(rawID)
	if checkErr := e.backend.InitInstance(ctx, id, name, blueprint, values); checkErr != nil {
		return InstanceInfo{}, checkErr
	}
	instance, err := e.backend.SpawnInstance(ctx, id)
	if err != nil {
		return InstanceInfo{}, err
	}
	if IsNil(instance) {
		return InstanceInfo{}, errors.New("backend returned no Instance")
	}
	e.mu.Lock()
	e.instances[id] = instance
	stopping := e.stopping
	e.mu.Unlock()
	if stopping {
		return InstanceInfo{}, errors.Join(ErrClosed, instance.Close(context.Background()))
	}
	return CloneInfo(instance.Info()), nil
}

func (e *Engine) SubmitCommand(ctx context.Context, id InstanceID, command string, args Arguments, key string) (CommandID, error) {
	if err := e.ready(); err != nil {
		return "", err
	}
	i, err := e.lookup(id)
	if err != nil {
		return "", err
	}
	if i.Info().Status == InstanceFailed {
		return "", ErrInstanceFailed
	}
	rawID, err := newUUID()
	if err != nil {
		return "", err
	}
	args, err = NormalizeArguments(args)
	if err != nil {
		return "", InvalidArgument("args", "arguments must contain JSON data")
	}
	return i.SubmitCommand(ctx, CommandRequest{ID: CommandID(rawID), Command: command, Args: args, IdempotencyKey: key})
}

func (e *Engine) GetCommandExecution(ctx context.Context, id InstanceID, command CommandID) (CommandExecution, error) {
	i, err := e.lookup(id)
	if err != nil {
		return CommandExecution{}, err
	}
	return i.GetCommandExecution(ctx, command)
}

func (e *Engine) ListCommandExecutions(ctx context.Context, id InstanceID, q CommandExecutionQuery) (CommandExecutionPage, error) {
	i, err := e.lookup(id)
	if err != nil {
		return CommandExecutionPage{}, err
	}
	return i.ListCommandExecutions(ctx, q)
}

func (e *Engine) Wait(ctx context.Context, id InstanceID, command CommandID) (CommandExecution, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := e.GetCommandExecution(ctx, id, command)
		if err != nil {
			return CommandExecution{}, err
		}
		if Terminal(record.Status) {
			return record, nil
		}
		select {
		case <-ctx.Done():
			return CommandExecution{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *Engine) ListCommandDefinitions() []CommandDefinition {
	defs := e.backend.ListCommandDefinitions()
	result := make([]CommandDefinition, len(defs))
	for n, d := range defs {
		result[n] = CloneCommandDefinition(d)
	}
	return result
}

func (e *Engine) Blueprints(ctx context.Context) ([]Blueprint, error) {
	return e.blueprints.List(ctx)
}

func (e *Engine) FetchBlueprint(ctx context.Context, reference string) (Blueprint, error) {
	if err := e.ready(); err != nil {
		return Blueprint{}, err
	}
	return e.blueprints.Fetch(ctx, reference)
}

func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.stopping = true
	e.cancel()
	e.mu.Unlock()
	initialized := make(chan struct{})
	go func() { e.initializing.Wait(); close(initialized) }()
	instances := e.snapshot()
	results := make(chan error, len(instances))
	for _, i := range instances {
		go func() { results <- i.Close(ctx) }()
	}
	var result error
	for range instances {
		select {
		case err := <-results:
			result = errors.Join(result, err)
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		}
	}
	select {
	case <-initialized:
		return result
	case <-ctx.Done():
		return errors.Join(result, ctx.Err())
	}
}
func (e *Engine) Close() { _ = e.Shutdown(context.Background()) } //nolint:errcheck // Convenience cleanup; use Shutdown to observe errors.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
