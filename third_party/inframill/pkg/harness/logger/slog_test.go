package logger_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/pkg/harness"
	harnesslogger "github.com/sir-hassan/inframill/pkg/harness/logger"
)

var _ harness.LoggerFactory = (*harnesslogger.SlogFactory)(nil)

func TestSlogFactoryIntegratesWithBuild(t *testing.T) {
	var output bytes.Buffer
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main: harness.MainConfig{Port: 8080},
				Logger: harness.LoggerConfig{
					Type: harnesslogger.TypeSlog,
					Settings: mustSettings(t, harnesslogger.SlogSettings{
						Level: "info", Format: harnesslogger.FormatJSON,
					}),
				},
			}, nil
		}),
		LoggerFactory:    harnesslogger.NewSlogFactory(&output),
		HandlerFactories: harness.HandlerFactories{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSlogFactoryValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		config    harness.LoggerConfig
		wantPaths []string
	}{
		{name: "root validates missing type", config: harness.LoggerConfig{}},
		{
			name: "unsupported type", config: harness.LoggerConfig{Type: "other"},
			wantPaths: []string{"type"},
		},
		{
			name: "unknown setting",
			config: harness.LoggerConfig{Type: harnesslogger.TypeSlog, Settings: mustSettings(t, struct {
				Destination string `json:"destination"`
			}{Destination: "private-output"})},
			wantPaths: []string{"settings"},
		},
		{
			name: "invalid values",
			config: harness.LoggerConfig{
				Type: harnesslogger.TypeSlog,
				Settings: mustSettings(t, harnesslogger.SlogSettings{
					Level: "verbose", Format: "pretty",
				}),
			},
			wantPaths: []string{"settings.level", "settings.format"},
		},
		{
			name: "valid",
			config: harness.LoggerConfig{
				Type: harnesslogger.TypeSlog,
				Settings: mustSettings(t, harnesslogger.SlogSettings{
					Level: "DEBUG", Format: harnesslogger.FormatText,
				}),
			},
		},
	}

	factory := harnesslogger.NewSlogFactory(&bytes.Buffer{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			violations := factory.Validate(context.Background(), test.config)
			if len(violations) != len(test.wantPaths) {
				t.Fatalf("violations = %#v", violations)
			}
			for index, path := range test.wantPaths {
				if violations[index].Path != path {
					t.Fatalf("violations[%d].path = %q, want %q", index, violations[index].Path, path)
				}
			}
			if strings.Contains(harness.NewValidationError(violations).Error(), "private-output") {
				t.Fatal("validation error exposed a setting value")
			}
		})
	}
}

func TestSlogLoggerHonorsFormatThresholdAndFields(t *testing.T) {
	baseFields := []harness.Field{{Key: "service", Value: "application"}}
	var output bytes.Buffer
	factory := harnesslogger.NewSlogFactory(&output, baseFields...)
	baseFields[0] = harness.Field{Key: "mutated", Value: true}
	created, err := factory.Create(context.Background(), harness.LoggerConfig{
		Type: harnesslogger.TypeSlog,
		Settings: mustSettings(t, harnesslogger.SlogSettings{
			Level: "info", Format: harnesslogger.FormatJSON,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := created.With(harness.Field{Key: "component", Value: "worker"})
	logger.Debug(context.Background(), "hidden")
	logger.Info(context.Background(), "ready", harness.Field{Key: "attempt", Value: 2})
	if err := logger.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	line := output.String()
	for _, expected := range []string{
		`"level":"INFO"`, `"msg":"ready"`, `"service":"application"`,
		`"component":"worker"`, `"attempt":2`,
	} {
		if !strings.Contains(line, expected) {
			t.Fatalf("log = %q, missing %q", line, expected)
		}
	}
	for _, unexpected := range []string{"hidden", "mutated"} {
		if strings.Contains(line, unexpected) {
			t.Fatalf("log = %q, contains %q", line, unexpected)
		}
	}
}

func TestSlogFactoryCreatesTextLogger(t *testing.T) {
	var output bytes.Buffer
	factory := harnesslogger.NewSlogFactory(&output)
	created, err := factory.Create(context.Background(), harness.LoggerConfig{
		Type: harnesslogger.TypeSlog,
		Settings: mustSettings(t, harnesslogger.SlogSettings{
			Level: "warn", Format: harnesslogger.FormatText,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	created.Warn(context.Background(), "attention")
	if line := output.String(); !strings.Contains(line, "level=WARN") || !strings.Contains(line, "msg=attention") {
		t.Fatalf("log = %q", line)
	}
}

func TestSlogFactoryReportsConstructionErrors(t *testing.T) {
	config := harness.LoggerConfig{
		Type: harnesslogger.TypeSlog,
		Settings: mustSettings(t, harnesslogger.SlogSettings{
			Level: "info", Format: harnesslogger.FormatJSON,
		}),
	}
	for _, factory := range []*harnesslogger.SlogFactory{nil, harnesslogger.NewSlogFactory(nil)} {
		if _, err := factory.Create(context.Background(), config); err == nil {
			t.Fatal("Create succeeded without an output")
		}
	}
	config.Type = "other"
	if _, err := harnesslogger.NewSlogFactory(&bytes.Buffer{}).Create(context.Background(), config); err == nil {
		t.Fatal("Create succeeded with an unsupported type")
	}
}

func mustSettings(t *testing.T, value any) harness.Settings {
	t.Helper()
	settings, err := harness.NewSettings(value)
	if err != nil {
		t.Fatal(err)
	}

	return settings
}
