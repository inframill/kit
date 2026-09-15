// Package releases embeds the maintained driver release descriptors.
package releases

import "embed"

// Drivers is the source of the executable's release catalog.
//
//go:embed drivers/*/*.yaml
var Drivers embed.FS
