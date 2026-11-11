//go:build windows

package kopia

import (
	"os/exec"
	"syscall"
	"time"
)

// configureSubprocess prevents flashing console windows when spawning CLI child processes on Windows
// and bounds pipe wait delays on cancellation.
func configureSubprocess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 3 * time.Second
}
