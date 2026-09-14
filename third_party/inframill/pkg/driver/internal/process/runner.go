// Package process implements the default local process runner for the Inframill
// engine. It never invokes a shell and redacts typed secret environment values
// from streamed output.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Runner executes local programs with a controlled environment.
type Runner struct{}

// Error reports a nonzero process exit code.
type Error struct {
	Program  string
	ExitCode int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Program, e.ExitCode)
}

// Run executes exactly Command.Program with Command.Args. Secret environment
// values are revealed only while constructing the child environment and are
// redacted across streaming write boundaries.
func (Runner) Run(ctx context.Context, command Command) error {
	if command.Program == "" {
		return errors.New("process program is required")
	}
	environment, sensitive := controlledEnvironment(command.Env, command.SecretEnv)
	//nolint:gosec // The engine supplies a structured program and argument array; no shell is involved.
	child := exec.CommandContext(ctx, command.Program, command.Args...)
	child.Env = environment
	configureCancellation(child)
	child.Stdin = command.Stdin
	stdout := newRedactor(command.Stdout, sensitive)
	stderr := newRedactor(command.Stderr, sensitive)
	child.Stdout = stdout
	child.Stderr = stderr
	runErr := child.Run()
	flushErr := errors.Join(stdout.Close(), stderr.Close())
	if runErr == nil {
		return flushErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return errors.Join(contextErr, runErr, flushErr)
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return errors.Join(&Error{Program: command.Program, ExitCode: exitErr.ExitCode()}, flushErr)
	}
	return errors.Join(fmt.Errorf("start %s: %w", command.Program, runErr), flushErr)
}

func controlledEnvironment(values map[string]string, secrets map[string]Secret) ([]string, [][]byte) {
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, _ := strings.Cut(item, "=")
		switch name {
		case "HOME", "PATH", "TMPDIR", "SSL_CERT_DIR", "SSL_CERT_FILE", "SSH_AUTH_SOCK":
			environment[name] = value
		}
	}
	maps.Copy(environment, values)
	sensitive := make([][]byte, 0, len(secrets))
	for name, secret := range secrets {
		if secret == nil {
			continue
		}
		value := secret.Reveal()
		environment[name] = value
		if value != "" {
			sensitive = append(sensitive, []byte(value))
		}
	}
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+environment[name])
	}
	return result, sensitive
}

type redactor struct {
	destination io.Writer
	secrets     [][]byte
	pending     []byte
}

func newRedactor(destination io.Writer, secrets [][]byte) *redactor {
	return &redactor{destination: destination, secrets: secrets}
}

func (r *redactor) Write(value []byte) (int, error) {
	if r.destination == nil || len(value) == 0 {
		return len(value), nil
	}
	r.pending = append(r.pending, value...)
	cut := len(r.pending)
	for _, secret := range r.secrets {
		for length := 1; length < len(secret) && length <= len(r.pending); length++ {
			if bytes.Equal(r.pending[len(r.pending)-length:], secret[:length]) && len(r.pending)-length < cut {
				cut = len(r.pending) - length
			}
		}
	}
	ready := bytes.Clone(r.pending[:cut])
	r.pending = bytes.Clone(r.pending[cut:])
	if err := r.emit(ready); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (r *redactor) Close() error {
	if r.destination == nil || len(r.pending) == 0 {
		return nil
	}
	err := r.emit(r.pending)
	r.pending = nil
	return err
}

func (r *redactor) emit(value []byte) error {
	if len(value) == 0 {
		return nil
	}
	redacted := bytes.Clone(value)
	for _, secret := range r.secrets {
		redacted = bytes.ReplaceAll(redacted, secret, []byte("[REDACTED]"))
	}
	_, err := r.destination.Write(redacted)
	return err
}
