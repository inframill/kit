// Command protoc-gen-inframill-cli is a build-time protobuf plugin.
package main

import (
	"google.golang.org/protobuf/compiler/protogen"

	cligen "github.com/sir-hassan/inframill/internal/cli/generator"
)

func main() { protogen.Options{}.Run(cligen.Generate) }
