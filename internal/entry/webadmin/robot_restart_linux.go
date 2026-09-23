//go:build linux

package webadmin

import (
	"fmt"
	"os/exec"
	"strings"
)

func startRobotRestartHelper(exe, configDir string) error {
	if strings.TrimSpace(exe) == "" {
		return fmt.Errorf("empty executable path")
	}
	cmd := exec.Command("/bin/sh", "-c", buildRobotRestartScript(exe, configDir))
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
