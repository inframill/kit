package debugdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
)

// Serve implements the three-command protocol for first-party executables.
// Third-party Drivers can implement the documented JSON contract in any language.
func Serve(ctx context.Context, implementation Driver, version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	result, err := dispatch(ctx, implementation, version, args, stdin, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err) //nolint:errcheck // Best-effort diagnostic, failure is returned via exit status.
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return 1
	}
	return 0
}

func dispatch(ctx context.Context, implementation Driver, version string, args []string, stdin io.Reader, stderr io.Writer) (any, error) {
	if len(args) == 1 && args[0] == "info" {
		definition, err := implementation.Definition()
		if err != nil {
			return nil, err
		}
		info := drivers.Info{Name: definition.Name, Version: version, Protocol: drivers.Protocol, Commands: make(map[string]drivers.CommandDefinition, len(definition.Commands))}
		for name, command := range definition.Commands {
			if command.Description == "" {
				command.Description = strings.Join(command.Effects, "; ")
			}
			info.Commands[name] = drivers.CommandDefinition{Description: command.Description, ArgumentsSchema: command.ArgumentsSchema, Safety: drivers.SafetyClass(command.Safety), Effects: command.Effects, Verification: command.Verification}
		}
		if err := info.Validate(); err != nil {
			return nil, err
		}
		return info, nil
	}
	validate := len(args) == 1 && args[0] == "validate"
	run := len(args) == 2 && args[0] == "run"
	if !validate && !run {
		return nil, errors.New("usage: DRIVER info | validate | run COMMAND")
	}
	data, err := io.ReadAll(io.LimitReader(stdin, drivers.MaxRequest+1))
	if err != nil {
		return nil, err
	}
	if len(data) > drivers.MaxRequest {
		return nil, errors.New("driver request exceeds size limit")
	}
	var request drivers.Request
	if err = drivers.DecodeRequest(data, &request); err != nil {
		return nil, err
	}
	if request.Protocol != drivers.Protocol {
		return nil, errors.New("unsupported Driver request protocol")
	}
	if err = implementation.ValidateConfig(request.Config); err != nil {
		return nil, err
	}
	if request.BlueprintDirectory != "" {
		if err = implementation.ValidateBlueprint(request.BlueprintDirectory, request.Config); err != nil {
			return nil, err
		}
	}
	if request.Binding != nil {
		violations := implementation.ValidateInstance(InstanceValidation{Binding: *request.Binding, Values: request.Values, DriverConfig: request.Config, Path: "values"})
		if len(violations) != 0 {
			return nil, errors.New(violations[0].Message)
		}
	}
	if validate && request.Command == "" {
		return map[string]bool{"valid": true}, nil
	}
	if request.Binding == nil || request.WorkspaceDirectory == "" || request.Command == "" {
		return nil, errors.New("command requires binding, workspaceDirectory and command")
	}
	if run && args[1] != request.Command {
		return nil, errors.New("command argument does not match request")
	}
	prepared, err := implementation.Prepare(ctx, ExecutionContext{
		Stdout: stderr, Stderr: stderr, Artifacts: directoryArtifacts{directory: request.ArtifactDirectory},
		WorkspaceDirectory: request.WorkspaceDirectory, ArtifactDirectory: request.ArtifactDirectory,
		Binding: *request.Binding, CommandID: request.CommandID, Values: request.Values, DriverConfig: request.Config,
	}, request.Command, request.Arguments)
	if err != nil {
		return nil, err
	}
	if validate {
		return map[string]bool{"valid": true}, nil
	}
	return prepared.Execute(ctx)
}
