package runmill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	probeTimeout              = 5 * time.Second
	settingsValidationTimeout = 10 * time.Second
)

type providerClient struct {
	path        string
	prefix      []string
	info        ProviderInfo
	environment map[string]string
}

func (e *providerClient) validateSettings(ctx context.Context, request settingsParams) error {
	ctx, stop := context.WithTimeout(ctx, settingsValidationTimeout)
	defer stop()
	_, err := e.invoke(ctx, "validate_settings", request, MaxValidationReplyBytes, nil, nil)
	return err
}

func (e *providerClient) Execute(ctx context.Context, scope ExecutionContext, command string, arguments Arguments, secrets map[string]string) (any, error) {
	request, err := copyJSON(invocationParams{settingsParams: settingsParams{Settings: Settings(objectOrEmpty(scope.Settings))}, WorkspaceDir: scope.WorkspaceDir, ArtifactDir: scope.ArtifactDir, ExecutionID: scope.ExecutionID, Command: command, Arguments: Arguments(objectOrEmpty(arguments))})
	if err != nil {
		return nil, err
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	encoded, err := encodeSecrets(secrets)
	if err != nil {
		return nil, err
	}
	input := runParams{invocationParams: request, Secrets: encoded}
	data, err := e.invokeProcess(ctx, "run", &input, MaxExecutionReplyBytes, scope.Diagnostics, scope.Replies, scope.Process)
	if err != nil {
		return nil, err
	}
	var value any
	if err := decode(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (e *providerClient) invoke(ctx context.Context, method string, params any, limit int, live, replies io.Writer) ([]byte, error) {
	return e.invokeProcess(ctx, method, params, limit, live, replies, nil)
}

func (e *providerClient) invokeProcess(ctx context.Context, method string, params any, limit int, live, replies io.Writer, process *ProcessObserver) ([]byte, error) {
	if params == nil {
		params = struct{}{}
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, errors.New("invalid Provider request")
	}
	input, err := json.Marshal(requestEnvelope{ProtocolVersion: ProtocolVersion, Method: method, Params: encoded})
	if err != nil {
		return nil, err
	}
	if len(input) > MaxRequestBytes {
		return nil, errors.New("provider request exceeds size limit")
	}
	caller := ctx
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	output := &replyReader{limit: limit, stop: stop, live: replies}
	diagnostics := &diagnosticWriter{live: live}
	command := processCommand{Process: process, Program: e.path, Args: append([]string(nil), e.prefix...), Env: e.environment,
		Stdin: bytes.NewReader(input), Stdout: output, Stderr: diagnostics}
	processErr := runProcess(ctx, command)
	if err := caller.Err(); err != nil {
		return nil, err
	}
	if err := output.finish(); err != nil {
		if tail := strings.TrimSpace(string(diagnostics.tail)); tail != "" {
			return nil, fmt.Errorf("Provider protocol: %w: %s", err, tail)
		}
		return nil, fmt.Errorf("Provider protocol: %w", err)
	}
	if diagnostics.err != nil {
		return nil, errors.New("Provider tool output failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if output.final.Type == "error" {
		if processErr == nil {
			return nil, errors.New("Provider error reply conflicts with zero exit status")
		}
		var exit *processError
		if !errors.As(processErr, &exit) {
			return nil, fmt.Errorf("Provider process: %s", processErr.Error())
		}
		return nil, &ProviderError{Code: output.final.Code, Message: output.final.Message}
	}
	if processErr != nil {
		return nil, fmt.Errorf("Provider process: %s", processErr.Error())
	}
	if output.final.Data == nil {
		return []byte("null"), nil
	}
	return output.final.Data, nil
}

type diagnosticWriter struct {
	live io.Writer
	tail []byte
	err  error
}

func (output *diagnosticWriter) Write(data []byte) (int, error) {
	if output.err != nil {
		return 0, output.err
	}
	const tailLimit = 4096
	output.tail = append(output.tail, data...)
	if len(output.tail) > tailLimit {
		output.tail = bytes.Clone(output.tail[len(output.tail)-tailLimit:])
	}
	if output.live != nil {
		n, err := output.live.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		output.err = err
		return n, err
	}
	return len(data), nil
}

// loadExecutable resolves and probes one configured executable without host policy.
func loadExecutable(ctx context.Context, id string, config Executable, environ []string) (ProviderSnapshot, *providerClient) {
	// Resolve once so later cwd changes cannot select another executable.
	if strings.TrimSpace(config.Path) != "" {
		if absolute, err := filepath.Abs(config.Path); err == nil {
			config.Path = absolute
		}
	}
	e := &providerClient{path: config.Path, prefix: config.Args, environment: filteredEnvironment(environ)}
	health := ProviderSnapshot{ID: id, Executable: config, Availability: Unavailable}
	err := e.probe(ctx)
	health.CheckedAt = time.Now().UTC()
	if err != nil {
		health.Reason = err.Error()
		return health, nil
	}
	health.Availability, health.Info = Available, &e.info
	return health, e
}

func (e *providerClient) probe(ctx context.Context) error {
	stat, err := os.Stat(e.path)
	if strings.TrimSpace(e.path) == "" || err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0o111 == 0 {
		return errors.New("provider executable is missing or not executable")
	}
	for _, arg := range e.prefix {
		if strings.ContainsRune(arg, '\x00') {
			return errors.New("invalid Provider prefix argument")
		}
	}
	probe, stop := context.WithTimeout(ctx, probeTimeout)
	defer stop()
	data, err := e.invoke(probe, "info", nil, MaxValidationReplyBytes, nil, nil)
	if err != nil {
		return err
	}
	if err := decode(data, &e.info); err != nil {
		return err
	}
	return e.info.Validate()
}
