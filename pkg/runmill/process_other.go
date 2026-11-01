//go:build !linux && !darwin

package runmill

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

func configureCancellation(child *exec.Cmd) { child.WaitDelay = 2 * time.Second }

func ValidateSignal(name string) error {
	if name != "SIGKILL" {
		return errors.New("unsupported OS signal")
	}
	return nil
}

func signalProcess(child *exec.Cmd, name string) error {
	if err := ValidateSignal(name); err != nil {
		return err
	}
	return child.Process.Signal(os.Kill)
}
