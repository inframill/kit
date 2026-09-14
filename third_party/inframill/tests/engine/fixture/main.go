// Command fixture builds disposable Blueprint layouts for the daemon test suite.
// It is not part of the Inframill application or its release targets.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sir-hassan/inframill/internal/blueprint"
)

func main() {
	if len(os.Args) != 3 {
		_, _ = fmt.Fprintln(os.Stderr, "fixture requires source and new output directory")
		os.Exit(2)
	}
	if _, err := blueprint.BuildLayout(context.Background(), os.Args[1], os.Args[2]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
