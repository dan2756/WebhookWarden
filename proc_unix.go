//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup starts the command in a process group of its own, and
// makes cancellation (a timeout, or a forced shutdown) kill that whole
// group, not just the direct child: `sh -c 'make deploy'` leaves make and
// everything it started running if only sh is killed.
//
// The group gets a new session, too, so the command has no controlling
// terminal. In a background group of hook's own session, a prompt that
// reads /dev/tty (git, sudo, ssh) would be stopped by SIGTTIN and hang the
// queue; without a terminal it fails instead.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		// A negative pid signals every process in the group.
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// signalExitCode returns 128+n for a process killed by signal n, the way
// shells report it.
func signalExitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return -1
}
