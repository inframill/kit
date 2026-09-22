package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	catalog "github.com/inframill/kit/providers/tests/catalog"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

var version string
var errServed = errors.New("provider invocation failed")

func main() {
	if e := run(os.Args[1:]); e != nil {
		if !errors.Is(e, errServed) {
			fmt.Fprintln(os.Stderr, e)
		}
		os.Exit(1)
	}
}
func run(args []string) error {
	if version == "" {
		version = "test"
	}
	if len(args) == 1 && args[0] == "version" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": version, "providers": catalog.List()})
	}
	if len(args) == 2 && args[0] == "providers" && args[1] == "list" {
		return json.NewEncoder(os.Stdout).Encode(catalog.List())
	}
	if len(args) < 3 || args[0] != "provider" {
		return errors.New("usage: inframill-kit provider NAME --provider-version VERSION [--tool ABSOLUTE_PATH] (JSON request on stdin)")
	}
	fs := flag.NewFlagSet("provider", flag.ContinueOnError)
	v := fs.String("provider-version", "", "exact implementation version")
	tool := fs.String("tool", "", "absolute tool executable override")
	if e := fs.Parse(args[2:]); e != nil {
		return e
	}
	implementation, e := catalog.New(args[1], *v, *tool)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if runmill.Serve(ctx, implementation, runmill.ServeOptions{
		Version: *v, Args: fs.Args(), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	}) != 0 {
		return errServed
	}
	return nil
}
