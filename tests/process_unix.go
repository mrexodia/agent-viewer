//go:build !windows

package tests

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup sets up a new process group for the command
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills the process and all its children
func killProcessTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		// Kill the process group (negative PID)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
