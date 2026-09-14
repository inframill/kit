// Package harness builds and runs configuration-driven Go applications.
package harness

import (
	"context"
	"net/http"
	"time"
)

// ConfigLoader loads one complete application configuration snapshot.
type ConfigLoader interface {
	Load(ctx context.Context) (Config, error)
}

// ConfigLoaderFunc adapts a function to ConfigLoader.
type ConfigLoaderFunc func(context.Context) (Config, error)

// Load calls fn.
func (fn ConfigLoaderFunc) Load(ctx context.Context) (Config, error) { return fn(ctx) }

// Config describes one Hummus application configuration snapshot.
type Config struct {
	Main     MainConfig      `json:"main"     yaml:"main"`
	Logger   LoggerConfig    `json:"logger"   yaml:"logger"`
	Handlers []HandlerConfig `json:"handlers" yaml:"handlers"`
	Workers  []WorkerConfig  `json:"workers"  yaml:"workers"`
	// Sections contains application-owned top-level objects. The YAML loader
	// decodes each object as map[string]any. Applications own their validation
	// and pass their values explicitly to application dependencies.
	Sections map[string]any `json:"sections,omitempty" yaml:",inline"`
}

// MainConfig contains process-wide settings owned by Hummus.
type MainConfig struct {
	Port              int           `json:"port"              yaml:"port"`
	DatabaseURL       string        `json:"databaseUrl"       yaml:"databaseUrl"`
	ShutdownTimeout   time.Duration `json:"shutdownTimeout"   yaml:"shutdownTimeout"`
	ReadHeaderTimeout time.Duration `json:"readHeaderTimeout" yaml:"readHeaderTimeout"`
	ReadTimeout       time.Duration `json:"readTimeout"       yaml:"readTimeout"`
	WriteTimeout      time.Duration `json:"writeTimeout"      yaml:"writeTimeout"`
	IdleTimeout       time.Duration `json:"idleTimeout"       yaml:"idleTimeout"`
}

// LoggerConfig selects the application logger implementation.
type LoggerConfig struct {
	Type     string   `json:"type"     yaml:"type"`
	Settings Settings `json:"settings" yaml:"settings"`
}

// HandlerConfig selects one HTTP handler. Its factory owns all handler settings,
// including the mount path.
type HandlerConfig struct {
	Type     string   `json:"type"     yaml:"type"`
	Settings Settings `json:"settings" yaml:"settings"`
}

// WorkerConfig selects one supervised background worker.
type WorkerConfig struct {
	Type     string   `json:"type"     yaml:"type"`
	Settings Settings `json:"settings" yaml:"settings"`
}

// LoggerFactory validates and creates the one application logger.
type LoggerFactory interface {
	Validate(ctx context.Context, config LoggerConfig) Violations
	Create(ctx context.Context, config LoggerConfig) (Logger, error)
}

// HandlerFactory validates and creates configured HTTP handlers. Validate
// returns the mount path resolved from the factory-owned settings. The supplied
// http.Handler is Hummus's immutable in-process dispatcher.
type HandlerFactory interface {
	Validate(ctx context.Context, config HandlerConfig) (string, Violations)
	Create(ctx context.Context, logger Logger, dispatcher http.Handler, config HandlerConfig) (http.Handler, error)
}

// HandlerFactories selects HTTP handler factories by the exact configured type.
type HandlerFactories map[string]HandlerFactory

// WorkerFactory validates and creates configured background workers.
type WorkerFactory interface {
	Validate(ctx context.Context, config WorkerConfig) Violations
	Create(ctx context.Context, logger Logger, config WorkerConfig) (Worker, error)
}

// WorkerFactories selects background worker factories by the exact configured type.
type WorkerFactories map[string]WorkerFactory

// Worker is one supervised background activity.
type Worker interface {
	Run(ctx context.Context) error
}

// WorkerFunc adapts a function to Worker.
type WorkerFunc func(context.Context) error

// Run calls fn.
func (fn WorkerFunc) Run(ctx context.Context) error { return fn(ctx) }

const (
	defaultShutdownTimeout   = 10 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
)

func withMainDefaults(config MainConfig) MainConfig {
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.ReadHeaderTimeout == 0 {
		config.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultReadTimeout
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = defaultWriteTimeout
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = defaultIdleTimeout
	}

	return config
}
