package driver

import (
	"context"
	"errors"
	"io"
)

// ExecutionContext supplies one command's paths, binding, Values and output sinks.
// Callers own the sinks; all structured inputs are copied during preparation.
type ExecutionContext struct {
	Stdout             io.Writer
	Stderr             io.Writer
	WorkspaceDirectory string
	ArtifactDirectory  string
	CommandID          string
	Binding            Binding
	Values             map[string]any
	DriverConfig       Config
}

// CommandRequest selects a configured ID and an unqualified command name.
type CommandRequest struct {
	DriverID  string
	Command   string
	Scope     ExecutionContext
	Arguments map[string]any
}

// PreparedCommand has passed validation. Execute performs and verifies its effects.
type PreparedCommand interface {
	Execute(context.Context) (any, error)
}

type safeCommand struct{ command PreparedCommand }

func (s safeCommand) Execute(ctx context.Context) (value any, err error) {
	defer func() {
		if recover() != nil {
			value = nil
			err = errors.New("command implementation panicked")
		}
	}()
	return s.command.Execute(ctx)
}
