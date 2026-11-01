package runmill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// processError reports a nonzero process exit code.
type processError struct {
	Program  string
	ExitCode int
}

func (e *processError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Program, e.ExitCode)
}

// runProcess invokes the configured program and literal arguments.
func runProcess(ctx context.Context, command processCommand) error {
	if command.Program == "" {
		return errors.New("process program is required")
	}
	environment := controlledEnvironment(command.Env)
	//nolint:gosec // The caller supplies a structured program and argument array; no shell is involved.
	child := exec.CommandContext(ctx, command.Program, command.Args...)
	child.Env = environment
	configureCancellation(child)
	child.Stdin = command.Stdin
	child.Stdout = command.Stdout
	child.Stderr = command.Stderr
	if err := child.Start(); err != nil {
		return fmt.Errorf("start %s: %w", command.Program, err)
	}
	process := &liveProcess{child: child}
	var observerErr error
	if command.Process != nil && command.Process.Started != nil {
		observerErr = observeStart(command.Process.Started, process)
		if observerErr != nil {
			_ = child.Cancel()
		}
	}
	runErr := child.Wait()
	process.mu.Lock()
	process.done = true
	process.mu.Unlock()
	if command.Process != nil && command.Process.Exited != nil && child.ProcessState != nil {
		command.Process.Exited(child.ProcessState.ExitCode())
	}
	if observerErr != nil {
		return errors.Join(observerErr, runErr)
	}
	if runErr == nil {
		return nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return errors.Join(contextErr, runErr)
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return &processError{Program: command.Program, ExitCode: exitErr.ExitCode()}
	}
	return fmt.Errorf("start %s: %w", command.Program, runErr)
}

func observeStart(callback func(Process) error, process Process) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("process start observer panicked")
		}
	}()
	return callback(process)
}

type liveProcess struct {
	child *exec.Cmd
	mu    sync.Mutex
	done  bool
}

func (p *liveProcess) PID() int { return p.child.Process.Pid }

func (p *liveProcess) Signal(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return os.ErrProcessDone
	}
	return signalProcess(p.child, name)
}

func controlledEnvironment(values map[string]string) []string {
	environment := filteredEnvironment(os.Environ())
	maps.Copy(environment, values)
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+environment[name])
	}
	return result
}

// processCommand is a shell-free external process invocation.
type processCommand struct {
	Process *ProcessObserver
	Program string
	Args    []string
	Env     map[string]string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func filteredEnvironment(environ []string) map[string]string {
	environment := make(map[string]string)
	for _, item := range environ {
		name, value, _ := strings.Cut(item, "=")
		switch name {
		case "HOME", "PATH", "TMPDIR", "SSL_CERT_DIR", "SSL_CERT_FILE", "SSH_AUTH_SOCK":
			environment[name] = value
		}
	}
	return environment
}
