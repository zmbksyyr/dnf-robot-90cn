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
	"unsafe"
)

const (
	windowsCreateNewProcessGroup = 0x00000200
	windowsDetachedProcess       = 0x00000008
	windowsCreateNewConsole      = 0x00000010
)

func startRobotRestartHelper(exe, configDir string) error {
	if exe == "" {
		return fmt.Errorf("empty executable path")
	}
	restartLogPath := windowsRestartLogPath(configDir)
	if err := os.MkdirAll(filepath.Dir(restartLogPath), 0755); err != nil {
		return fmt.Errorf("create restart log directory: %w", err)
	}
	restartLog, err := os.OpenFile(restartLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open restart log: %w", err)
	}
	defer restartLog.Close()
	cmd := exec.Command(exe,
		"--restart-helper",
		"--restart-exe", exe,
		"--restart-config-dir", configDir,
		"--restart-parent-pid", fmt.Sprint(os.Getpid()),
	)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdout = restartLog
	cmd.Stderr = restartLog
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

	fmt.Fprintf(os.Stderr, "[%s] restart helper started parent_pid=%d exe=%s\n", time.Now().Format(time.RFC3339), *parentPID, resolvedExe)
	// Let the Web handler flush its response before terminating the Robot.
	time.Sleep(time.Second)
	if err := terminateWindowsProcess(*parentPID); err != nil {
		return fmt.Errorf("stop current robot process: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[%s] previous robot stopped parent_pid=%d\n", time.Now().Format(time.RFC3339), *parentPID)
	if err := startWindowsRobot(resolvedExe, *configDir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[%s] replacement robot launched\n", time.Now().Format(time.RFC3339))
	return nil
}

func terminateWindowsProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Kill(); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !windowsProcessExists(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not exit within 10 seconds", pid)
}

func windowsProcessExists(pid int) bool {
	const (
		processQueryLimitedInformation = 0x1000
		stillActive                    = 259
	)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	closeHandle := kernel32.NewProc("CloseHandle")
	getExitCodeProcess := kernel32.NewProc("GetExitCodeProcess")
	handle, _, _ := openProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	var exitCode uint32
	ok, _, _ := getExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	_, _, _ = closeHandle.Call(handle)
	return ok != 0 && exitCode == stillActive
}

func startWindowsRobot(exe, configDir string) error {
	_ = configDir
	robot := exec.Command(exe)
	robot.Dir = filepath.Dir(exe)
	setVisibleWindowsProcess(robot)
	if err := robot.Start(); err != nil {
		return fmt.Errorf("start robot: %w", err)
	}
	if err := robot.Process.Release(); err != nil {
		return fmt.Errorf("release restarted robot process: %w", err)
	}
	return nil
}

func setVisibleWindowsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windowsCreateNewProcessGroup | windowsCreateNewConsole,
	}
}

func windowsRestartLogPath(configDir string) string {
	return filepath.Clean(configDir) + ".restart.log"
}

func setDetachedWindowsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsCreateNewProcessGroup | windowsDetachedProcess,
	}
}
