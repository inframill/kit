package config_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/pkg/harness"
	harnessconfig "github.com/sir-hassan/inframill/pkg/harness/config"
)

func TestYAMLFileLoadsTypedConfigAndResolvesSettings(t *testing.T) {
	path := writeYAML(t, `
main:
  port: 9090
  databaseUrl: ${DATABASE_URL}
  shutdownTimeout: 3s
logger:
  type: test
  settings:
    token: ${SECRET}
    literal: $${NOT_A_REFERENCE}
handlers:
  - type: test
    settings:
      path: /api
      nested:
        token: ${SECRET}
      audiences:
        - public-client
workers:
  - type: test
    settings: {}
`)
	environ := []string{"SECRET=first", "DATABASE_URL=postgres://database"}
	loader := harnessconfig.NewYAMLFile(path, harnessconfig.WithEnvironment(environ))
	environ[0] = "SECRET=mutated"

	loaded, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Main.Port != 9090 || loaded.Main.DatabaseURL != "postgres://database" || loaded.Main.ShutdownTimeout != 3*time.Second {
		t.Fatalf("main = %#v", loaded.Main)
	}
	if loaded.Logger.Type != "test" || len(loaded.Handlers) != 1 || len(loaded.Workers) != 1 {
		t.Fatalf("config = %#v", loaded)
	}
	var loggerSettings struct {
		Token   string `json:"token"`
		Literal string `json:"literal"`
	}
	if err := loaded.Logger.Settings.Decode(&loggerSettings); err != nil {
		t.Fatal(err)
	}
	if loggerSettings.Token != "first" || loggerSettings.Literal != "${NOT_A_REFERENCE}" {
		t.Fatalf("logger settings = %#v", loggerSettings)
	}
	var handlerSettings struct {
		Path   string `json:"path"`
		Nested struct {
			Token string `json:"token"`
		} `json:"nested"`
		Audiences []string `json:"audiences"`
	}
	if err := loaded.Handlers[0].Settings.Decode(&handlerSettings); err != nil {
		t.Fatal(err)
	}
	if handlerSettings.Path != "/api" || handlerSettings.Nested.Token != "first" || !reflect.DeepEqual(handlerSettings.Audiences, []string{"public-client"}) {
		t.Fatalf("handler settings = %#v", handlerSettings)
	}
}

func TestYAMLFileReadsANewSnapshotForEachLoad(t *testing.T) {
	path := writeYAML(t, minimalYAML(8080))
	loader := harnessconfig.NewYAMLFile(path)

	first, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(minimalYAML(9090)), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Main.Port != 8080 || second.Main.Port != 9090 {
		t.Fatalf("ports = %d, %d", first.Main.Port, second.Main.Port)
	}
}

