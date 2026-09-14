//go:build linux || darwin

package process

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
