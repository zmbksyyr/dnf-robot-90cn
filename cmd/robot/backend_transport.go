package main

import (
	"fmt"
	"net"
	"path/filepath"

	robotstate "robot/internal/capability/robotstate"
	s4a21backend "robot/internal/composition/backend/s4a21"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/scheduler"
	"robot/internal/shared"
)

type backendTransportBundle struct {
	actions  scheduler.BackendActionTransport
	sessions scheduler.BackendSessionTransport
	close    func() error
}

func openBackendRobotState(info shared.BackendInfo, paths layout.Paths) (*robotstate.FileStore, error) {
	switch info.ID {
	case shared.BackendNative:
		return nil, nil
	case shared.BackendS4A21:
		if !paths.Valid() {
			return nil, fmt.Errorf("S4A21 robot state requires a valid runtime layout")
		}
		return robotstate.OpenFileStore(filepath.Join(paths.State, "robot_state.json"))
	default:
		return nil, fmt.Errorf("backend %s has no robot state store", info.ID)
	}
}

func composeBackendTransports(info shared.BackendInfo, cfg *config.SysConfig) (backendTransportBundle, error) {
	if cfg == nil {
		return backendTransportBundle{}, fmt.Errorf("backend transport requires config")
	}
	switch info.ID {
	case shared.BackendNative:
		return backendTransportBundle{close: func() error { return nil }}, nil
	case shared.BackendS4A21:
		if cfg.RobotConnectIP == "" || cfg.RobotGamePort <= 0 {
			return backendTransportBundle{}, fmt.Errorf("S4A21 game address is incomplete")
		}
		factory := s4a21backend.SessionFactory{Address: net.JoinHostPort(cfg.RobotConnectIP, fmt.Sprint(cfg.RobotGamePort))}
		transport := s4a21backend.NewActionTransport(factory)
		return backendTransportBundle{
			actions: transport, sessions: transport,
			close: transport.CloseAll,
		}, nil
	default:
		return backendTransportBundle{}, fmt.Errorf("backend %s has no transport composition", info.ID)
	}
}
