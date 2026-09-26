//go:build !linux

package webadmin

import "os/exec"

func configureCommandProcessGroup(_ *exec.Cmd) {}

func killCommandProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil && (cmd.ProcessState == nil || !cmd.ProcessState.Exited()) {
		_ = cmd.Process.Kill()
	}
}
