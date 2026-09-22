package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/capability/robotconfig"
	"robot/internal/entry/tcpapi"
	"robot/internal/entry/webadmin"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/foundation/network"
	"robot/internal/scheduler"
	"robot/internal/shared"
)

// runSimulatorBackend is deliberately separate from native startup. It does
// not open MySQL, initialize native RSA/party services, or construct market
// and mail adapters that require native tables.
func runSimulatorBackend(cfg *config.SysConfig, paths layout.Paths, info shared.BackendInfo) int {
	if err := runtimeinit.InitConfigOnly(cfg); err != nil {
		foundationlog.Robotf("SIMULATOR_RUNTIME_INIT_FAILED backend=%s err=%v\n", info.ID, err)
		return 1
	}
	rc, err := loadRequiredRobotConfig(paths.RobotConfig())
	if err != nil {
		foundationlog.Robotf("SIMULATOR_RUNTIME_CONFIG_FAILED err=%v\n", err)
		return 1
	}
	if err := ensureSimulatorOpenFileLimit(rc); err != nil {
		foundationlog.Robotf("SIMULATOR_OPEN_FILE_CAPACITY_FAILED err=%v\n", err)
		return 1
	}
	state, err := openBackendRobotState(info, paths)
	if err != nil {
		foundationlog.Robotf("SIMULATOR_ROBOT_STATE_FAILED err=%v\n", err)
		return 1
	}
	transports, err := composeBackendTransports(info, cfg)
	if err != nil {
		foundationlog.Robotf("SIMULATOR_TRANSPORT_FAILED err=%v\n", err)
		return 1
	}
	defer transports.close()
	townMaps, err := loadBackendTownMapCatalog(context.Background(), info, cfg)
	if err != nil {
		foundationlog.Robotf("SIMULATOR_TOWN_MAP_FAILED err=%v\n", err)
		return 1
	}
	manager := scheduler.NewRobotManager(nil, cfg, nil)
	manager.SetRobotStateDirectory(state)
	manager.SetBackendActionTransport(transports.actions)
	manager.SetBackendSessionTransport(transports.sessions)
	manager.SetTownMapCatalog(townMaps)
	defer func() {
		if err := manager.Shutdown(); err != nil {
			foundationlog.Robotf("SIMULATOR_MANAGER_SHUTDOWN_FAILED err=%v\n", err)
		}
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.RobotPort)
	tcpServer := network.NewTCPServer(addr)
	tcpServer.SetLimits(256, 90*time.Second, 15*time.Second)
	tcpServer.OnMessage(func(clientID string, raw []byte) {
		response := tcpapi.HandlePacket(clientID, string(raw), manager)
		if response != "" {
			if err := tcpServer.SendTo(clientID, []byte(response)); err != nil {
				foundationlog.Robotf("SIMULATOR_TCP_RESPONSE_FAILED client=%s err=%v\n", clientID, err)
			}
		}
	})
	if err := tcpServer.Start(); err != nil {
		foundationlog.Robotf("SIMULATOR_TCP_START_FAILED addr=%s err=%v\n", addr, err)
		return 1
	}
	defer tcpServer.Close()
	stopWebAdmin := webadmin.StartSupervisor(cfg)
	defer stopWebAdmin()
	manager.StartAutoActions()
	foundationlog.Robotf("SIMULATOR_STARTED backend=%s tcp=%s\n", info.ID, addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh
	foundationlog.Robotf("SIMULATOR_STOPPING backend=%s\n", info.ID)
	return 0
}

func ensureSimulatorOpenFileLimit(rc robotconfig.RuntimeConfig) error {
	if rc.MaxOnlineRobots < 1 {
		return fmt.Errorf("max_online_robots must be positive")
	}
	return nil
}
