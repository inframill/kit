package harness_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/sir-hassan/inframill/pkg/harness"
)

type testLogger struct {
	mu     sync.Mutex
	events []string
	closed int
}

func (*testLogger) Debug(context.Context, string, ...harness.Field) {}
func (*testLogger) Info(context.Context, string, ...harness.Field)  {}
func (*testLogger) Warn(context.Context, string, ...harness.Field)  {}
func (*testLogger) Error(context.Context, string, ...harness.Field) {}
func (logger *testLogger) With(...harness.Field) harness.Logger     { return logger }
func (logger *testLogger) Close(context.Context) error {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.closed++
	logger.events = append(logger.events, "logger")
	return nil
}

type loggerFactory struct {
	logger     *testLogger
	violations harness.Violations
	created    int
}

func (factory *loggerFactory) Validate(context.Context, harness.LoggerConfig) harness.Violations {
	return factory.violations
}

func (factory *loggerFactory) Create(context.Context, harness.LoggerConfig) (harness.Logger, error) {
	factory.created++
	return factory.logger, nil
}

type handlerFactory struct {
	logger     harness.Logger
	dispatcher http.Handler
	paths      map[string]string
	violations map[string]harness.Violations
	handlers   map[string]http.Handler
	createErrs map[string]error
	created    []string
}

func handlerFactories(factory harness.HandlerFactory, componentTypes ...string) harness.HandlerFactories {
	factories := make(harness.HandlerFactories, len(componentTypes))
	for _, componentType := range componentTypes {
		factories[componentType] = factory
	}

	return factories
}

func (factory *handlerFactory) Validate(_ context.Context, config harness.HandlerConfig) (string, harness.Violations) {
	resolved := factory.paths[config.Type]
	if resolved == "" && config.Type != "" {
		resolved = "/" + config.Type
	}

	return resolved, factory.violations[config.Type]
}

func (factory *handlerFactory) Create(_ context.Context, logger harness.Logger, dispatcher http.Handler, config harness.HandlerConfig) (http.Handler, error) {
	factory.logger = logger
	if factory.dispatcher == nil {
		factory.dispatcher = dispatcher
	} else if factory.dispatcher != dispatcher {
		return nil, errors.New("dispatcher identity changed")
	}
	factory.created = append(factory.created, config.Type)
	if err := factory.createErrs[config.Type]; err != nil {
		return nil, err
	}
	if handler := factory.handlers[config.Type]; handler != nil {
		return handler, nil
	}
	return http.NotFoundHandler(), nil
}

type workerFactory struct {
	logger     harness.Logger
	violations map[string]harness.Violations
	workers    map[string]harness.Worker
	created    []string
}

func workerFactories(factory harness.WorkerFactory, componentTypes ...string) harness.WorkerFactories {
	factories := make(harness.WorkerFactories, len(componentTypes))
	for _, componentType := range componentTypes {
		factories[componentType] = factory
	}

	return factories
}

func (factory *workerFactory) Validate(_ context.Context, config harness.WorkerConfig) harness.Violations {
	return factory.violations[config.Type]
}

func (factory *workerFactory) Create(_ context.Context, logger harness.Logger, config harness.WorkerConfig) (harness.Worker, error) {
	factory.logger = logger
	factory.created = append(factory.created, config.Type)
	if worker := factory.workers[config.Type]; worker != nil {
		return worker, nil
	}
	return harness.WorkerFunc(func(context.Context) error { return nil }), nil
}

func TestBuildAggregatesViolationsBeforeConstruction(t *testing.T) {
	loads := 0
	logger := &testLogger{}
	logFactory := &loggerFactory{logger: logger, violations: harness.Violations{{Path: "settings.format", Code: "invalid", Message: "bad format"}}}
	handlers := &handlerFactory{paths: map[string]string{"grpc": "/same", "http": "/same/"}, violations: map[string]harness.Violations{
		"grpc": {{Path: "settings.mode", Code: "invalid", Message: "bad mode"}},
		"http": {{Path: "settings.mode", Code: "invalid", Message: "bad mode"}},
	}}
	workers := &workerFactory{violations: map[string]harness.Violations{
		"job": {{Path: "settings.delay", Code: "invalid", Message: "bad delay"}},
	}}
	_, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			loads++
			return harness.Config{
				Main: harness.MainConfig{Port: 0}, Logger: harness.LoggerConfig{},
				Handlers: []harness.HandlerConfig{
					{Type: "grpc"},
					{Type: "http"},
					{},
				},
				Workers: []harness.WorkerConfig{{Type: "job"}, {}},
			}, nil
		}),
		LoggerFactory: logFactory, HandlerFactories: handlerFactories(handlers, "grpc", "http"),
		WorkerFactories: workerFactories(workers, "job"),
	})
	var validation *harness.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Build error = %T %v", err, err)
	}
	if loads != 1 || logFactory.created != 0 || len(handlers.created) != 0 || len(workers.created) != 0 {
		t.Fatalf("construction occurred: loads=%d logger=%d handlers=%v workers=%v", loads, logFactory.created, handlers.created, workers.created)
	}
	got := validation.Violations()
	wantPaths := []string{
		"main.port", "logger.type", "logger.settings.format", "handlers[0].settings.mode",
		"handlers[1].settings.mode", "handlers[1].settings.path", "handlers[2].type",
		"workers[0].settings.delay", "workers[1].type",
	}
	paths := make([]string, 0, len(got))
	for _, violation := range got {
		paths = append(paths, violation.Path)
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("violation paths = %#v, want %#v", paths, wantPaths)
	}
	got[0].Path = "mutated"
	if validation.Violations()[0].Path == "mutated" {
		t.Fatal("ValidationError exposed mutable violations")
	}
}

