//go:build windows

package webadmin

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsRestartLogStaysOutsideConfigDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	got := windowsRestartLogPath(root)
	want := root + ".restart.log"
	if got != want {
		t.Fatalf("restart log path = %q, want %q", got, want)
	}
	if filepath.Dir(got) == filepath.Clean(root) {
		t.Fatalf("restart log must not lock the config directory: %q", got)
	}
}

func TestRestartedWindowsRobotUsesVisibleConsole(t *testing.T) {
	cmd := exec.Command("robot.exe")
	setVisibleWindowsProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.HideWindow {
		t.Fatalf("restarted robot must keep a visible console: %+v", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.CreationFlags&windowsCreateNewConsole == 0 || cmd.SysProcAttr.CreationFlags&windowsDetachedProcess != 0 {
		t.Fatalf("restarted robot flags=%#x", cmd.SysProcAttr.CreationFlags)
	}
}
