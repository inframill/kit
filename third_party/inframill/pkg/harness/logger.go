package harness

import "context"

// Logger is the one structured logger shared by Hummus and its components.
type Logger interface {
	Debug(ctx context.Context, message string, fields ...Field)
	Info(ctx context.Context, message string, fields ...Field)
	Warn(ctx context.Context, message string, fields ...Field)
	Error(ctx context.Context, message string, fields ...Field)
	With(fields ...Field) Logger
	Close(ctx context.Context) error
}

// Field is one ordered structured logging field.
type Field struct {
	Key   string
	Value any
}
