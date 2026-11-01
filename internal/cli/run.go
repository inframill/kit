// Package cli hosts the Kit executable and its provider protocol entry point.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/inframill/kit/internal/catalog"
	"github.com/inframill/kit/pkg/runmill"
)

const usage = `usage: kit-exec list | version | NAME-VERSION [--tool ABSOLUTE_PATH] | NAME-VERSION version
Provider requests use the Inframill JSON protocol on stdin.
`

func Run(ctx context.Context, args []string, version string, providers *catalog.Catalog, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encode := func(value any) int {
		if err := json.NewEncoder(stdout).Encode(value); err != nil {
			return fail(err)
		}
		return 0
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	if len(args) == 1 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Fprint(stdout, usage)
			return 0
		case "list":
			return encode(providers.List())
		case "version":
			return encode(map[string]string{"version": version})
		}
	}
	release, err := providers.Get(args[0])
	if err != nil {
		return fail(err)
	}
	if len(args) == 2 && args[1] == "version" {
		return encode(release)
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	tool := flags.String("tool", "", "absolute tool executable override")
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	provider, err := providers.Provider(args[0], *tool)
	if err != nil {
		return fail(err)
	}
	return runmill.Serve(ctx, provider, runmill.ServeOptions{
		Version: release.Version, Args: flags.Args(), Stdin: stdin, Stdout: stdout, Stderr: stderr,
	})
}
