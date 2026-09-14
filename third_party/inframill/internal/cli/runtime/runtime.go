// Package runtime owns CLI policy around generated typed Connect calls.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	cliconfig "github.com/sir-hassan/inframill/internal/cli/config"
	clioutput "github.com/sir-hassan/inframill/internal/cli/output"
	remoteoutput "github.com/sir-hassan/inframill/internal/cli/output/remote"
	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

type InputError struct{ Message string }

func (e InputError) Error() string { return e.Message }

type Runtime struct {
	Environment    []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Client         func(string) inframillv1connect.EngineServiceClient
	Terminal       func(io.Writer) bool
	Color          *string
}

type Settings struct {
	Endpoint      string
	Timeout       time.Duration
	Output, Color string
}

func Env(environ []string, key string) (string, bool) {
	var result string
	found := false
	for _, item := range environ {
		if k, v, ok := strings.Cut(item, "="); ok && k == key {
			result, found = v, true
		}
	}
	return result, found
}

func option(cmd *cobra.Command, key string, setting *string, environ []string, envKey, fallback string) string {
	if f := cmd.Flags().Lookup(key); f != nil && f.Changed {
		return f.Value.String()
	}
	if envKey != "" {
		if value, exists := Env(environ, envKey); exists {
			return value
		}
	}
	if setting != nil {
		return *setting
	}
	return fallback
}

func Resolve(cmd *cobra.Command, environ []string, remote, logs bool) (Settings, error) {
	path := flagValue(cmd, "config")
	preferences, err := cliconfig.Load(path, cmd.Flags().Changed("config"))
	if err != nil {
		return Settings{}, InputError{err.Error()}
	}
	settings := Settings{Output: option(cmd, "output", preferences.Output, nil, "", clioutput.Table), Color: option(cmd, "color", preferences.Color, nil, "", "auto")}
	if logs && !cmd.Flags().Changed("output") {
		settings.Output = ""
	}
	if settings.Output == "" && (!logs || cmd.Flags().Changed("output")) {
		return settings, InputError{"output must be table or json"}
	}
	if validationErr := clioutput.Validate(settings.Output, logs); validationErr != nil {
		return settings, InputError{validationErr.Error()}
	}
	if settings.Color != "auto" && settings.Color != "always" && settings.Color != "never" {
		return settings, InputError{"color must be auto, always or never"}
	}
	if !remote {
		return settings, nil
	}
	settings.Endpoint = option(cmd, "endpoint", preferences.Endpoint, environ, "INFRAMILL_ENDPOINT", "http://127.0.0.1:8080")
	endpoint, err := url.Parse(settings.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return settings, InputError{"endpoint must be an HTTP(S) base URL without credentials, query or fragment"}
	}
	settings.Timeout, err = time.ParseDuration(option(cmd, "timeout", preferences.Timeout, environ, "INFRAMILL_TIMEOUT", "2m"))
	if err != nil || settings.Timeout <= 0 {
		return settings, InputError{"timeout must be positive"}
	}
	return settings, nil
}

func DefaultClient(endpoint string) inframillv1connect.EngineServiceClient {
	return inframillv1connect.NewEngineServiceClient(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint)
}

func ExactArgs(n int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return InputError{"incorrect command arguments"}
		}
		return nil
	}
}

func (r *Runtime) Add(root *cobra.Command, group string, cmd *cobra.Command) {
	parent := root
	for _, name := range strings.Fields(group) {
		var child *cobra.Command
		for _, existing := range parent.Commands() {
			if existing.Name() == name {
				child = existing
				break
			}
		}
		if child == nil {
			child = &cobra.Command{Use: name, Short: "Manage " + name + " resources", Args: ExactArgs(0), RunE: func(*cobra.Command, []string) error { return InputError{"a subcommand is required"} }}
			if parent == root {
				child.GroupID = "remote"
				child.PersistentFlags().String("endpoint", "http://127.0.0.1:8080", "daemon HTTP(S) base URL")
				child.PersistentFlags().String("timeout", "2m", "total invocation deadline")
				child.PersistentFlags().String("config", ".inframill.yaml", "optional CLI defaults YAML; explicit files are required")
				child.PersistentFlags().StringP("output", "o", "table", "output format: table or json (logs: json only)")
				Complete(child, "output", "table", "json")
			}
			parent.AddCommand(child)
		}
		parent = child
	}
	CompleteFiles(cmd)
	parent.AddCommand(cmd)
}

