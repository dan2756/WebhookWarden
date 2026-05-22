//go:build !unix

package main

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op without Unix process groups; cancellation
// kills only the direct child.
func setProcessGroup(*exec.Cmd) {}

func signalExitCode(*os.ProcessState) int { return -1 }
