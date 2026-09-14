package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/pkg/driver/internal/process"
)

const (
	probeTimeout      = 5 * time.Second
	validationTimeout = 10 * time.Second
)

type executable struct {
	path        string
	prefix      []string
	info        Info
	secrets     map[string]process.Secret
	environment map[string]string
}

type secretValue string

func (s secretValue) Reveal() string { return string(s) }

func (e *executable) validate(ctx context.Context, request Request) error {
	ctx, stop := context.WithTimeout(ctx, validationTimeout)
	defer stop()
	request.Protocol = Protocol
	data, err := e.invoke(ctx, []string{"validate"}, &request, MaxInfo, nil, false)
	if err != nil {
		return err
	}
	var response struct {
		Valid bool `json:"valid"`
	}
	if err := decode(data, &response); err != nil {
		return err
	}
	if !response.Valid {
		return errors.New("driver validation did not succeed")
	}
	return nil
}

func (e *executable) Prepare(ctx context.Context, scope ExecutionContext, command string, arguments map[string]any) (PreparedCommand, error) {
	request, err := copyJSON(Request{Protocol: Protocol, WorkspaceDirectory: scope.WorkspaceDirectory, ArtifactDirectory: scope.ArtifactDirectory, Binding: &scope.Binding, CommandID: scope.CommandID, Values: scope.Values, Config: scope.DriverConfig, Command: command, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	if err := e.validate(ctx, request); err != nil {
		return nil, err
	}
	return prepared{driver: e, request: request, stdout: scope.Stdout, stderr: scope.Stderr}, nil
}

type prepared struct {
	driver  *executable
	request Request
	stdout  io.Writer
	stderr  io.Writer
}

func (p prepared) Execute(ctx context.Context) (any, error) {
	data, err := p.driver.invoke(ctx, []string{"run", p.request.Command}, &p.request, MaxResult, p.stderr, true)
	if err != nil {
		return nil, err
	}
	var value any
	if err := decode(data, &value); err != nil {
		return nil, err
	}
	value = p.driver.redactValue(value)
	// Publish only accepted JSON results. Tool output is already in stderr.
	if p.stdout != nil {
		if err := json.NewEncoder(p.stdout).Encode(value); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func (e *executable) redactValue(value any) any {
	switch typed := value.(type) {
	case string:
		return e.redactString(typed)
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for name, child := range typed {
			key := e.redactString(name)
			redacted[key] = e.redactValue(child)
		}
		return redacted
	case []any:
		for index, child := range typed {
			typed[index] = e.redactValue(child)
		}
	}
	return value
}

func (e *executable) redactString(value string) string {
	for _, secret := range e.secrets {
		if text := secret.Reveal(); text != "" {
			value = strings.ReplaceAll(value, text, "[REDACTED]")
		}
	}
	return value
}

func (e *executable) invoke(ctx context.Context, args []string, request *Request, limit int, live io.Writer, runtimeSecrets bool) ([]byte, error) {
	var input []byte
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		if len(data) > MaxRequest {
			return nil, errors.New("driver request exceeds size limit")
		}
		input = data
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	output := &limitedOutput{limit: limit, stop: stop}
	diagnostics := &diagnosticOutput{live: live}
	command := process.Command{Program: e.path, Args: append(append([]string(nil), e.prefix...), args...), Env: e.environment, Stdin: bytes.NewReader(input), Stdout: output, Stderr: diagnostics}
	if runtimeSecrets {
		command.SecretEnv = e.secrets
	}
	err := (process.Runner{}).Run(ctx, command)
	if output.exceeded {
		return nil, errors.New("driver output exceeds size limit")
	}
	if err != nil {
		message := e.redactString(strings.TrimSpace(string(diagnostics.tail)))
		if message != "" {
			return nil, fmt.Errorf("driver %s: %w: %s", filepath.Base(e.path), err, message)
		}
		return nil, fmt.Errorf("driver %s: %w", filepath.Base(e.path), err)
	}
	return output.Bytes(), nil
}

type limitedOutput struct {
	bytes.Buffer
	limit    int
	stop     context.CancelFunc
	exceeded bool
}

func (output *limitedOutput) Write(data []byte) (int, error) {
	if output.Len()+len(data) > output.limit {
		output.exceeded = true
		output.stop()
		return 0, errors.New("driver output exceeds size limit")
	}
	return output.Buffer.Write(data)
}

type diagnosticOutput struct {
	live io.Writer
	tail []byte
}

func (output *diagnosticOutput) Write(data []byte) (int, error) {
	const tailLimit = 4096
	output.tail = append(output.tail, data...)
	if len(output.tail) > tailLimit {
		output.tail = bytes.Clone(output.tail[len(output.tail)-tailLimit:])
	}
	if output.live != nil {
		return output.live.Write(data)
	}
	return len(data), nil
}
