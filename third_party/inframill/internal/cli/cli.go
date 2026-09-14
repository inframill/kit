// Package cli composes one native and generated Cobra command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	generated "github.com/sir-hassan/inframill/internal/cli/gen"
	"github.com/sir-hassan/inframill/internal/cli/localexec"
	cliruntime "github.com/sir-hassan/inframill/internal/cli/runtime"
	"github.com/sir-hassan/inframill/internal/debugdriver"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	"github.com/sir-hassan/inframill/internal/server"
)

type Dependencies struct {
	Environment           []string
	Stdin                 io.Reader
	Stdout, Stderr        io.Writer
	Version, DebugVersion string
	Client                func(string) inframillv1connect.EngineServiceClient
	Terminal              func(io.Writer) bool
}

type trackedWriter struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (w *trackedWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.mu.Lock()
		w.err = err
		w.mu.Unlock()
	}
	return n, err
}

func Run(ctx context.Context, args []string, d Dependencies) int {
	if d.Stdin == nil {
		d.Stdin = strings.NewReader("")
	}
	if d.Stdout == nil {
		d.Stdout = io.Discard
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if d.Version == "" {
		d.Version = "0.0.0-dev"
	}
	if d.DebugVersion == "" {
		d.DebugVersion = "0.0.0-dev"
	}
	if len(args) >= 2 && args[0] == "driver" && args[1] == "debug" {
		return debugdriver.Serve(ctx, debugdriver.Driver{}, d.DebugVersion, args[2:], d.Stdin, d.Stdout, d.Stderr)
	}
	if d.Client == nil {
		d.Client = cliruntime.DefaultClient
	}
	detect := d.Terminal
	if detect == nil {
		detect = func(w io.Writer) bool { f, ok := w.(*os.File); return ok && term.IsTerminal(int(f.Fd())) }
	}
	stdout, stderr := &trackedWriter{writer: d.Stdout}, &trackedWriter{writer: d.Stderr}
	d.Stdout, d.Stderr = stdout, stderr
	d.Terminal = func(w io.Writer) bool {
		if tracked, ok := w.(*trackedWriter); ok {
			return detect(tracked.writer)
		}
		return detect(w)
	}
	root, color := newRoot(d)
	root.SetArgs(args)
	root.SetContext(ctx)
	var err error
	if len(args) == 0 {
		root.SetOut(stderr)
		err = root.Help()
		if err == nil {
			err = cliruntime.InputError{Message: "a command is required"}
		}
	} else if _, _, findErr := root.Find(args); findErr != nil {
		err = cliruntime.InputError{Message: "unknown command"}
	} else {
		err = root.Execute()
	}
	if stdout.failure() != nil || stderr.failure() != nil {
		err = errors.New("cannot write CLI output")
	}
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(cliruntime.ColorWriter(stderr, *color, d.Environment, d.Terminal), "inframill:", cliruntime.Diagnostic(err)) //nolint:errcheck // Best-effort diagnostic after failure.
	var invalid cliruntime.InputError
	var local *localexec.ExitError
	switch {
	case stdout.failure() != nil || stderr.failure() != nil:
		return 3
	case errors.As(err, &local):
		return local.Code
	case errors.As(err, &invalid):
		return 2
	case ctx.Err() != nil:
		return 130
	case errors.Is(err, cliruntime.ErrUnsuccessful):
		return 1
	default:
		return 3
	}
}

func newRoot(d Dependencies) (*cobra.Command, *string) {
	color := "auto"
	root := &cobra.Command{Use: "inframill", Short: "Manage named Instances through external Drivers", Version: d.Version, SilenceUsage: true, SilenceErrors: true, Args: cliruntime.ExactArgs(0), RunE: func(*cobra.Command, []string) error { return cliruntime.InputError{Message: "a command is required"} }}
	root.SetIn(d.Stdin)
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	cliruntime.Choices(root, root.PersistentFlags(), "color", "auto", "human output color: auto, always or never", &color, "auto", "always", "never")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, _ error) error {
		if cmd.Name() == "exec" || cmd.Name() == "history" {
			return cliruntime.InputError{Message: "unknown local flag or invalid local flags"}
		}
		return cliruntime.InputError{Message: "unknown flag or invalid flag value"}
	})
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if color != "auto" && color != "always" && color != "never" {
			return cliruntime.InputError{Message: "color must be auto, always or never"}
		}
		return nil
	}
	help := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		cmd.SetOut(cliruntime.ColorWriter(out, color, d.Environment, d.Terminal))
		defer cmd.SetOut(out)
		help(cmd, args)
	})
	root.AddGroup(&cobra.Group{ID: nativeGroup, Title: "Local commands:"}, &cobra.Group{ID: "remote", Title: "Daemon commands:"}, &cobra.Group{ID: utilityGroup, Title: "Utilities:"})
	root.SetHelpCommand(&cobra.Command{Use: "help [command]", Short: "Help about any command", GroupID: utilityGroup, RunE: func(_ *cobra.Command, args []string) error {
		target, remaining, findErr := root.Find(args)
		if findErr != nil || len(remaining) > 0 {
			return cliruntime.InputError{Message: "unknown help command"}
		}
		return target.Help()
	}})
	root.SetHelpCommandGroupID(utilityGroup)
	root.SetCompletionCommandGroupID(utilityGroup)
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the core release version", GroupID: utilityGroup, Args: cliruntime.ExactArgs(0), RunE: func(*cobra.Command, []string) error { _, err := fmt.Fprintln(d.Stdout, d.Version); return err }})
	addNative(root, d, &color)
	runtime := &cliruntime.Runtime{Environment: d.Environment, Stdin: d.Stdin, Stdout: d.Stdout, Stderr: d.Stderr, Client: d.Client, Terminal: d.Terminal, Color: &color}
	generated.Register(root, runtime)
	return root, &color
}