// Enum binds generated numeric fields with static completion and safe errors.
func Enum(flags *pflag.FlagSet, name, help string, labels map[string]int32, set func(int32)) {
	flags.Var(&enumValue{labels: labels, set: set}, name, help)
}

type enumValue struct {
	labels map[string]int32
	set    func(int32)
	value  string
}

func (e *enumValue) String() string { return e.value }
func (*enumValue) Type() string     { return "enum" }
func (e *enumValue) Set(value string) error {
	// An explicitly empty optional filter preserves the existing unspecified value.
	if value == "" {
		e.set(0)
		e.value = ""
		return nil
	}
	number, exists := e.labels[strings.ToLower(value)]
	if !exists {
		return errors.New("invalid enum value")
	}
	e.set(number)
	e.value = value
	return nil
}

func Configure(cmd *cobra.Command, request proto.Message) {
	cmd.Args = ExactArgs(0)
	switch req := request.(type) {
	case *pb.FetchBlueprintRequest:
		cmd.Use += " REFERENCE"
		cmd.Args = positions(1, func(args []string) { req.Reference = args[0] })
	case *pb.InitInstanceRequest:
		cmd.Use += " NAME"
		cmd.Args = positions(1, func(args []string) { req.Name = args[0] })
	case *pb.GetInstanceRequest:
		cmd.Use += " INSTANCE_ID"
		cmd.Args = positions(1, func(args []string) { req.InstanceId = args[0] })
	case *pb.SubmitCommandRequest:
		cmd.Flags().Bool("wait", false, "wait without cancelling admitted daemon work")
		if cmd.Name() == "delete" {
			cmd.Use += " INSTANCE_ID"
			cmd.Args = positions(1, func(args []string) { req.InstanceId = args[0]; req.Command = DeleteCommand })
		} else {
			req.Args = &structpb.Struct{}
			cmd.Use += " INSTANCE_ID COMMAND"
			cmd.Args = positions(2, func(args []string) { req.InstanceId = args[0]; req.Command = args[1] })
		}
	case *pb.GetCommandExecutionRequest:
		cmd.Use += " INSTANCE_ID COMMAND_ID"
		cmd.Args = positions(2, func(args []string) { req.InstanceId = args[0]; req.CommandId = args[1] })
	case *pb.ReadCommandLogRequest:
		req.Limit = 64 << 10
		if cmd.Name() == "stdout" {
			req.Stream = pb.LogStream_LOG_STREAM_STDOUT
		} else {
			req.Stream = pb.LogStream_LOG_STREAM_STDERR
		}
		cmd.Use += " INSTANCE_ID COMMAND_ID"
		cmd.Args = positions(2, func(args []string) { req.InstanceId = args[0]; req.CommandId = args[1] })
	case *pb.ListInstancesRequest:
		req.Limit = 50
	case *pb.ListCommandExecutionsRequest:
		req.Limit = 50
	case *pb.ListBlueprintsRequest, *pb.ListCommandDefinitionsRequest:
		if cmd.Name() == "show" {
			cmd.Use += " NAME"
			cmd.Args = ExactArgs(1)
		}
	}
}

func positions(n int, set func([]string)) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := ExactArgs(n)(cmd, args); err != nil {
			return err
		}
		set(args)
		return nil
	}
}

func validateRequest(request proto.Message) error {
	switch req := request.(type) {
	case *pb.InitInstanceRequest:
		if req.GetInstance() == nil {
			return InputError{"init requires --file FILE"}
		}
	case *pb.ListInstancesRequest:
		if req.GetLimit() < 1 || req.GetLimit() > 100 {
			return InputError{"limit must be between 1 and 100"}
		}
	case *pb.ListCommandExecutionsRequest:
		if req.GetInstanceId() == "" {
			return InputError{"command execution list requires --instance-id"}
		}
		if req.GetLimit() < 1 || req.GetLimit() > 100 {
			return InputError{"limit must be between 1 and 100"}
		}
	case *pb.ReadCommandLogRequest:
		if req.GetOffset() < 0 || req.GetLimit() < 1 || req.GetLimit() > 1<<20 {
			return InputError{"invalid log offset or limit"}
		}
	}
	return nil
}

