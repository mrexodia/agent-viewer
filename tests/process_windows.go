//go:build windows

package tests

import (
	"fmt"
	"os/exec"
)

// setupProcessGroup is a no-op on Windows (handled at kill time)
func setupProcessGroup(cmd *exec.Cmd) {
	// Nothing to do on Windows
}

// killProcessTree kills the process and all its children
func killProcessTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		// Kill the process tree on Windows
		exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)).Run()
	}
}
