// Package server composes Inframill's engine and HTTP handlers through harness.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/internal/appconfig"
	"github.com/sir-hassan/inframill/internal/backend"
	"github.com/sir-hassan/inframill/internal/blueprint"
	"github.com/sir-hassan/inframill/internal/connectadapter"
	driver "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/harness"
	harnessLogger "github.com/sir-hassan/inframill/pkg/harness/logger"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

// Serve loads one configuration, composes resources and runs until cancellation.
func Serve(ctx context.Context, environ []string, configPath, version string, stderr io.Writer) error {
	app, err := build(ctx, environ, configPath, version, stderr)
	if err != nil {
		return err
	}
	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run inframill app: %w", err)
	}
	return nil
}

func build(ctx context.Context, environ []string, configPath, version string, stderr io.Writer) (*harness.App, error) {
	loaded, err := appconfig.Load(ctx, configPath, environ)
	if err != nil {
		return nil, fmt.Errorf("load application configuration: %w", err)
	}
	config := loaded.Harness
	config.Main = mainDefaults(config.Main)
	environment := snapshotEnvironment(environ)
	packageRoot, dataRoot := filepath.Join(loaded.DataDir, "blueprints"), filepath.Join(loaded.DataDir, "instances")
	registry, err := driver.New(ctx, loaded.Drivers, environ)
	if err != nil {
		return nil, fmt.Errorf("load Drivers: %w", err)
	}
	blueprints, err := blueprint.NewStore(packageRoot, strings.Split(environment["INFRAMILL_HTTP_REGISTRIES"], ","))
	if err != nil {
		return nil, fmt.Errorf("create Blueprint store: %w", err)
	}
	store, err := backend.New(dataRoot, registry, backend.Options{})
	if err != nil {
		return nil, fmt.Errorf("create backend: %w", err)
	}
	engine, err := inframill.New(ctx, blueprints, store)
	if err != nil {
		return nil, err
	}
	app, err := harness.Build(ctx, harness.Options{
		ConfigLoader:     harness.ConfigLoaderFunc(func(loadCtx context.Context) (harness.Config, error) { return config, loadCtx.Err() }),
		LoggerFactory:    harnessLogger.NewSlogFactory(stderr, harness.Field{Key: "service", Value: "inframill"}, harness.Field{Key: "version", Value: version}),
		HandlerFactories: connectadapter.NewHandlerFactories(engine),
		Cleanup:          engine.Shutdown,
	})
	if err != nil {
		return nil, fmt.Errorf("build inframill app: %w", err)
	}
	if err := engine.Start(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("start engine: %w", err), app.Close(context.WithoutCancel(ctx)))
	}
	for _, failure := range engine.StartupFailures() {
		_, _ = fmt.Fprintf(stderr, "inframill: Instance %s unavailable: %s\n", failure.ID, failure.Message) //nolint:errcheck // Startup diagnostic, healthy Instances remain available.
	}
	return app, nil
}

func mainDefaults(config harness.MainConfig) harness.MainConfig {
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 30 * time.Second
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = 150 * time.Second
	}
	return config
}

func snapshotEnvironment(environ []string) map[string]string {
	result := make(map[string]string, len(environ))
	for _, entry := range environ {
		if name, value, ok := strings.Cut(entry, "="); ok {
			result[name] = value
		}
	}
	return result
}
