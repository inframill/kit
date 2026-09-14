package process

import (
	"io"
)

// Command is a shell-free external process invocation.
type Command struct {
	Program   string
	Args      []string
	Env       map[string]string
	SecretEnv map[string]Secret
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// Secret reveals a runtime credential only when constructing the child environment.
type Secret interface{ Reveal() string }