// Execute is shared policy; the generated closure performs the direct typed call.
func (r *Runtime) Execute(cmd *cobra.Command, args []string, request, response proto.Message, read func() error, call func(context.Context, inframillv1connect.EngineServiceClient) (proto.Message, error)) error {
	if validationErr := remoteoutput.ValidateDefinitions(); validationErr != nil {
		return InputError{validationErr.Error()}
	}
	_, logs := request.(*pb.ReadCommandLogRequest)
	settings, err := Resolve(cmd, r.Environment, true, logs)
	if err != nil {
		return err
	}
	if r.Color != nil {
		*r.Color = settings.Color
	}
	columns := flagValue(cmd, "columns")
	selected := cmd.Flags().Changed("columns")
	if selected && settings.Output == clioutput.JSON {
		return InputError{"columns require table output"}
	}
	if validationErr := remoteoutput.Validate(response, columns, selected); validationErr != nil {
		return InputError{validationErr.Error()}
	}
	if readErr := read(); readErr != nil {
		return readErr
	}
	if validationErr := validateRequest(request); validationErr != nil {
		return validationErr
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), settings.Timeout)
	defer cancel()
	client := r.Client(settings.Endpoint)
	var result proto.Message
	if req, ok := request.(*pb.GetCommandExecutionRequest); ok && cmd.Name() == "wait" {
		result, err = WaitForCommand(ctx, client, req.GetInstanceId(), req.GetCommandId(), false)
	} else {
		result, err = call(ctx, client)
	}
	if err != nil && !errors.Is(err, ErrUnsuccessful) {
		return err
	}
	if req, ok := request.(*pb.SubmitCommandRequest); ok {
		wait := flagValue(cmd, "wait") == "true"
		if wait {
			submission, ok := result.(*pb.SubmitCommandResponse)
			if !ok {
				return errors.New("invalid submission response")
			}
			result, err = WaitForCommand(ctx, client, req.GetInstanceId(), submission.GetCommandId(), req.GetCommand() == DeleteCommand)
		}
	}
	if err != nil && !errors.Is(err, ErrUnsuccessful) {
		return err
	}
	if cmd.Name() == "show" {
		result, err = selectCatalog(result, args[0])
		if err != nil {
			return err
		}
	}
	if logs && settings.Output != clioutput.JSON {
		log, ok := result.(*pb.ReadCommandLogResponse)
		if !ok {
			return errors.New("invalid log response")
		}
		n, writeErr := r.Stdout.Write(log.GetData())
		if writeErr == nil && n != len(log.GetData()) {
			return io.ErrShortWrite
		}
		return writeErr
	}
	writer := r.Stdout
	if settings.Output != clioutput.JSON {
		writer = ColorWriter(writer, settings.Color, r.Environment, r.Terminal)
	}
	if writeErr := remoteoutput.WriteColumns(writer, settings.Output, result, columns, selected); writeErr != nil {
		return writeErr
	}
	return err
}

func selectCatalog(result proto.Message, name string) (proto.Message, error) {
	switch catalog := result.(type) {
	case *pb.ListBlueprintsResponse:
		for _, item := range catalog.GetBlueprints() {
			if item.GetReference() == name {
				return item, nil
			}
		}
	case *pb.ListCommandDefinitionsResponse:
		for _, item := range catalog.GetCommands() {
			if item.GetName() == name {
				return item, nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("catalog item not found"))
}

func ColorWriter(writer io.Writer, mode string, environ []string, terminal func(io.Writer) bool) io.Writer {
	noColor, _ := Env(environ, "NO_COLOR")
	term, _ := Env(environ, "TERM")
	if mode == "always" || (mode == "auto" && noColor == "" && term != "dumb" && terminal != nil && terminal(writer)) {
		return colorWriter{writer}
	}
	return writer
}

type colorWriter struct{ writer io.Writer }

func (w colorWriter) Write(data []byte) (int, error) {
	decorated := append([]byte("\x1b[36m"), data...)
	decorated = append(decorated, []byte("\x1b[0m")...)
	n, err := w.writer.Write(decorated)
	if err == nil && n != len(decorated) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// Diagnostic never prints untrusted Connect payloads or parser values.
func Diagnostic(err error) string {
	if connectError, ok := errors.AsType[*connect.Error](err); ok {
		return "daemon request failed: " + connectError.Code().String()
	}
	return fmt.Sprint(err)
}

func flagValue(cmd *cobra.Command, name string) string {
	if flag := cmd.Flags().Lookup(name); flag != nil {
		return flag.Value.String()
	}
	return ""
}
