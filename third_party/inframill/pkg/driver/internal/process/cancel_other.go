//go:build !linux && !darwin

package process

import (
	"os/exec"
	"time"
)

func configureCancellation(child *exec.Cmd) { child.WaitDelay = 2 * time.Second }
