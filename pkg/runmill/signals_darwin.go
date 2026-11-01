package runmill

import "syscall"

func platformSignal(name string) syscall.Signal {
	switch name {
	case "SIGEMT":
		return syscall.SIGEMT
	case "SIGINFO":
		return syscall.SIGINFO
	default:
		return 0
	}
}
