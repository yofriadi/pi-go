// go:build windows
//go:build windows
// +build windows

package tools

import (
	"fmt"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {
	// CreationFlags can be set, but let's keep it simple for compilation
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		// Kill process tree on Windows using taskkill
		_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)).Run()
	}
}
