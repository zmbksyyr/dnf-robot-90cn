package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/capability/catalog"
	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	backendregistry "robot/internal/composition/backend"
	s4a21backend "robot/internal/composition/backend/s4a21"
	"robot/internal/entry/tcpapi"
	"robot/internal/entry/webadmin"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/foundation/network"
	"robot/internal/scheduler"
	"robot/internal/shared"
)

// runS4A21Backend is deliberately separate from native startup. It does
// not open MySQL, initialize native RSA/party services, or construct market
// and mail adapters that require native tables.
func runS4A21Backend(cfg *config.SysConfig, paths layout.Paths, info shared.BackendInfo, selection shared.BackendSelection) int {
	if err := runtimeinit.InitConfigForBackend(cfg, info); err != nil {
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
	if err := exportBackendItemCatalogs(info, cfg, paths); err != nil {
		foundationlog.Robotf("SIMULATOR_ITEM_CATALOG_FAILED err=%v\n", err)
		return 1
	}
	loadoutDB, err := s4a21DatabasePath(cfg.DFGameR, backendSetting(selection, "database_path"))
	if err != nil {
		foundationlog.Robotf("SIMULATOR_LOADOUT_DATABASE_FAILED err=%v\n", err)
		return 1
	}
	// Do not mark the generation as applied until simulator-specific
	// initialization (including transport composition and PVF projection) has
	// succeeded. A failed startup must retry the reinitialization next time.
	if err := runtimeinit.MarkBackendRuntimeApplied(paths, selection); err != nil {
		foundationlog.Robotf("SIMULATOR_RUNTIME_MARK_FAILED err=%v\n", err)
		return 1
	}
	manager := scheduler.NewRobotManager(nil, cfg, nil)
	manager.ConfigureBackendRuntime(info, s4a21backend.PersistenceInspector{DatabasePath: loadoutDB}, nil)
	manager.SetBackendRobotCreator(info, nil)
	manager.SetRobotStateDirectory(state)
	defer func() {
		if err := state.Flush(); err != nil { foundationlog.Robotf("SIMULATOR_STATE_FLUSH_FAILED err=%v\n", err) }
	}()
	manager.SetBackendActionTransport(transports.actions)
	manager.SetBackendSessionTransport(transports.sessions)
	manager.SetTownMapCatalog(townMaps)
	nameTemplates := catalog.NameTemplates(paths.Templates)
	loadouts := s4a21backend.SQLiteLoadoutApplier{
		DatabasePath: loadoutDB, Config: rc, Equipment: catalog.ViewItemCatalogs(paths.PVF).Equipment, RandIntn: manager.RandIntn,
	}
	if err := reconcileSimulatorLoadouts(context.Background(), state, loadouts); err != nil {
		foundationlog.Robotf("SIMULATOR_LOADOUT_RECONCILE_FAILED err=%v\n", err)
		return 1
	}
	manager.SetBackendRobotCreator(info, s4a21backend.RobotCreator{
		Provisioner: s4a21backend.Provisioner{Address: fmt.Sprintf("%s:%d", cfg.RobotConnectIP, cfg.RobotGamePort)},
		BatchStore:  state, IdentityStore: state, RobotCatalog: state, Config: rc, Names: nameTemplates, Maps: townMaps,
		AccountPrefix: "robot", IDStart: rc.RobotUIDStart,
		RandIntn: manager.RandIntn, RandBetween: manager.RandBetween,
		Loadouts: loadouts, Profiles: loadouts,
	})
	manager.SetBackendRobotCleaner(s4a21backend.RobotCleaner{
		Protocol: s4a21backend.CharacterDeleter{Address: fmt.Sprintf("%s:%d", cfg.RobotConnectIP, cfg.RobotGamePort)},
		State:    state, Sessions: transports.sessions,
	})
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
	webCtx, webCancel := context.WithCancel(context.Background())
	webServer := webadmin.NewWithCatalog(cfg, fmt.Sprintf("127.0.0.1:%d", cfg.RobotPort), fmt.Sprintf("0.0.0.0:%d", cfg.WebPort), info.ID, backendregistry.Available())
	webDone := make(chan error, 1)
	go func() { webDone <- webServer.Serve(webCtx) }()
	defer func() {
		webCancel()
		select {
		case err := <-webDone:
			if err != nil {
				foundationlog.Robotf("WEB_SERVER_STOP_FAILED err=%v\n", err)
			}
		case <-time.After(6 * time.Second):
			foundationlog.Robotf("WEB_SERVER_STOP_TIMEOUT\n")
		}
	}()
	manager.StartAutoActions()
	foundationlog.Robotf("SIMULATOR_STARTED backend=%s tcp=%s\n", info.ID, addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh
	foundationlog.Robotf("SIMULATOR_STOPPING backend=%s\n", info.ID)
	return 0
}

func backendSetting(selection shared.BackendSelection, key string) string {
	return selection.Settings[key]
}

func reconcileSimulatorLoadouts(ctx context.Context, state *robotstate.FileStore, applier s4a21backend.CharacterLoadoutApplier) error {
	robots, err := state.SelectRobots(ctx, robotcap.CommandRequest{Count: 1 << 30})
	if err != nil {
		return err
	}
	identities, err := state.Identities(ctx, shared.BackendS4A21)
	if err != nil {
		return err
	}
	accounts := make(map[string]string, len(identities))
	for _, identity := range identities {
		accounts[identity.CharacterName] = identity.Account
	}
	profiles, canResolve := applier.(s4a21backend.CharacterProfileReader)
	profileAdapter, canReconcileLevel := applier.(s4a21backend.CharacterProfileAdapter)
	updates := make([]robotcap.Info, 0, len(robots))
	for _, robot := range robots {
		account := accounts[robot.Name]
		if account == "" {
			return fmt.Errorf("S4A21 robot %d/%s has no account identity", robot.UID, robot.Name)
		}
		if canReconcileLevel {
			robot, err = profileAdapter.ReconcileConfiguredCharacterLevel(ctx, account, robot)
			if err != nil {
				return fmt.Errorf("reconcile S4A21 level uid=%d: %w", robot.UID, err)
			}
			updates = append(updates, robot)
		} else if canResolve {
			robot, err = profiles.ResolveCharacterProfile(ctx, account, robot)
			if err != nil {
				return fmt.Errorf("resolve S4A21 profile uid=%d: %w", robot.UID, err)
			}
			updates = append(updates, robot)
		}
		if err := applier.ApplyCharacterLoadout(ctx, account, robot); err != nil {
			return fmt.Errorf("reconcile S4A21 loadout uid=%d: %w", robot.UID, err)
		}
	}
	if len(updates) > 0 {
		if err := state.UpdateRobotProfiles(ctx, updates); err != nil {
			return fmt.Errorf("persist S4A21 profiles: %w", err)
		}
	}
	return nil
}

func ensureSimulatorOpenFileLimit(rc robotconfig.RuntimeConfig) error {
	if rc.MaxOnlineRobots < 1 {
		return fmt.Errorf("max_online_robots must be positive")
	}
	return nil
}
