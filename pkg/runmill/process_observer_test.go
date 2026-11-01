//go:build linux || darwin

package runmill

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// SPEC-0000 REQ-003/027: OS outcomes are observable even without protocol replies.
func TestProcessObserver(t *testing.T) {
	for _, code := range []int{0, 7} {
		pid, exit := 0, -99
		command := processCommand{Program: testShell, Args: []string{"-c", "exit \"$1\"", "fixture", "0"}, Process: &ProcessObserver{
			Started: func(process Process) error { pid = process.PID(); return nil },
			Exited:  func(value int) { exit = value },
		}}
		if code == 7 {
			command.Args[3] = "7"
		}
		err := runProcess(t.Context(), command)
		if pid <= 0 || exit != code || (err != nil) != (code != 0) {
			t.Fatalf("pid=%d exit=%d error=%v", pid, exit, err)
		}
	}
	called := false
	err := runProcess(t.Context(), processCommand{Program: filepath.Join(t.TempDir(), "absent"), Process: &ProcessObserver{
		Started: func(Process) error { called = true; return nil }, Exited: func(int) { called = true },
	}})
	if err == nil || called {
		t.Fatal("failed spawn reported a process")
	}
}

func TestProcessObserverFailureReapsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	want := errors.New("synthetic persistence failure")
	exited := false
	err := runProcess(ctx, processCommand{Program: testShell, Args: []string{"-c", "sleep 20"}, Process: &ProcessObserver{
		Started: func(Process) error { return want },
		Exited: func(code int) {
			exited = true
			if code != -1 {
				t.Errorf("expected killed process, got %d", code)
			}
		},
	}})
	if !errors.Is(err, want) || !exited || ctx.Err() != nil {
		t.Fatal("observer failure did not promptly reap child", err)
	}
}