func addNative(root *cobra.Command, d Dependencies, color *string) {
	execOptions := localexec.ExecOptions{Config: ".inframill.yaml", Name: "local/default"}
	execute := &cobra.Command{Use: "exec COMMAND", Short: "Execute locally and stream the verified result", GroupID: nativeGroup, Args: cliruntime.ExactArgs(1), Example: "  inframill exec debug.echo --instance examples/debug.json --blueprint-dir blueprints/debug", RunE: func(cmd *cobra.Command, args []string) error {
		execOptions.Command = args[0]
		execOptions.BlueprintSet = cmd.Flags().Changed("blueprint-dir")
		execOptions.EnvFileSet = cmd.Flags().Changed("env-file")
		return localexec.Exec(cmd.Context(), execOptions, d.Environment, d.Stdout, d.Stderr)
	}}
	f := execute.Flags()
	f.StringVar(&execOptions.Config, "config", execOptions.Config, "required application YAML")
	f.StringVar(&execOptions.Name, "name", execOptions.Name, "immutable project/name")
	f.StringVar(&execOptions.Instance, "instance", "", "Instance JSON file (blueprint and values)")
	f.StringVar(&execOptions.Blueprint, "blueprint-dir", "", "local Blueprint source override")
	f.StringVar(&execOptions.ArgsFile, "args-file", "", "command Args JSON file")
	f.StringVar(&execOptions.EnvFile, "env-file", "", "explicit dotenv secret assignments")
	f.StringVar(&execOptions.ResultFile, "result-file", "", "new structured result export file")
	cliruntime.CompleteFiles(execute)
	root.AddCommand(execute)
	historyOptions := localexec.HistoryOptions{Name: "local/default", Limit: 50}
	history := &cobra.Command{Use: "history", Short: "Read existing local Instance command history", GroupID: nativeGroup, Args: cliruntime.ExactArgs(0), Example: "  inframill history --workspace .inframill --name examples/debug", RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := cliruntime.Resolve(cmd, nil, false, false)
		if err != nil {
			return err
		}
		historyOptions.Output = settings.Output
		*color = settings.Color
		writer := d.Stdout
		if settings.Output != "json" {
			writer = cliruntime.ColorWriter(writer, settings.Color, d.Environment, d.Terminal)
		}
		return localexec.History(cmd.Context(), historyOptions, writer, d.Stderr)
	}}
	f = history.Flags()
	f.StringVar(&historyOptions.Workspace, "workspace", "", "existing data directory")
	f.StringVar(&historyOptions.Name, "name", historyOptions.Name, "immutable project/name")
	f.StringVar(&historyOptions.CommandID, "command", "", "command UUID")
	f.StringVar(&historyOptions.Cursor, "cursor", "", "page cursor")
	f.IntVar(&historyOptions.Limit, "limit", 50, "page size (1-100)")
	f.StringP("output", "o", "table", "output format: table or json")
	f.String("config", ".inframill.yaml", "optional CLI defaults YAML")
	cliruntime.CompleteFiles(history)
	cliruntime.Complete(history, "output", "table", "json")
	root.AddCommand(history)
	config := ".inframill.yaml"
	serve := &cobra.Command{Use: "server", Short: "Start the daemon using required application configuration", GroupID: nativeGroup, Args: cliruntime.ExactArgs(0), Example: "  inframill server --config .inframill.yaml", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(config) == "" {
			return cliruntime.InputError{Message: "--config requires a nonempty path"}
		}
		return server.Serve(cmd.Context(), d.Environment, config, d.Version, d.Stderr)
	}}
	serve.Flags().StringVar(&config, "config", config, "required application YAML")
	cliruntime.CompleteFiles(serve)
	root.AddCommand(serve)
	driver := &cobra.Command{Use: "driver", Short: "Inspect local Drivers and run Driver protocol entry points", GroupID: nativeGroup}
	driver.AddCommand(&cobra.Command{Use: "debug info|validate|run COMMAND", Short: "Run the debug Driver protocol with exact input/output streams", DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
		code := debugdriver.Serve(cmd.Context(), debugdriver.Driver{}, d.DebugVersion, args, d.Stdin, d.Stdout, d.Stderr)
		if code != 0 {
			return &localexec.ExitError{Code: code, Cause: errors.New("driver protocol failed")}
		}
		return nil
	}})

	driverList := &cobra.Command{Use: "list", Short: "List configured local Drivers and their last info probe health", Args: cliruntime.ExactArgs(0), Example: "  inframill driver list --columns id,status,reason", RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := cliruntime.Resolve(cmd, nil, false, false)
		if err != nil {
			return err
		}
		*color = settings.Color
		writer := d.Stdout
		if settings.Output != "json" {
			writer = cliruntime.ColorWriter(writer, settings.Color, d.Environment, d.Terminal)
		}
		config, _ := cmd.Flags().GetString("config")
		columns, _ := cmd.Flags().GetString("columns")
		return localexec.ListDrivers(cmd.Context(), localexec.DriverListOptions{Config: config, Output: settings.Output, Columns: columns, ColumnsSet: cmd.Flags().Changed("columns")}, d.Environment, writer)
	}}
	driverList.Flags().String("config", ".inframill.yaml", "required application YAML")
	driverList.Flags().StringP("output", "o", "table", "output format: table or json")
	driverList.Flags().String("columns", "", "comma-separated columns: id,name,version,status,reason,checked_at")
	cliruntime.CompleteFiles(driverList)
	cliruntime.Complete(driverList, "output", "table", "json")
	driver.AddCommand(driverList)
	root.AddCommand(driver)
}

const (
	nativeGroup = "native"
)

func (w *trackedWriter) failure() error { w.mu.Lock(); defer w.mu.Unlock(); return w.err }

const (
	utilityGroup = "utility"
)