func TestBuildRejectsUnsupportedHandlerTypeBeforeConstruction(t *testing.T) {
	registered := &handlerFactory{}
	loggerFactory := &loggerFactory{logger: &testLogger{}}
	_, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main:     harness.MainConfig{Port: 8080},
				Logger:   harness.LoggerConfig{Type: "test"},
				Handlers: []harness.HandlerConfig{{Type: "missing"}},
			}, nil
		}),
		LoggerFactory:    loggerFactory,
		HandlerFactories: handlerFactories(registered, "registered"),
	})
	var validation *harness.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Build error = %T %v", err, err)
	}
	violations := validation.Violations()
	if len(violations) != 1 || violations[0].Path != "handlers[0].type" || violations[0].Code != "unsupported" {
		t.Fatalf("violations = %#v", violations)
	}
	if loggerFactory.created != 0 || len(registered.created) != 0 {
		t.Fatalf("construction occurred: logger=%d handlers=%v", loggerFactory.created, registered.created)
	}
}

func TestBuildSelectsHandlerFactoryByType(t *testing.T) {
	first := &handlerFactory{}
	second := &handlerFactory{}
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main:     harness.MainConfig{Port: 8080},
				Logger:   harness.LoggerConfig{Type: "test"},
				Handlers: []harness.HandlerConfig{{Type: "first"}, {Type: "second"}},
			}, nil
		}),
		LoggerFactory: &loggerFactory{logger: &testLogger{}},
		HandlerFactories: harness.HandlerFactories{
			"first":  first,
			"second": second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.created, []string{"first"}) || !reflect.DeepEqual(second.created, []string{"second"}) {
		t.Fatalf("created factories: first=%v second=%v", first.created, second.created)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSelectsWorkerFactoryByType(t *testing.T) {
	first := &workerFactory{}
	second := &workerFactory{}
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main:    harness.MainConfig{Port: 8080},
				Logger:  harness.LoggerConfig{Type: "test"},
				Workers: []harness.WorkerConfig{{Type: "first"}, {Type: "second"}},
			}, nil
		}),
		LoggerFactory:    &loggerFactory{logger: &testLogger{}},
		HandlerFactories: harness.HandlerFactories{},
		WorkerFactories: harness.WorkerFactories{
			"first":  first,
			"second": second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.created, []string{"first"}) || !reflect.DeepEqual(second.created, []string{"second"}) {
		t.Fatalf("created factories: first=%v second=%v", first.created, second.created)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRejectsUnsupportedWorkerTypeBeforeConstruction(t *testing.T) {
	loggerFactory := &loggerFactory{logger: &testLogger{}}
	_, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main:    harness.MainConfig{Port: 8080},
				Logger:  harness.LoggerConfig{Type: "test"},
				Workers: []harness.WorkerConfig{{Type: "missing"}},
			}, nil
		}),
		LoggerFactory:    loggerFactory,
		HandlerFactories: harness.HandlerFactories{},
	})
	var validation *harness.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Build error = %T %v", err, err)
	}
	violations := validation.Violations()
	if len(violations) != 1 || violations[0].Path != "workers[0].type" || violations[0].Code != "unsupported" {
		t.Fatalf("violations = %#v", violations)
	}
	if loggerFactory.created != 0 {
		t.Fatalf("logger construction count = %d", loggerFactory.created)
	}
}

func TestBuildSharesOneLoggerAndCloseOrdersCleanupBeforeLogger(t *testing.T) {
	logger := &testLogger{}
	logFactory := &loggerFactory{logger: logger}
	handlers := &handlerFactory{}
	workers := &workerFactory{}
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main: harness.MainConfig{Port: 8080}, Logger: harness.LoggerConfig{Type: "test"},
				Handlers: []harness.HandlerConfig{{Type: "api"}},
				Workers:  []harness.WorkerConfig{{Type: "job"}},
			}, nil
		}),
		LoggerFactory: logFactory, HandlerFactories: handlerFactories(handlers, "api"),
		WorkerFactories: workerFactories(workers, "job"),
		Cleanup: func(context.Context) error {
			logger.mu.Lock()
			defer logger.mu.Unlock()
			logger.events = append(logger.events, "cleanup")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if logFactory.created != 1 || handlers.logger != logger || workers.logger != logger {
		t.Fatal("factories did not share exactly one logger")
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Run(context.Background()); err == nil {
		t.Fatal("Run after Close error = nil")
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if !reflect.DeepEqual(logger.events, []string{"cleanup", "logger"}) || logger.closed != 1 {
		t.Fatalf("close events = %v, logger closes = %d", logger.events, logger.closed)
	}
}

func TestBuildConstructionFailureCleansUpAndClosesLoggerLast(t *testing.T) {
	logger := &testLogger{}
	cleanupCalls := 0
	_, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main: harness.MainConfig{Port: 8080}, Logger: harness.LoggerConfig{Type: "test"},
				Handlers: []harness.HandlerConfig{
					{Type: "first"},
					{Type: "second"},
				},
			}, nil
		}),
		LoggerFactory: &loggerFactory{logger: logger},
		HandlerFactories: handlerFactories(&handlerFactory{createErrs: map[string]error{
			"second": errors.New("constructor failed"),
		}}, "first", "second"),
		Cleanup: func(context.Context) error {
			cleanupCalls++
			logger.mu.Lock()
			defer logger.mu.Unlock()
			logger.events = append(logger.events, "cleanup")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "constructor failed") {
		t.Fatalf("Build error = %v", err)
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if cleanupCalls != 1 || !reflect.DeepEqual(logger.events, []string{"cleanup", "logger"}) {
		t.Fatalf("cleanup calls=%d events=%v", cleanupCalls, logger.events)
	}
}
