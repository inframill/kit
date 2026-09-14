package inframill

import (
	"encoding/json"
	"io"
	"time"
)

type (
	Arguments     map[string]any
	CommandStatus string
)

const (
	Queued      CommandStatus = "queued"
	Running     CommandStatus = "running"
	Succeeded   CommandStatus = "succeeded"
	Failed      CommandStatus = "failed"
	Interrupted CommandStatus = "interrupted"
)

func Terminal(s CommandStatus) bool { return s == Succeeded || s == Failed || s == Interrupted }

type CommandRequest struct {
	ID             CommandID
	Command        string
	Args           Arguments
	IdempotencyKey string
}
type CommandError struct {
	Code    ErrorCode
	Message string
	Path    string
}
type CommandExecution struct {
	Request   CommandRequest
	Status    CommandStatus
	CreatedAt time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
	Result    json.RawMessage
	Error     *CommandError
}
type CommandExecutionQuery struct {
	Status CommandStatus
	Limit  int
	Cursor string
}
type CommandExecutionPage struct {
	Executions []CommandExecution
	NextCursor string
	ObservedAt time.Time
}
type (
	Clock       func() time.Time
	SafetyClass string
)

const (
	ReadOnly    SafetyClass = "read_only"
	Reversible  SafetyClass = "reversible"
	Mutating    SafetyClass = "mutating"
	Destructive SafetyClass = "destructive"
)

type CommandDefinition struct {
	Name            string
	Description     string
	ArgumentsSchema map[string]any
	Safety          SafetyClass
	Effects         []string
	Verification    string
}

func (e *Engine) Stdout(id InstanceID, command CommandID) (io.ReadCloser, error) {
	i, err := e.lookup(id)
	if err != nil {
		return nil, err
	}
	return i.Stdout(command)
}

func (e *Engine) Stderr(id InstanceID, command CommandID) (io.ReadCloser, error) {
	i, err := e.lookup(id)
	if err != nil {
		return nil, err
	}
	return i.Stderr(command)
}
