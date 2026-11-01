//go:build linux || darwin

package runmill

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// A tool may spawn providers or SSH helpers. Cancel its process group, including
// children that inherit output pipes, before waiting for bounded pipe cleanup.
func configureCancellation(child *exec.Cmd) {
	child.WaitDelay = 2 * time.Second
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Cancel = func() error {
		err := syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// ValidateSignal accepts the canonical signals supported by the daemon OS.
func ValidateSignal(name string) error {
	if signalNumber(name) == 0 {
		return errors.New("unsupported OS signal")
	}
	return nil
}

func signalProcess(child *exec.Cmd, name string) error {
	if err := ValidateSignal(name); err != nil {
		return err
	}
	// The os.Process state rejects signalling after its Wait has completed.
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		return err
	}
	err := syscall.Kill(-child.Process.Pid, signalNumber(name))
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
