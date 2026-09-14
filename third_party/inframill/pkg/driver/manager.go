// Package driver manages configured executable Drivers independently of the application.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Manager owns discovery, Blueprint validation and command preparation.
// List returns cached health. Validate and Prepare refresh the entries they use.
// Implementations must support concurrent calls and honor context cancellation.
type Manager interface {
	List(context.Context) ([]Entry, error)
	Validate(context.Context, ValidationRequest) error
	Prepare(context.Context, CommandRequest) (PreparedCommand, error)
}

type manager struct {
	entries map[string]*configuredDriver
	ids     []string
}

// New probes every explicitly configured executable. Individual failures become
// offline entries. Invalid IDs or caller cancellation prevent construction.
// environ is a startup snapshot; only allowlisted variables reach child processes,
// and INFRAMILL_SECRET_* credentials are injected only into run commands.
func New(ctx context.Context, configured Configurations, environ []string) (Manager, error) {
	m := &manager{entries: make(map[string]*configuredDriver, len(configured))}
	for id := range configured {
		if !ValidID(id) {
			return nil, errors.New("invalid or reserved configured Driver ID")
		}
		m.ids = append(m.ids, id)
	}
	sort.Strings(m.ids)
	for _, id := range m.ids {
		config := configured[id]
		config.Args = append([]string(nil), config.Args...)
		entry := newConfiguredDriver(id, config, environ)
		m.entries[id] = entry
		_, _ = entry.probe(ctx) // Individual failures are represented in health.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *manager) List(ctx context.Context) ([]Entry, error) {
	result := make([]Entry, 0, len(m.ids))
	for _, id := range m.ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := m.entries[id]
		entry.mu.RLock()
		snapshot, err := copyJSON(entry.health)
		entry.mu.RUnlock()
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *manager) selectDriver(ctx context.Context, id string) (*executable, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := m.entries[id]
	if !ok {
		return nil, fmt.Errorf("drivers.%s: unknown configured Driver", id)
	}
	e, err := entry.probe(ctx)
	if err != nil {
		return nil, fmt.Errorf("drivers.%s: offline: %w", id, err)
	}
	return e, nil
}

func (m *manager) Validate(ctx context.Context, input ValidationRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := copyJSON(input)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(request.Drivers))
	for id := range request.Drivers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e, err := m.selectDriver(ctx, id)
		if err != nil {
			return err
		}
		config := request.Drivers[id]
		if config == nil {
			return fmt.Errorf("drivers.%s: configuration must be an object", id)
		}
		// Preserve the protocol's separate config, source and instance validation phases.
		phases := []Request{{Config: config}}
		if request.BlueprintDirectory != "" {
			phases = append(phases, Request{BlueprintDirectory: request.BlueprintDirectory, Config: config})
		}
		if request.Binding != nil {
			phases = append(phases, Request{Binding: request.Binding, Values: request.Values, Config: config})
		}
		for _, phase := range phases {
			if err := e.validate(ctx, phase); err != nil {
				return fmt.Errorf("drivers.%s: %w", id, err)
			}
		}
	}
	return ctx.Err()
}

func (m *manager) Prepare(ctx context.Context, request CommandRequest) (PreparedCommand, error) {
	e, err := m.selectDriver(ctx, request.DriverID)
	if err != nil {
		return nil, err
	}
	if _, ok := e.info.Commands[request.Command]; !ok {
		return nil, errors.New("unsupported Driver command")
	}
	prepared, err := e.Prepare(ctx, request.Scope, request.Command, request.Arguments)
	if err != nil {
		return nil, err
	}
	return safeCommand{prepared}, nil
}

// copyJSON normalizes dynamic JSON and isolates maps/slices at ownership boundaries.
func copyJSON[T any](input T) (T, error) {
	var output T
	data, err := json.Marshal(input)
	if err != nil {
		return output, errors.New("invalid Driver JSON input")
	}
	if err := decode(data, &output); err != nil {
		return output, err
	}
	return output, nil
}
