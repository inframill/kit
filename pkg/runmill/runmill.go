// Package runmill hosts and serves executable commands independently of application policy.
package runmill

import (
	"context"
	"io"
)

// Provider supplies static settings validation and direct command execution.
// Implementations must honor cancellation and check prerequisites before effects.
type Provider interface {
	Definition() (ProviderDefinition, error)
	// ValidateSettings checks only this Provider's static settings object.
	ValidateSettings(context.Context, Settings) error
	Execute(context.Context, ExecutionContext, string, Arguments, map[string]string) (any, error)
}

// ProviderDefinition is implementation metadata; Serve supplies the protocol and release version.
type ProviderDefinition struct {
	Name     string
	Commands map[string]CommandDefinition
}

// Arguments is one command execution's JSON object, supplied by the caller.
type Arguments map[string]any

// ExecutionContext supplies one command's ID, paths, settings and output sinks.
// The caller owns Diagnostics; nil discards output. Structured inputs are copied by Host.
type ExecutionContext struct {
	// Process observes only the host's run subprocess, never validation/probes.
	// Callbacks execute synchronously and must not block indefinitely.
	Process     *ProcessObserver
	Diagnostics io.Writer
	// Replies receives validated JSON reply records at the executable host boundary.
	Replies io.Writer
	message func(string) error
	// WorkspaceDir is the host-provided workspace root for this Provider.
	// Providers choose the relative file paths beneath it.
	WorkspaceDir string
	ArtifactDir  string
	ExecutionID  string
	Settings     Settings
}

// ProcessObserver reports OS lifecycle independently of the Provider protocol.
// Started runs after spawn; an error aborts and reaps the process. Exited runs
// after Wait, with -1 for signal termination. Neither callback receives secrets.
type ProcessObserver struct {
	Started func(process Process) error
	Exited  func(exitCode int)
}

// Process is a live run subprocess. Signal accepts a canonical signal name on
// the host OS and targets the process group on Linux/macOS. After Wait it fails
// with os.ErrProcessDone; the diagnostic PID must never be used to signal later.
type Process interface {
	PID() int
	Signal(name string) error
}

// Execution describes the command, inputs and environment to execute through Host.
type Execution struct {
	ProviderID string
	Command    string
	Context    ExecutionContext
	Arguments  Arguments
}

// Message immediately emits a structured human reply through Serve. Outside Serve,
// an unset message sink discards messages, as with an unset Diagnostics writer.
func (s ExecutionContext) Message(text string) error {
	if s.message == nil {
		return nil
	}
	return s.message(text)
}

// Settings is one Provider's opaque JSON configuration object.
type Settings map[string]any
