//go:build !windows

// @scope.file kopia.proc
// @hash sha256:f7dcb41058c56053
// @implements spec.kopia_engine sha256:44f993b3db7e5386
// Subprocess pipe and termination WaitDelay configuration for Kopia and rclone child processes.

package kopia

import (
	"os/exec"
	"time"
)

func configureSubprocess(cmd *exec.Cmd) {
	cmd.WaitDelay = 3 * time.Second
}
