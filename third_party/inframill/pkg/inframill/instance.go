package inframill

import (
	"context"
	"io"
	"regexp"
	"strings"
	"time"
)

type (
	InstanceID     string
	CommandID      string
	InstanceValues map[string]any
	InstanceStatus string
)

const (
	InstanceIdle    InstanceStatus = "idle"
	InstanceRunning InstanceStatus = "running"
	InstanceFailed  InstanceStatus = "failed"
)

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func ValidUUID(value string) bool { return uuidPattern.MatchString(value) }
func ValidName(value string) bool { return namePattern.MatchString(value) }
func ValidInstanceName(value string) bool {
	project, name, ok := strings.Cut(value, "/")
	return ok && ValidName(project) && ValidName(name)
}

func ParseInstanceID(value string) (InstanceID, error) {
	if !ValidUUID(value) {
		return "", &Error{Code: InvalidInstanceID, Path: "instance_id", Message: "instance_id must be a UUID"}
	}
	return InstanceID(value), nil
}

type InstanceFailure struct {
	Code    ErrorCode
	Message string
}
type InstanceInfo struct {
	ID        InstanceID
	Name      string
	Values    InstanceValues
	Blueprint string
	Status    InstanceStatus
	Failure   *InstanceFailure
}
type InstanceConfig struct {
	Blueprint string
	Values    InstanceValues
}

// Instance owns its queue, persistence and worker. Submit success means durable
// admission. Close drains until cancellation and never deletes data.
type Instance interface {
	Info() InstanceInfo
	SubmitCommand(context.Context, CommandRequest) (CommandID, error)
	GetCommandExecution(context.Context, CommandID) (CommandExecution, error)
	ListCommandExecutions(context.Context, CommandExecutionQuery) (CommandExecutionPage, error)
	Stdout(CommandID) (io.ReadCloser, error)
	Stderr(CommandID) (io.ReadCloser, error)
	Close(context.Context) error
	// Deleted reports completed builtin.delete, independently of execution status.
	Deleted() bool
}
type InstanceQuery struct {
	Project string
	Limit   int
	Cursor  string
}
type InstancePage struct {
	Instances  []InstanceInfo
	NextCursor string
	ObservedAt time.Time
}
