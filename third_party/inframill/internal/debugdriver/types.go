package debugdriver

import (
	"context"
	"github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
	"io"
)

type Violation struct {
	Code    string
	Path    string
	Message string
}
type InstanceValidation struct {
	Binding      driver.Binding
	Values       map[string]any
	DriverConfig driver.Config
	Path         string
}
type ExecutionContext struct {
	Stdout             io.Writer
	Stderr             io.Writer
	Artifacts          Artifacts
	WorkspaceDirectory string
	ArtifactDirectory  string
	CommandID          string
	Binding            driver.Binding
	Values             map[string]any
	DriverConfig       driver.Config
}
type Artifacts interface {
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, []byte) error
}
type Definition struct {
	Name     string
	Commands map[string]inframill.CommandDefinition
}
