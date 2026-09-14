// Package logger provides reusable structured logger implementations for Hummus applications.
package logger

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/sir-hassan/inframill/pkg/harness"
)

const (
	// TypeSlog selects the standard-library slog implementation.
	TypeSlog = "slog"
	// FormatJSON emits one JSON object per log record.
	FormatJSON = "json"
	// FormatText emits slog's key-value text format.
	FormatText = "text"

	violationInvalid     = "invalid"
	violationUnsupported = "unsupported"
)

// SlogSettings configures the standard-library slog implementation.
type SlogSettings struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// SlogFactory validates and creates standard-library slog loggers.
type SlogFactory struct {
	output     io.Writer
	baseFields []harness.Field
}

// NewSlogFactory creates a factory that writes to output and attaches baseFields
// to every record. The base field slice is copied.
func NewSlogFactory(output io.Writer, baseFields ...harness.Field) *SlogFactory {
	return &SlogFactory{
		output:     output,
		baseFields: append([]harness.Field(nil), baseFields...),
	}
}

// Validate reports configuration violations relative to the logger config.
func (*SlogFactory) Validate(_ context.Context, config harness.LoggerConfig) harness.Violations {
	if strings.TrimSpace(config.Type) == "" {
		return nil
	}
	if config.Type != TypeSlog {
		return harness.Violations{{
			Path: "type", Code: violationUnsupported, Message: "unsupported logger type",
		}}
	}

	settings, err := decodeSlogSettings(config.Settings)
	if err != nil {
		return harness.Violations{{Path: "settings", Code: violationInvalid, Message: err.Error()}}
	}

	var violations harness.Violations
	if _, err := parseLevel(settings.Level); err != nil {
		violations = append(violations, harness.Violation{
			Path: "settings.level", Code: violationInvalid, Message: "level is invalid",
		})
	}
	if settings.Format != FormatJSON && settings.Format != FormatText {
		violations = append(violations, harness.Violation{
			Path: "settings.format", Code: violationInvalid, Message: "format must be json or text",
		})
	}

	return violations
}

// Create constructs a logger from config. Hummus calls Validate before Create.
func (factory *SlogFactory) Create(_ context.Context, config harness.LoggerConfig) (harness.Logger, error) {
	if factory == nil || factory.output == nil {
		return nil, errors.New("log output is required")
	}
	if config.Type != TypeSlog {
		return nil, errors.New("unsupported logger type")
	}

	settings, err := decodeSlogSettings(config.Settings)
	if err != nil {
		return nil, err
	}
	level, err := parseLevel(settings.Level)
	if err != nil {
		return nil, err
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch settings.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(factory.output, options)
	case FormatText:
		handler = slog.NewTextHandler(factory.output, options)
	default:
		return nil, errors.New("log format must be json or text")
	}

	return &slogLogger{logger: slog.New(handler).With(slogFields(factory.baseFields)...)}, nil
}

func decodeSlogSettings(settings harness.Settings) (SlogSettings, error) {
	var decoded SlogSettings
	if err := settings.Decode(&decoded); err != nil {
		return SlogSettings{}, err
	}

	return decoded, nil
}

func parseLevel(value string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(value))); err != nil {
		return 0, err
	}

	return level, nil
}

type slogLogger struct{ logger *slog.Logger }

func (logger *slogLogger) Debug(ctx context.Context, message string, fields ...harness.Field) {
	logger.logger.DebugContext(ctx, message, slogFields(fields)...)
}

func (logger *slogLogger) Info(ctx context.Context, message string, fields ...harness.Field) {
	logger.logger.InfoContext(ctx, message, slogFields(fields)...)
}

func (logger *slogLogger) Warn(ctx context.Context, message string, fields ...harness.Field) {
	logger.logger.WarnContext(ctx, message, slogFields(fields)...)
}

func (logger *slogLogger) Error(ctx context.Context, message string, fields ...harness.Field) {
	logger.logger.ErrorContext(ctx, message, slogFields(fields)...)
}

func (logger *slogLogger) With(fields ...harness.Field) harness.Logger {
	return &slogLogger{logger: logger.logger.With(slogFields(fields)...)}
}

func (*slogLogger) Close(context.Context) error { return nil }

func slogFields(fields []harness.Field) []any {
	result := make([]any, 0, len(fields)*2)
	for _, field := range fields {
		result = append(result, field.Key, field.Value)
	}

	return result
}
