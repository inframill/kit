package main

import (
	"github.com/inframill/kit/drivers/roots/internal/runner"
	implementation "github.com/inframill/kit/drivers/roots/opentofu/0.2.0"
	wire "github.com/sir-hassan/inframill/pkg/driver"
)

func main() {
	runner.Main(implementation.ReleaseJSON, func(r runner.Release, tool string) wire.Driver {
		return implementation.Driver{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
	})
}
