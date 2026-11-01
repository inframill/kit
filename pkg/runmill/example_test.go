package runmill_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/inframill/kit/pkg/runmill"
)

// echoProvider is a complete minimal implementation without application dependencies.
type echoProvider struct{}

func (echoProvider) Definition() (runmill.ProviderDefinition, error) {
	return runmill.ProviderDefinition{Name: "example", Commands: map[string]runmill.CommandDefinition{
		"echo": {Description: "Return a message", AccessMode: "read_write"},
	}}, nil
}

func (echoProvider) ValidateSettings(ctx context.Context, settings runmill.Settings) error {
	if len(settings) != 0 {
		return errors.New("settings must be empty")
	}
	return ctx.Err()
}

func (d echoProvider) Execute(ctx context.Context, scope runmill.ExecutionContext, name string, args runmill.Arguments, secrets map[string]string) (any, error) {
	if err := d.ValidateSettings(ctx, scope.Settings); err != nil {
		return nil, err
	}
	message, ok := args["message"].(string)
	if name != "echo" || !ok || len(args) != 1 {
		return nil, errors.New("echo requires a message string")
	}
	if scope.Diagnostics == nil {
		scope.Diagnostics = io.Discard
	}
	return (echoCommand{logs: scope.Diagnostics, message: message}).Execute(ctx, secrets)
}

type echoCommand struct {
	logs    io.Writer
	message string
}
type echoResult struct {
	Message string `json:"message"`
}

func (c echoCommand) Execute(ctx context.Context, _ map[string]string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintln(c.logs, "Returning the message"); err != nil {
		return nil, err
	}
	return echoResult{Message: c.message}, nil
}

// SPEC-0000 REQ-009: the README author example works with only the public package.
func ExampleServe() {
	input := `{"protocol":1,"method":"run","params":{"secrets":{},"settings":{},"command":"echo","arguments":{"message":"hello"},"workspaceDir":"/workspace","artifactDir":"/artifacts","executionId":"00000000-0000-4000-8000-000000000002"}}`
	var result, logs bytes.Buffer
	code := runmill.Serve(context.Background(), echoProvider{}, runmill.ServeOptions{
		Version: "0.1.0-dev", Stdin: strings.NewReader(input), Stdout: &result, Stderr: &logs,
	})
	fmt.Println(code)
	fmt.Print(logs.String())
	fmt.Print(result.String())
	// Output:
	// 0
	// Returning the message
	// {"type":"success","data":{"message":"hello"}}
}
