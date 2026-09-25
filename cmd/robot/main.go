package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/capability/robotconfig"
	backendregistry "robot/internal/composition/backend"
	"robot/internal/entry/webadmin"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/protocol/dnf"
	"robot/internal/shared"
)

func main() {
	restartHelper := webadmin.RestartHelperRequested(os.Args[1:])
	code := runMain()
	if code != 0 && !restartHelper {
		waitForFatalExit()
	}
	os.Exit(code)
}

func runMain() int {
	if webadmin.RestartHelperRequested(os.Args[1:]) {
		if err := webadmin.RunRestartHelper(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "restart helper failed: %v\n", err)
			return 1
		}
		return 0
	}

	dnf.PrintfGreen("robot starting...\n")

	configPath, configDir, err := runtimeConfigPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve config path error: %v\n", err)
		return 1
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config error: %v\n", err)
		return 1
	}
	cfg.ConfigDir = configDir
	paths := layout.New(configDir)
	if err := paths.Ensure(); err != nil {
		fmt.Fprintf(os.Stderr, "create config dir error: %v\n", err)
		return 1
	}
	backendSelection, err := loadBackendSelection(paths.BackendSelection())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load backend selection error: %v\n", err)
		return 1
	}
	backendInfo, err := backendregistry.Select(backendSelection.BackendID, runtime.GOOS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend selection error: %v\n", err)
		return 1
	}
	if err := applyBackendSelectionSettings(cfg, backendSelection); err != nil {
		fmt.Fprintf(os.Stderr, "backend settings error: %v\n", err)
		return 1
	}
	backendReinitialized, err := runtimeinit.PrepareBackendRuntime(paths, backendSelection)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend runtime preparation error: %v\n", err)
		return 1
	}
	dnf.ConfigureLogRotation(cfg.LogMaxSizeMB, cfg.LogMaxBackups)
	if err := dnf.LogInit(paths.RobotLog()); err != nil {
		fmt.Fprintf(os.Stderr, "init log error: %v\n", err)
		return 1
	}
	foundationlog.SetRobotSink(func(msg string) {
		dnf.LogString(msg)
	})
	defer func() {
		foundationlog.SetRobotSink(nil)
		dnf.LogClose()
	}()
	dnf.LogString(fmt.Sprintf("ROBOT_CONFIG path=%s config_dir=%s\n", configPath, cfg.ConfigDir))
	dnf.LogString(fmt.Sprintf("BACKEND_SELECTED id=%s generation=%d selected_at=%s capabilities=%d\n", backendInfo.ID, backendSelection.ConfigGeneration, backendSelection.SelectedAt.UTC().Format(time.RFC3339), len(backendInfo.Capabilities)))
	if backendReinitialized {
		dnf.LogString(fmt.Sprintf("BACKEND_RUNTIME_REINITIALIZED id=%s generation=%d\n", backendInfo.ID, backendSelection.ConfigGeneration))
	}
	return runBackend(cfg, paths, backendInfo, backendSelection)
}

func loadBackendSelection(path string) (shared.BackendSelection, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return shared.BackendSelection{BackendID: shared.BackendS4A21}, nil
	}
	if err != nil {
		return shared.BackendSelection{}, err
	}
	return shared.DecodeBackendSelection(data)
}

func runtimeConfigPaths() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	paths, err := layout.FromExecutable(exe)
	if err != nil {
		return "", "", err
	}
	return paths.MainConfig(), paths.Root, nil
}

func loadRequiredRobotConfig(path string) (robotconfig.RuntimeConfig, error) {
	rc, err := robotconfig.LoadFile(path)
	if err != nil {
		return robotconfig.RuntimeConfig{}, fmt.Errorf("load %s: %w", path, err)
	}
	return rc, nil
}

func recoveryWebURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}

func logRobotActionf(format string, args ...interface{}) {
	foundationlog.Robotf(format, args...)
}