func TestYAMLFileIntegratesWithBuild(t *testing.T) {
	path := writeYAML(t, `
main:
  port: 8080
logger:
  type: test
handlers:
  - type: test
    settings:
      path: /ready
workers: []
cli:
  enabled: true
`)
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader:     harnessconfig.NewYAMLFile(path, harnessconfig.WithAllowedSections("cli")),
		LoggerFactory:    buildLoggerFactory{},
		HandlerFactories: harness.HandlerFactories{"test": buildHandlerFactory{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestYAMLFileRejectsInvalidSourceContentWithoutExposingValues(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		environ   []string
		wantPath  string
		forbidden string
	}{
		{name: "empty", content: "", wantPath: "config"},
		{name: "syntax", content: "main: [super-secret", wantPath: "config", forbidden: "super-secret"},
		{name: "multiple documents", content: minimalYAML(8080) + "\n---\n{}\n", wantPath: "config"},
		{name: "unknown top-level field", content: "unknown: value\n", wantPath: "unknown", forbidden: "value"},
		{name: "unknown main field", content: "main:\n  databasePassword: super-secret\n", wantPath: "main.databasePassword", forbidden: "super-secret"},
		{name: "main environment reference", content: "main:\n  port: ${PORT}\n", environ: []string{"PORT=8080"}, wantPath: "main.port", forbidden: "8080"},
		{name: "missing main database environment", content: "main:\n  databaseUrl: ${DATABASE_URL}\n", wantPath: "main.databaseUrl"},
		{name: "null settings", content: "logger:\n  type: test\n  settings: null\n", wantPath: "logger.settings"},
		{name: "sequence settings", content: "logger:\n  type: test\n  settings: []\n", wantPath: "logger.settings"},
		{name: "scalar settings", content: "logger:\n  type: test\n  settings: super-secret\n", wantPath: "logger.settings", forbidden: "super-secret"},
		{name: "missing environment", content: "logger:\n  type: test\n  settings:\n    token: ${MISSING}\n", wantPath: "logger.settings.token"},
		{name: "partial environment reference", content: "logger:\n  type: test\n  settings:\n    token: prefix-${SECRET}\n", environ: []string{"SECRET=super-secret"}, wantPath: "logger.settings.token", forbidden: "super-secret"},
		{name: "handler name", content: "handlers:\n  - name: api\n", wantPath: "handlers[0].name"},
		{name: "top-level handler path", content: "handlers:\n  - path: /api\n", wantPath: "handlers[0].path"},
		{name: "unknown handler field", content: "handlers:\n  - route: /api\n", wantPath: "handlers[0].route"},
		{name: "worker name", content: "workers:\n  - name: job\n", wantPath: "workers[0].name"},
		{name: "unknown worker field", content: "workers:\n  - path: /job\n", wantPath: "workers[0].path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loader := harnessconfig.NewYAMLFile(
				writeYAML(t, test.content),
				harnessconfig.WithEnvironment(test.environ),
			)
			_, err := loader.Load(context.Background())
			var validation *harness.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("Load error = %T %v", err, err)
			}
			violations := validation.Violations()
			if len(violations) == 0 || violations[0].Path != test.wantPath {
				t.Fatalf("violations = %#v, want first path %q", violations, test.wantPath)
			}
			if test.forbidden != "" && strings.Contains(err.Error(), test.forbidden) {
				t.Fatalf("error exposes %q: %v", test.forbidden, err)
			}
		})
	}
}

func TestYAMLFilePreservesOperationalErrorIdentity(t *testing.T) {
	loader := harnessconfig.NewYAMLFile(filepath.Join(t.TempDir(), "missing.yaml"))
	_, err := loader.Load(context.Background())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load error = %T %v", err, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = loader.Load(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Load error = %T %v", err, err)
	}
}

func minimalYAML(port int) string {
	return "main:\n  port: " + strconv.Itoa(port) + "\nlogger:\n  type: test\nhandlers: []\nworkers: []\n"
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inframill.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

type buildLoggerFactory struct{}

func (buildLoggerFactory) Validate(context.Context, harness.LoggerConfig) harness.Violations {
	return nil
}

func (buildLoggerFactory) Create(context.Context, harness.LoggerConfig) (harness.Logger, error) {
	return buildLogger{}, nil
}

type buildLogger struct{}

func (buildLogger) Debug(context.Context, string, ...harness.Field) {}
func (buildLogger) Info(context.Context, string, ...harness.Field)  {}
func (buildLogger) Warn(context.Context, string, ...harness.Field)  {}
func (buildLogger) Error(context.Context, string, ...harness.Field) {}
func (logger buildLogger) With(...harness.Field) harness.Logger     { return logger }
func (buildLogger) Close(context.Context) error                     { return nil }

type buildHandlerFactory struct{}

func (buildHandlerFactory) Validate(_ context.Context, config harness.HandlerConfig) (string, harness.Violations) {
	var settings struct {
		Path string `json:"path"`
	}
	if err := config.Settings.Decode(&settings); err != nil {
		return "", harness.Violations{{Path: "settings", Code: "invalid", Message: err.Error()}}
	}

	return settings.Path, nil
}

func (buildHandlerFactory) Create(context.Context, harness.Logger, http.Handler, harness.HandlerConfig) (http.Handler, error) {
	return http.NotFoundHandler(), nil
}
