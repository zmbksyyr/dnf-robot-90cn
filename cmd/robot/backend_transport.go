package main

import (
	"fmt"
	"net"

	s4a21backend "robot/internal/composition/backend/s4a21"
	"robot/internal/foundation/config"
	"robot/internal/scheduler"
	"robot/internal/shared"
)

type backendTransportBundle struct {
	actions  scheduler.BackendActionTransport
	sessions scheduler.BackendSessionTransport
	close    func() error
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
