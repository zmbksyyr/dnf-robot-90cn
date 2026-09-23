//go:build windows

package webadmin

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"robot/internal/foundation/layout"
)

const (
	windowsCreateNewProcessGroup = 0x00000200
	windowsDetachedProcess       = 0x00000008
)

func startRobotRestartHelper(exe, configDir string) error {
	if exe == "" {
		return fmt.Errorf("empty executable path")
	}
	cmd := exec.Command(exe,
		"--restart-helper",
		"--restart-exe", exe,
		"--restart-config-dir", configDir,
		"--restart-parent-pid", fmt.Sprint(os.Getpid()),
	)
	cmd.Dir = filepath.Dir(exe)
	setDetachedWindowsProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func runRestartHelper(args []string) error {
	flags := flag.NewFlagSet("restart-helper", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	exe := flags.String("restart-exe", "", "robot executable")
	configDir := flags.String("restart-config-dir", "", "robot config directory")
	parentPID := flags.Int("restart-parent-pid", 0, "robot parent pid")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *parentPID <= 0 || *parentPID == os.Getpid() {
		return fmt.Errorf("invalid restart helper process arguments")
	}
	resolvedExe, err := filepath.Abs(*exe)
	if err != nil {
		return fmt.Errorf("resolve restart executable: %w", err)
	}
	info, err := os.Stat(resolvedExe)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("invalid restart executable %q", resolvedExe)
	}
	if *configDir == "" {
		return fmt.Errorf("empty restart config directory")
	}

	// Let the Web handler flush its response before terminating the Robot.
	time.Sleep(time.Second)
	_ = terminateWindowsProcess(*parentPID)
	time.Sleep(500 * time.Millisecond)
	return startWindowsRobot(resolvedExe, *configDir)
}

func terminateWindowsProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func startWindowsRobot(exe, configDir string) error {
	paths := layout.New(configDir)
	if err := os.MkdirAll(filepath.Dir(paths.StdoutLog()), 0755); err != nil {
		return err
	}
	errorLog, err := os.OpenFile(paths.StartErrorLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer errorLog.Close()

	robot := exec.Command(exe)
	robot.Dir = filepath.Dir(exe)
	stdout, err := os.OpenFile(paths.StdoutLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer stdout.Close()
	robot.Stdout = stdout
	robot.Stderr = errorLog
	setDetachedWindowsProcess(robot)
	if err := robot.Start(); err != nil {
		return fmt.Errorf("start robot: %w", err)
	}
	if err := robot.Process.Release(); err != nil {
		return fmt.Errorf("release restarted robot process: %w", err)
	}
	return nil
}

func setDetachedWindowsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsCreateNewProcessGroup | windowsDetachedProcess,
	}
}
