//go:build !windows

package kopia

import (
	"os/exec"
	"time"
)

func configureSubprocess(cmd *exec.Cmd) {
	cmd.WaitDelay = 3 * time.Second
}
