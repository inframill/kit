package runmill

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Host owns a fixed startup catalog. Load probes once; all other
// operations use that catalog. Implementations support concurrent calls.
type Host interface {
	Load(ctx context.Context, configs map[string]Executable) error
	List(ctx context.Context) ([]ProviderSnapshot, error)
	ValidateSettings(ctx context.Context, providerID string, settings Settings) error
	Execute(ctx context.Context, request Execution, secrets map[string]string) (any, error)
}

var (
	ErrNotLoaded     = errors.New("Provider Host is not loaded")
	ErrAlreadyLoaded = errors.New("Provider Host is already loaded; restart with a new Host")
)

type loadedProvider struct {
	health ProviderSnapshot
	client *providerClient
}

type host struct {
	gate    chan struct{}
	mu      sync.RWMutex
	loaded  bool
	environ []string
	entries map[string]*loadedProvider
	ids     []string
}

// NewHost copies the startup environment without I/O. Call Load before use. Only the
// allowlisted environment reaches subprocesses; secrets are supplied to Execute.
func NewHost(environ []string) Host {
	return &host{gate: make(chan struct{}, 1), environ: append([]string(nil), environ...)}
}

// Load publishes one complete catalog. Individual Provider failures become offline
// entries. Structural errors and cancellation leave the Host unloaded and retryable.
// A successful load, including an empty catalog, permanently rejects later loads.
func (m *host) Load(ctx context.Context, configured map[string]Executable) error {
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	loaded := m.loaded
	m.mu.RUnlock()
	if loaded {
		return ErrAlreadyLoaded
	}
	configs, err := copyJSON(configured)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(configs))
	for id := range configs {
		if !ValidID(id) {
			return errors.New("invalid configured Provider ID")
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entries := make(map[string]*loadedProvider, len(configs))
	for _, id := range ids {
		health, e := loadExecutable(ctx, id, configs[id], m.environ)
		loaded := &loadedProvider{health: health, client: e}
		entries[id] = loaded
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	m.entries, m.ids, m.loaded = entries, ids, true
	m.environ = nil
	return nil
}

func (m *host) List(ctx context.Context) ([]ProviderSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.loaded {
		return nil, ErrNotLoaded
	}
	result := make([]ProviderSnapshot, 0, len(m.ids))
	for _, id := range m.ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshot, err := copyJSON(m.entries[id].health)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, ctx.Err()
}

func (m *host) provider(ctx context.Context, id string) (*loadedProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.loaded {
		return nil, ErrNotLoaded
	}
	entry, ok := m.entries[id]
	if !ok {
		return nil, fmt.Errorf("providers.%s: unknown configured Provider", id)
	}
	if entry.health.Availability != Available {
		return nil, fmt.Errorf("providers.%s: unavailable: %s", id, entry.health.Reason)
	}
	return entry, nil
}

func (m *host) ValidateSettings(ctx context.Context, id string, input Settings) error {
	entry, err := m.provider(ctx, id)
	if err != nil {
		return err
	}
	request, err := copyJSON(settingsParams{Settings: input})
	if err != nil {
		return err
	}
	if err := request.validate(); err != nil {
		return err
	}
	if err := entry.client.validateSettings(ctx, request); err != nil {
		return fmt.Errorf("providers.%s: %w", id, err)
	}
	return ctx.Err()
}

// Execute checks capabilities and transport inputs, then starts exactly one run process.
func (m *host) Execute(ctx context.Context, request Execution, secrets map[string]string) (any, error) {
	entry, err := m.provider(ctx, request.ProviderID)
	if err != nil {
		return nil, err
	}
	if _, ok := entry.health.Info.Commands[request.Command]; !ok {
		return nil, errors.New("unsupported Provider command")
	}
	return entry.client.Execute(ctx, request.Context, request.Command, request.Arguments, secrets)
}

// Executable selects a file and a literal argument prefix; no shell or PATH lookup is used.
type Executable struct {
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// ValidID reports whether an ID can namespace a Provider's commands.
func ValidID(id string) bool { return idPattern.MatchString(id) }

type Availability string

const (
	Available   Availability = "available"
	Unavailable Availability = "unavailable"
)

// ProviderSnapshot is an independent startup snapshot of discovery.
// It is not a live connection or a guarantee that execution will succeed.
// Info is absent when unavailable. Reason is nonempty when unavailable.
type ProviderSnapshot struct {
	ID           string        `json:"id"`
	Executable   Executable    `json:"executable"`
	Availability Availability  `json:"availability"`
	Reason       string        `json:"reason,omitempty"`
	Info         *ProviderInfo `json:"info,omitempty"`
	CheckedAt    time.Time     `json:"checkedAt"`
}
