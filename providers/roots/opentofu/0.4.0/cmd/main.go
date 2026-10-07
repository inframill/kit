package main

import (
	"github.com/inframill/kit/providers/roots/internal/runner"
	implementation "github.com/inframill/kit/providers/roots/opentofu/0.4.0"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

func main() {
	runner.Main(implementation.ReleaseJSON, func(r runner.Release, tool string) runmill.Provider {
		return implementation.Provider{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
	})
}
