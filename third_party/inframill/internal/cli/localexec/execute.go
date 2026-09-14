package localexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/internal/backend"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type commandRunner struct {
	engine         *inframill.Engine
	backend        *backend.Backend
	stdout, stderr *liveWriter
}

func (runner commandRunner) execute(ctx, runCtx context.Context, instanceID inframill.InstanceID, command string, args inframill.Arguments) (result, error) {
	r := result{InstanceID: instanceID}
	// builtin.delete belongs to Instance lifecycle, not direct Driver execution.
	if strings.HasPrefix(command, "builtin.") {
		return r, invalid("local exec requires a Driver command")
	}
	id, err := runner.engine.SubmitCommand(ctx, instanceID, command, args, "")
	if err != nil {
		return r, err
	}
	r.CommandID = id
	r.Status = "unknown"
	c, err := runner.engine.Wait(runCtx, instanceID, id)
	if err != nil {
		c, err = runner.recoverCommand(instanceID, id)
		if err != nil {
			return r, err
		}
	}

	r = commandResult(instanceID, c)
	if failure := errors.Join(runner.stdout.failure(), runner.stderr.failure()); failure != nil && ctx.Err() == nil {
		return r, &ExitError{Code: 3, Cause: errors.New("terminal output failed; inspect workspace history")}
	}
	if ctx.Err() != nil || c.Status == inframill.Interrupted {
		return r, &ExitError{Code: 130, Cause: errors.New("local command interrupted")}
	}
	if c.Status != inframill.Succeeded {
		if c.Error != nil {
			return r, &ExitError{Code: 1, Cause: fmt.Errorf("local command failed [%s]: %s; inspect workspace history", c.Error.Code, c.Error.Message)}
		}
		return r, &ExitError{Code: 1, Cause: errors.New("local command failed; inspect structured result or history")}
	}
	return r, nil
}

func (runner commandRunner) recoverCommand(instanceID inframill.InstanceID, commandID inframill.CommandID) (inframill.CommandExecution, error) {
	// A cancelled Shutdown cancels execution; a second bounded Shutdown waits for
	// the worker to finish writing interruption and release its database.
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	_ = runner.engine.Shutdown(cancelled) //nolint:errcheck // Expected cancellation; wait below.
	cleanup, stopCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCleanup()
	if closeErr := runner.engine.Shutdown(cleanup); closeErr != nil {
		return inframill.CommandExecution{}, closeErr
	}
	reopened, openErr := runner.backend.SpawnInstance(cleanup, instanceID)
	if openErr != nil {
		return inframill.CommandExecution{}, openErr
	}
	c, openErr := reopened.GetCommandExecution(cleanup, commandID)
	closeErr := reopened.Close(cleanup)
	if failure := errors.Join(openErr, closeErr); failure != nil {
		return inframill.CommandExecution{}, failure
	}
	return c, nil
}
