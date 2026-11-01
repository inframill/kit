//go:build linux || darwin

package runmill

import "syscall"

// Names shared by Linux and Darwin; numbers come from the target OS constants.
var commonSignals = map[string]syscall.Signal{
	"SIGHUP": syscall.SIGHUP, "SIGINT": syscall.SIGINT,
	"SIGQUIT": syscall.SIGQUIT, "SIGILL": syscall.SIGILL,
	"SIGTRAP": syscall.SIGTRAP, "SIGABRT": syscall.SIGABRT,
	"SIGIOT": syscall.SIGIOT, "SIGFPE": syscall.SIGFPE,
	"SIGKILL": syscall.SIGKILL, "SIGBUS": syscall.SIGBUS,
	"SIGSEGV": syscall.SIGSEGV, "SIGSYS": syscall.SIGSYS,
	"SIGPIPE": syscall.SIGPIPE, "SIGALRM": syscall.SIGALRM,
	"SIGTERM": syscall.SIGTERM, "SIGURG": syscall.SIGURG,
	"SIGSTOP": syscall.SIGSTOP, "SIGTSTP": syscall.SIGTSTP,
	"SIGCONT": syscall.SIGCONT, "SIGCHLD": syscall.SIGCHLD,
	"SIGTTIN": syscall.SIGTTIN, "SIGTTOU": syscall.SIGTTOU,
	"SIGIO": syscall.SIGIO, "SIGXCPU": syscall.SIGXCPU,
	"SIGXFSZ": syscall.SIGXFSZ, "SIGVTALRM": syscall.SIGVTALRM,
	"SIGPROF": syscall.SIGPROF, "SIGWINCH": syscall.SIGWINCH,
	"SIGUSR1": syscall.SIGUSR1, "SIGUSR2": syscall.SIGUSR2,
}

func signalNumber(name string) syscall.Signal {
	if signal := commonSignals[name]; signal != 0 {
		return signal
	}
	return platformSignal(name)
}
