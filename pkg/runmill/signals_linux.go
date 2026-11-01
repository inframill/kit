package runmill

import "syscall"

func platformSignal(name string) syscall.Signal {
	switch name {
	case "SIGCLD":
		return syscall.SIGCLD
	case "SIGPOLL":
		return syscall.SIGPOLL
	case "SIGPWR":
		return syscall.SIGPWR
	case "SIGSTKFLT":
		return syscall.SIGSTKFLT
	case "SIGUNUSED":
		return syscall.SIGUNUSED
	default:
		return 0
	}
}
