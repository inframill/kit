package harness

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/pkg/harness/internal/dispatch"
)

const (
	violationCodeDuplicate   = "duplicate"
	violationCodeInvalid     = "invalid"
	violationCodeRequired    = "required"
	violationCodeUnsupported = "unsupported"
	requiredMessage          = "is required"
	typeRequiredMessage      = "type is required"
)

// Options supplies the application-owned ports used to build one App.
type Options struct {
	ConfigLoader     ConfigLoader
	LoggerFactory    LoggerFactory
	HandlerFactories HandlerFactories
	WorkerFactories  WorkerFactories
	Cleanup          func(context.Context) error
}

// Build loads, validates, constructs, and freezes one application.
func Build(ctx context.Context, options Options) (*App, error) {
	if violation := validateOptions(options); violation != nil {
		return nil, closeBuild(ctx, nil, options.Cleanup, NewValidationError(Violations{*violation}))
	}
	config, err := options.ConfigLoader.Load(ctx)
	if err != nil {
		return nil, closeBuild(ctx, nil, options.Cleanup, fmt.Errorf("load configuration: %w", err))
	}
	config.Main = withMainDefaults(config.Main)
	handlerPaths, violations := validateConfig(ctx, config, options)
	if len(violations) > 0 {
		return nil, closeBuild(ctx, nil, options.Cleanup, NewValidationError(violations))
	}

	logger, err := options.LoggerFactory.Create(ctx, config.Logger)
	if err != nil {
		return nil, closeBuild(ctx, nil, options.Cleanup, fmt.Errorf("create logger: %w", err))
	}
	if isNil(logger) {
		return nil, closeBuild(ctx, nil, options.Cleanup, errors.New("logger factory returned nil without an error"))
	}

	dispatcher := dispatch.New()
	routes := make([]dispatch.Route, 0, len(config.Handlers))
	for index, handlerConfig := range config.Handlers {
		factory := options.HandlerFactories[handlerConfig.Type]
		handler, createErr := factory.Create(ctx, logger, dispatcher, handlerConfig)
		if createErr != nil {
			return nil, closeBuild(ctx, logger, options.Cleanup,
				fmt.Errorf("create handler type %q at handlers[%d]: %w", handlerConfig.Type, index, createErr))
		}
		if isNil(handler) {
			return nil, closeBuild(ctx, logger, options.Cleanup,
				fmt.Errorf("handler factory returned nil for type %q without an error", handlerConfig.Type))
		}
		routes = append(routes, dispatch.Route{Path: handlerPaths[index], Handler: handler})
	}
	if !dispatcher.Seal(routes) {
		return nil, closeBuild(ctx, logger, options.Cleanup, errors.New("seal application dispatcher"))
	}

	workers := make([]configuredWorker, 0, len(config.Workers))
	for index, workerConfig := range config.Workers {
		factory := options.WorkerFactories[workerConfig.Type]
		worker, createErr := factory.Create(ctx, logger, workerConfig)
		if createErr != nil {
			return nil, closeBuild(ctx, logger, options.Cleanup,
				fmt.Errorf("create worker type %q at workers[%d]: %w", workerConfig.Type, index, createErr))
		}
		if isNil(worker) {
			return nil, closeBuild(ctx, logger, options.Cleanup,
				fmt.Errorf("worker factory returned nil for type %q without an error", workerConfig.Type))
		}
		workers = append(workers, configuredWorker{componentType: workerConfig.Type, worker: worker})
	}

	return newApp(config.Main, dispatcher, workers, logger, options.Cleanup), nil
}

func validateOptions(options Options) *Violation {
	for _, item := range []struct {
		path  string
		value any
	}{
		{path: "options.configLoader", value: options.ConfigLoader},
		{path: "options.loggerFactory", value: options.LoggerFactory},
		{path: "options.handlerFactories", value: options.HandlerFactories},
	} {
		if isNil(item.value) {
			return &Violation{Path: item.path, Code: violationCodeRequired, Message: requiredMessage}
		}
	}
	if violation := validateFactoryRegistry("options.handlerFactories", "handler", options.HandlerFactories); violation != nil {
		return violation
	}

	return validateFactoryRegistry("options.workerFactories", "worker", options.WorkerFactories)
}

func validateFactoryRegistry[T any](optionPath, component string, factories map[string]T) *Violation {
	types := make([]string, 0, len(factories))
	for componentType := range factories {
		types = append(types, componentType)
	}
	sort.Strings(types)
	for _, componentType := range types {
		if strings.TrimSpace(componentType) == "" {
			return &Violation{Path: optionPath, Code: violationCodeInvalid, Message: component + " factory type cannot be empty"}
		}
		if isNil(factories[componentType]) {
			return &Violation{
				Path: fmt.Sprintf("%s[%q]", optionPath, componentType), Code: violationCodeRequired, Message: requiredMessage,
			}
		}
	}

	return nil
}

