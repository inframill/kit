package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/inframill/kit/internal/catalog"
	"github.com/inframill/kit/internal/cli"
)

// Set by the release builder. Ordinary go builds identify themselves as development builds.
var version = "dev"

func main() {
	providers, err := catalog.Builtin()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], version, providers, os.Stdin, os.Stdout, os.Stderr))
}
