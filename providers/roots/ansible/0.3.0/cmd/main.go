package main

import (
	implementation "github.com/inframill/kit/providers/roots/ansible/0.3.0"
	"github.com/inframill/kit/providers/roots/internal/runner"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

func main() {
	runner.Main(implementation.ReleaseJSON, func(r runner.Release, tool string) runmill.Provider {
		return implementation.Provider{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
	})
}
