// Package runner serves one independently versioned provider over JSON stdin/stdout.
package runner

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sir-hassan/inframill/pkg/runmill"
)

type Release struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	ToolVersion string `json:"tool_version"`
	Protocol    int    `json:"protocol"`
}

func Main(metadata []byte, factory func(Release, string) runmill.Provider) {
	var release Release
	if err := json.Unmarshal(metadata, &release); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "version" {
		if err := json.NewEncoder(os.Stdout).Encode(release); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	flags := flag.NewFlagSet(release.Name, flag.ContinueOnError)
	tool := flags.String("tool", "", "absolute tool executable override")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if code := runmill.Serve(ctx, factory(release, *tool), runmill.ServeOptions{
		Version: release.Version, Args: flags.Args(), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	}); code != 0 {
		os.Exit(code)
	}
}
