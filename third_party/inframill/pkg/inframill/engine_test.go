package inframill_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

const fixtureID inframill.InstanceID = "00000000-0000-4000-8000-000000000001"

type engineSource struct {
	inframill.BlueprintSource
	invalid bool
	fetched bool
}

func (s *engineSource) Resolve(context.Context, string) (inframill.Blueprint, error) {
	return inframill.Blueprint{Reference: "registry.example/test@sha256:fixed"}, nil
}

func (s *engineSource) ValidateValues(context.Context, inframill.Blueprint, inframill.InstanceValues) error {
	if s.invalid {
		return errors.New("invalid values")
	}
	return nil
}

func (s *engineSource) Fetch(context.Context, string) (inframill.Blueprint, error) {
	s.fetched = true
	return inframill.Blueprint{}, nil
}

type engineInstance struct {
	inframill.Instance
	info      inframill.InstanceInfo
	submitted []inframill.CommandRequest
	deleted   bool
	closed    chan struct{}
	once      sync.Once
}

func (i *engineInstance) Info() inframill.InstanceInfo { return inframill.CloneInfo(i.info) }
func (i *engineInstance) Deleted() bool                { return i.deleted }
func (i *engineInstance) Close(context.Context) error {
	i.once.Do(func() { close(i.closed) })
	return nil
}

func (i *engineInstance) SubmitCommand(_ context.Context, r inframill.CommandRequest) (inframill.CommandID, error) {
	i.submitted = append(i.submitted, r)
	return r.ID, nil
}

type engineBackend struct {
	inframill.Backend
	instances   map[inframill.InstanceID]*engineInstance
	initialized int
	block       chan struct{}
	entered     chan struct{}
}

func (b *engineBackend) GetAllIDs(context.Context) ([]inframill.InstanceID, error) {
	ids := make([]inframill.InstanceID, 0, len(b.instances))
	for id := range b.instances {
		ids = append(ids, id)
	}
	return ids, nil
}

func (b *engineBackend) SpawnInstance(_ context.Context, id inframill.InstanceID) (inframill.Instance, error) {
	return b.instances[id], nil
}

func (b *engineBackend) InitInstance(ctx context.Context, id inframill.InstanceID, name string, bp inframill.Blueprint, values inframill.InstanceValues) error {
	b.initialized++
	if b.block != nil {
		close(b.entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.block:
		}
	}
	b.instances[id] = &engineInstance{info: inframill.InstanceInfo{ID: id, Name: name, Values: values, Blueprint: bp.Reference, Status: inframill.InstanceIdle}, closed: make(chan struct{})}
	return nil
}

func engineFixture(t *testing.T) (*inframill.Engine, *engineSource, *engineBackend) {
	t.Helper()
	s := &engineSource{}
	b := &engineBackend{instances: map[inframill.InstanceID]*engineInstance{fixtureID: {info: inframill.InstanceInfo{ID: fixtureID, Name: "project/existing", Status: inframill.InstanceIdle}, closed: make(chan struct{})}}}
	e, err := inframill.New(t.Context(), s, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, s, b
}

func TestEngineInitializesValidatedValues(t *testing.T) {
	e, s, b := engineFixture(t)
	values := inframill.InstanceValues{"nested": map[string]any{"count": 1}}
	info, err := e.InitInstance(t.Context(), "project/dev", inframill.InstanceConfig{Blueprint: testBlueprint, Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if !inframill.ValidUUID(string(info.ID)) || info.ID == fixtureID || info.Name != "project/dev" || s.fetched {
		t.Fatal(info, s.fetched)
	}
	info.Values["nested"] = changedValue
	got, err := e.GetInstance(t.Context(), info.ID)
	if err != nil || got.Values["nested"] == changedValue {
		t.Fatal(got, err)
	}
	if _, err := e.InitInstance(t.Context(), "project/dev", inframill.InstanceConfig{Blueprint: testBlueprint, Values: values}); !errors.Is(err, inframill.ErrAlreadyExists) {
		t.Fatal(err)
	}
	s.invalid = true
	if _, err := e.InitInstance(t.Context(), "project/invalid", inframill.InstanceConfig{Blueprint: testBlueprint, Values: values}); err == nil || b.initialized != 1 {
		t.Fatal(err, b.initialized)
	}
}

func TestEngineRoutesUUIDCommandsAndRejectsFailed(t *testing.T) {
	e, _, b := engineFixture(t)
	i := b.instances[fixtureID]
	for range 2 {
		if _, err := e.SubmitCommand(t.Context(), fixtureID, "custom.action", inframill.Arguments{"item": "original"}, "retry"); err != nil {
			t.Fatal(err)
		}
	}
	if len(i.submitted) != 2 || i.submitted[0].ID == i.submitted[1].ID || !inframill.ValidUUID(string(i.submitted[0].ID)) || i.submitted[0].Command != "custom.action" {
		t.Fatal(i.submitted)
	}
	i.info.Status = inframill.InstanceFailed
	if _, err := e.SubmitCommand(t.Context(), fixtureID, "custom.action", nil, ""); !errors.Is(err, inframill.ErrInstanceFailed) || len(i.submitted) != 2 {
		t.Fatal(err)
	}
}

func TestEnginePrunesDeletedInstances(t *testing.T) {
	e, _, b := engineFixture(t)
	b.instances[fixtureID].deleted = true
	if _, err := e.GetInstance(t.Context(), fixtureID); !errors.Is(err, inframill.ErrNotFound) {
		t.Fatal(err)
	}
	page, err := e.ListInstances(t.Context(), inframill.InstanceQuery{})
	if err != nil || len(page.Instances) != 0 {
		t.Fatal(page, err)
	}
}

func TestShutdownCancelsInitializationAndClosesLiveInstances(t *testing.T) {
	e, _, b := engineFixture(t)
	b.block = make(chan struct{})
	b.entered = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := e.InitInstance(context.Background(), "project/new", inframill.InstanceConfig{Blueprint: testBlueprint, Values: inframill.InstanceValues{}})
		done <- err
	}()
	<-b.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-b.instances[fixtureID].closed:
	default:
		t.Fatal("live instance not closed")
	}
	if _, err := e.SubmitCommand(t.Context(), fixtureID, "custom.action", nil, ""); !errors.Is(err, inframill.ErrClosed) {
		t.Fatal(err)
	}
}

func TestEngineRejectsNilDependencies(t *testing.T) {
	if _, err := inframill.New(t.Context(), nil, nil); err == nil {
		t.Fatal("accepted nil dependencies")
	}
}

const (
	testBlueprint = "test"
)
