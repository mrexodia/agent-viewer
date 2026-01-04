//go:build !windows

package tests

import (
	"os/exec"
	"syscall"
	"time"
)

// setupProcessGroup sets up a new process group for the command
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills the process and all its children
func killProcessTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil {
			// Kill the process group
			syscall.Kill(-pgid, syscall.SIGKILL)
		}
		// Also try killing the process directly
		cmd.Process.Kill()
		// Give it a moment to die
		time.Sleep(50 * time.Millisecond)
	}
}
