package webadmin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"robot/internal/foundation/config"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

const webAdminShutdownTimeout = 6 * time.Second

func StartSupervisor(cfg *config.SysConfig, backend ...shared.BackendID) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			cmd := newCommand(cfg, backend...)
			if cmd == nil {
				return
			}
			if err := cmd.Start(); err != nil {
				foundationlog.Robotf("web admin start failed: %v\n", err)
				select {
				case <-stop:
					return
				case <-time.After(3 * time.Second):
					continue
				}
			}
			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}
			foundationlog.Robotf("web admin listening on %s pid=%d\n", fmt.Sprintf("0.0.0.0:%d", cfg.WebPort), pid)
			waitCh := make(chan error, 1)
			go func() { waitCh <- cmd.Wait() }()
			select {
			case <-stop:
				if cmd.Process != nil {
					if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
						foundationlog.Robotf("web admin SIGTERM failed pid=%d err=%v\n", pid, err)
					}
					select {
					case <-waitCh:
					case <-time.After(webAdminShutdownTimeout):
						foundationlog.Robotf("web admin graceful shutdown timed out pid=%d; killing\n", pid)
						_ = cmd.Process.Kill()
						<-waitCh
					}
				}
				return
			case err := <-waitCh:
				foundationlog.Robotf("web admin exited pid=%d err=%v; restarting\n", pid, err)
				select {
				case <-stop:
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func newCommand(cfg *config.SysConfig, backend ...shared.BackendID) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		foundationlog.Robotf("web admin executable lookup failed: %v\n", err)
		return nil
	}
	robotAddr := fmt.Sprintf("127.0.0.1:%d", cfg.RobotPort)
	webAddr := fmt.Sprintf("0.0.0.0:%d", cfg.WebPort)
	selectedBackend := shared.BackendNative
	if len(backend) > 0 && backend[0] != "" {
		selectedBackend = backend[0]
	}
	snapshot, err := json.Marshal(cfg)
	if err != nil {
		foundationlog.Robotf("web admin config snapshot failed: %v\n", err)
		return nil
	}
	cmd := exec.Command(exe, "--web-admin", "--web-config-stdin", "--backend-id", string(selectedBackend), "--robot-addr", robotAddr, "--web-addr", webAddr)
	cmd.Stdin = bytes.NewReader(snapshot)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}