func validateConfig(ctx context.Context, config Config, options Options) ([]string, Violations) {
	violations := validateMain(config.Main)
	if strings.TrimSpace(config.Logger.Type) == "" {
		violations = append(violations, Violation{Path: "logger.type", Code: violationCodeRequired, Message: typeRequiredMessage})
	}
	violations = append(violations, prefixViolations("logger", options.LoggerFactory.Validate(ctx, config.Logger))...)

	resolvedPaths := make([]string, len(config.Handlers))
	handlerPaths := make(map[string]int, len(config.Handlers))
	for index, handler := range config.Handlers {
		base := fmt.Sprintf("handlers[%d]", index)
		if strings.TrimSpace(handler.Type) == "" {
			violations = append(violations, Violation{Path: base + ".type", Code: violationCodeRequired, Message: typeRequiredMessage})
			continue
		}
		factory, ok := options.HandlerFactories[handler.Type]
		if !ok {
			violations = append(violations, Violation{Path: base + ".type", Code: violationCodeUnsupported, Message: "unsupported handler type"})
			continue
		}
		mountPath, current := factory.Validate(ctx, handler)
		resolvedPaths[index] = mountPath
		violations = append(violations, prefixViolations(base, current)...)
		if mountPath == "" && strings.TrimSpace(handler.Type) != "" && len(current) == 0 {
			violations = append(violations, Violation{Path: base + ".settings.path", Code: violationCodeRequired, Message: "handler factory must resolve a mount path"})
		} else if mountPath != "" {
			if !canonicalMountPath(mountPath) {
				violations = append(violations, Violation{Path: base + ".settings.path", Code: violationCodeInvalid, Message: "path must be a canonical absolute prefix"})
			} else if handlerPaths[mountKey(mountPath)] > 0 {
				violations = append(violations, Violation{Path: base + ".settings.path", Code: violationCodeDuplicate, Message: "path is duplicated"})
			} else {
				handlerPaths[mountKey(mountPath)]++
			}
		}
	}

	for index, worker := range config.Workers {
		base := fmt.Sprintf("workers[%d]", index)
		if strings.TrimSpace(worker.Type) == "" {
			violations = append(violations, Violation{Path: base + ".type", Code: violationCodeRequired, Message: typeRequiredMessage})
			continue
		}
		factory, ok := options.WorkerFactories[worker.Type]
		if !ok {
			violations = append(violations, Violation{Path: base + ".type", Code: violationCodeUnsupported, Message: "unsupported worker type"})
			continue
		}
		violations = append(violations, prefixViolations(base, factory.Validate(ctx, worker))...)
	}

	return resolvedPaths, violations
}

func validateMain(config MainConfig) Violations {
	var violations Violations
	if config.Port < 1 || config.Port > 65535 {
		violations = append(violations, Violation{Path: "main.port", Code: violationCodeInvalid, Message: "port must be between 1 and 65535"})
	}
	for _, duration := range []struct {
		path  string
		value time.Duration
	}{
		{path: "main.shutdownTimeout", value: config.ShutdownTimeout},
		{path: "main.readHeaderTimeout", value: config.ReadHeaderTimeout},
		{path: "main.readTimeout", value: config.ReadTimeout},
		{path: "main.writeTimeout", value: config.WriteTimeout},
		{path: "main.idleTimeout", value: config.IdleTimeout},
	} {
		if duration.value < 0 {
			violations = append(violations, Violation{Path: duration.path, Code: violationCodeInvalid, Message: "duration cannot be negative"})
		}
	}

	return violations
}

func canonicalMountPath(value string) bool {
	if value == "" || value[0] != '/' || strings.ContainsAny(value, "?#") || strings.Contains(value, "//") {
		return false
	}
	if value == "/" {
		return true
	}
	withoutTrailing := strings.TrimSuffix(value, "/")

	return path.Clean(withoutTrailing) == withoutTrailing
}

func mountKey(value string) string {
	if value == "/" {
		return value
	}

	return strings.TrimSuffix(value, "/")
}

func prefixViolations(prefix string, violations Violations) Violations {
	result := make(Violations, 0, len(violations))
	for _, violation := range violations {
		if violation.Path == "" {
			violation.Path = prefix
		} else if strings.HasPrefix(violation.Path, "[") {
			violation.Path = prefix + violation.Path
		} else {
			violation.Path = prefix + "." + violation.Path
		}
		result = append(result, violation)
	}

	return result
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() { //nolint:exhaustive // Only nil-able kinds require explicit handling.
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func closeBuild(ctx context.Context, logger Logger, cleanup func(context.Context) error, cause error) error {
	var cleanupErr, loggerErr error
	if cleanup != nil {
		cleanupErr = cleanup(ctx)
	}
	if logger != nil {
		loggerErr = logger.Close(ctx)
	}

	return errors.Join(cause, cleanupErr, loggerErr)
}
