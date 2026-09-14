package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sir-hassan/inframill/pkg/driver/internal/process"
)

type configuredDriver struct {
	config      Executable
	environment map[string]string
	secrets     map[string]process.Secret
	gate        chan struct{}
	mu          sync.RWMutex
	health      Entry
}

func newConfiguredDriver(id string, config Executable, environ []string) *configuredDriver {
	// Resolve once so later cwd changes cannot select another executable.
	if strings.TrimSpace(config.Path) != "" {
		if absolute, err := filepath.Abs(config.Path); err == nil {
			config.Path = absolute
		}
	}
	entry := &configuredDriver{config: config, gate: make(chan struct{}, 1),
		environment: map[string]string{}, secrets: map[string]process.Secret{},
		health: Entry{ID: id, Executable: config, Status: Offline}}
	for _, item := range environ {
		name, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "INFRAMILL_SECRET_") {
			entry.secrets[name] = secretValue(value)
		}
		switch name {
		case "HOME", "PATH", "TMPDIR", "SSL_CERT_DIR", "SSL_CERT_FILE", "SSH_AUTH_SOCK":
			entry.environment[name] = value
		}
	}
	return entry
}

func (entry *configuredDriver) probe(ctx context.Context) (*executable, error) {
	select {
	case entry.gate <- struct{}{}:
		defer func() { <-entry.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e := &executable{path: entry.config.Path, prefix: entry.config.Args, environment: entry.environment, secrets: entry.secrets}
	err := e.probe(ctx)
	entry.mu.Lock()
	entry.health.CheckedAt = time.Now().UTC()
	entry.health.Info = nil
	entry.health.Reason = ""
	entry.health.Status = Connected
	if err != nil {
		entry.health.Status = Offline
		entry.health.Reason = e.redactString(err.Error())
	} else {
		entry.health.Info = &e.info
	}
	entry.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (e *executable) probe(ctx context.Context) error {
	stat, err := os.Stat(e.path)
	if strings.TrimSpace(e.path) == "" || err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0o111 == 0 {
		return errors.New("driver executable is missing or not executable")
	}
	for _, arg := range e.prefix {
		if strings.ContainsRune(arg, '\x00') {
			return errors.New("invalid Driver prefix argument")
		}
	}
	probe, stop := context.WithTimeout(ctx, probeTimeout)
	defer stop()
	data, err := e.invoke(probe, []string{"info"}, nil, MaxInfo, nil, false)
	if err != nil {
		return err
	}
	if err := decode(data, &e.info); err != nil {
		return err
	}
	return e.info.Validate()
}
