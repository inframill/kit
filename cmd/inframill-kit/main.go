package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/inframill/kit/internal/catalog"
	"github.com/inframill/kit/internal/driverkit"
)

var version = "0.1.0"

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 1 && args[0] == "version" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": version, "drivers": catalog.List()})
	}
	if len(args) == 2 && args[0] == "drivers" && args[1] == "list" {
		return json.NewEncoder(os.Stdout).Encode(catalog.List())
	}
	if len(args) < 3 || args[0] != "driver" {
		return errors.New("usage: inframill-kit driver NAME --driver-version VERSION [--tool ABSOLUTE_PATH] info|validate|run [COMMAND]")
	}
	fs := flag.NewFlagSet("driver", flag.ContinueOnError)
	v := fs.String("driver-version", "", "exact implementation version")
	tool := fs.String("tool", "", "absolute tool executable override")
	if e := fs.Parse(args[2:]); e != nil {
		return e
	}
	r, e := catalog.Get(args[1], *v)
	if e != nil {
		return e
	}
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 {
		return errors.New("select protocol action and optional command")
	}
	command := ""
	if len(rest) == 2 {
		command = rest[1]
	}
	return driverkit.Execute(context.Background(), r, rest[0], command, os.Stdin, os.Stdout, driverkit.Options{Tool: *tool, Stderr: os.Stderr})
}
