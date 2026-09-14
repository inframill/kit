// Package localexec composes persistent local command execution for the CLI.
package localexec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/sir-hassan/inframill/internal/appconfig"
	clioutput "github.com/sir-hassan/inframill/internal/cli/output"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	execMode      = "exec"
	historyMode   = "history"
	resultVersion = "inframill.dev/local-result/v1"
)

type options struct {
	drivers                                                    drivers.Configurations
	blueprint, instance, workspace, name, argsFile, resultFile string
	config                                                     string
	command, commandID, cursor, output                         string
	limit                                                      int
	envFile                                                    string
	envFileSet                                                 bool
	blueprintSet                                               bool
}

// ExitError carries the stable local CLI exit code without parsing diagnostics.
type ExitError struct {
	Code  int
	Cause error
}

func (e *ExitError) Error() string { return e.Cause.Error() }
func (e *ExitError) Unwrap() error { return e.Cause }

func invalid(message string) error { return &ExitError{Code: 2, Cause: errors.New(message)} }

// ExecOptions preserves explicit flag presence without another argument parser.
type ExecOptions struct {
	Config, Blueprint, Instance, Name, ArgsFile, EnvFile, ResultFile, Command string
	BlueprintSet, EnvFileSet                                                  bool
}

type HistoryOptions struct {
	Workspace, Name, CommandID, Cursor, Output string
	Limit                                      int
}

func Exec(ctx context.Context, input ExecOptions, environ []string, stdout, stderr io.Writer) error {
	return run(ctx, execMode, options{
		config: input.Config, blueprint: input.Blueprint,
		instance: input.Instance, name: input.Name, argsFile: input.ArgsFile, envFile: input.EnvFile,
		resultFile: input.ResultFile, command: input.Command, blueprintSet: input.BlueprintSet,
		envFileSet: input.EnvFileSet,
	}, environ, stdout, stderr)
}

func History(ctx context.Context, input HistoryOptions, stdout, stderr io.Writer) error {
	if err := clioutput.Validate(input.Output, false); err != nil {
		return invalid(err.Error())
	}
	return run(ctx, historyMode, options{
		workspace: input.Workspace, name: input.Name,
		commandID: input.CommandID, cursor: input.Cursor, output: input.Output, limit: input.Limit,
	}, nil, stdout, stderr)
}

func readObject(path string) (map[string]any, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalid("input must be a regular JSON file")
	}
	//nolint:gosec // Explicit local input path, checked to be a regular file.
	f, err := os.Open(path)
	if err != nil {
		return nil, invalid("cannot open input JSON file")
	}
	defer f.Close() //nolint:errcheck // Read-only input.
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, invalid("cannot read input JSON within 1 MiB limit")
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, invalid("input JSON must be an object")
	}
	return object, nil
}

func exitCode(ctx context.Context, err error) int {
	if err == nil {
		return 0
	}
	if failure, ok := errors.AsType[*ExitError](err); ok {
		return failure.Code
	}
	if ctx.Err() != nil {
		return 130
	}
	var domain *inframill.Error
	if errors.As(err, &domain) && (domain.Code == inframill.InvalidRequest || domain.Code == inframill.InvalidConfig || domain.Code == inframill.InvalidInstanceID) {
		return 2
	}
	return 3
}

func run(ctx context.Context, mode string, o options, environ []string, stdout, stderr io.Writer) error {
	var input execInput
	var inputErr error
	if mode == execMode {
		config, configErr := appconfig.Load(ctx, o.config, environ)
		if configErr != nil {
			return invalid(configErr.Error())
		}
		o.workspace = config.DataDir
		o.drivers = config.Drivers
		input, inputErr = selectInput(&o, environ)
	}
	file, err := reserveResult(o)
	if err != nil {
		return &ExitError{Code: 3, Cause: err}
	}
	r := result{APIVersion: resultVersion, Status: "rejected"}
	switch {
	case inputErr != nil:
		err = inputErr
	case o.workspace == "":
		err = invalid("history requires --workspace")
	default:
		r, err = runWorkspace(ctx, mode, o, input, environ, stdout, stderr)
	}
	code := exitCode(ctx, err)
	if r.APIVersion == "" {
		r.APIVersion = resultVersion
	}
	if r.Status == "" {
		r.Status = "rejected"
	}
	if err != nil && r.Error == nil {
		r.Error = &resultError{Code: "LOCAL_ERROR", Message: err.Error()}
	}
	if publishErr := file.publish(r); publishErr != nil {
		return &ExitError{Code: 3, Cause: errors.New("cannot publish structured result file; inspect workspace history")}
	}
	if err != nil {
		return &ExitError{Code: code, Cause: err}
	}
	return nil
}
