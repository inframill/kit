// Package kit embeds the selected release for standalone driver executables.
package kit

import (
	"embed"
	"strings"
)

//go:embed VERSION
var version string

//go:embed releases/*.lock.json
var releases embed.FS

func Version() string { return strings.TrimSpace(version) }

func Release() ([]byte, error) {
	return releases.ReadFile("releases/" + Version() + ".lock.json")
}
