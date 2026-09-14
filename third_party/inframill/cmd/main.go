// Command inframill composes the application CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sir-hassan/inframill/internal/cli"
)

// version and debugVersion remain independent release linker targets.
var (
	version      = "0.0.0-dev"
	debugVersion = "0.0.0-dev"
)

func main() {
	localQuery := len(os.Args) > 2 && os.Args[1] == "driver" && os.Args[2] == "list"
	if localQuery || (len(os.Args) > 1 && (os.Args[1] == "exec" || os.Args[1] == "history")) {
		signal.Ignore(syscall.SIGPIPE)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); stop() }()
	code := cli.Run(ctx, os.Args[1:], cli.Dependencies{Environment: os.Environ(), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Version: version, DebugVersion: debugVersion})
	stop()
	os.Exit(code)
}
