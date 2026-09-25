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

// runBackend owns the selected adapter's complete initialization, dispatch,
// and shutdown lifecycle. Shared scheduling remains below the composition
// boundary; version-specific work stays in the selected adapter.
func runBackend(cfg *config.SysConfig, paths layout.Paths, info shared.BackendInfo, selection shared.BackendSelection) int {
	if err := runtimeinit.InitConfigForBackend(cfg, info); err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_INIT_FAILED backend=%s err=%v\n", info.ID, err)
		return 1
	}
	rc, err := loadRequiredRobotConfig(paths.RobotConfig())
	if err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_CONFIG_FAILED err=%v\n", err)
		return 1
	}
	if err := ensureAdapterOpenFileLimit(rc); err != nil {
		foundationlog.Robotf("ADAPTER_OPEN_FILE_CAPACITY_FAILED err=%v\n", err)
		return 1
	}
	transports, err := composeBackendTransports(info, cfg)
	if err != nil {
		foundationlog.Robotf("ADAPTER_TRANSPORT_FAILED err=%v\n", err)
		return 1
	}
	defer transports.close()
	pvfPath, err := s4a21PVFPath(cfg.DFGameR)
	if err != nil {
		foundationlog.Robotf("ADAPTER_TOWN_MAP_FAILED err=%v\n", err)
		return 1
	}
	catalogs, err := s4a21backend.ReadCatalogs(pvfPath)
	if err != nil {
		foundationlog.Robotf("ADAPTER_PVF_CATALOG_FAILED err=%v\n", err)
		return 1
	}
	townMaps := catalogs.TownMaps
	if err := exportItemCatalogs(paths, catalogs.Equipment, catalogs.Stackable); err != nil {
		foundationlog.Robotf("ADAPTER_ITEM_CATALOG_FAILED err=%v\n", err)
		return 1
	}
	loadoutDB, err := s4a21DatabasePath(cfg.DFGameR, backendSetting(selection, "database_path"))
	if err != nil {
		foundationlog.Robotf("ADAPTER_LOADOUT_DATABASE_FAILED err=%v\n", err)
		return 1
	}
	equipment := catalogs.Equipment
	inventory, err := (s4a21backend.SQLiteStartupInventory{
		DatabasePath: loadoutDB, AccountPrefix: "robot", Config: rc, Equipment: equipment,
	}).ScanAndClean(context.Background())
	if err != nil {
		foundationlog.Robotf("ADAPTER_STARTUP_INVENTORY_FAILED err=%v\n", err)
		return 1
	}
	state := robotstate.NewMemoryStore(inventory.Robots)
	if err := state.RegisterIdentities(context.Background(), inventory.Identities); err != nil {
		foundationlog.Robotf("ADAPTER_STARTUP_IDENTITIES_FAILED err=%v\n", err)
		return 1
	}
	foundationlog.Robotf("ADAPTER_STARTUP_INVENTORY scanned=%d adopted=%d deleted_accounts=%d deleted_characters=%d\n",
		inventory.ScannedAccounts, len(inventory.Robots), inventory.DeletedAccounts, inventory.DeletedCharacters)
	// Do not mark the generation as applied until adapter-specific
	// initialization (including transport composition and PVF projection) has
	// succeeded. A failed startup must retry the reinitialization next time.
	if err := runtimeinit.MarkBackendRuntimeApplied(paths, selection); err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_MARK_FAILED err=%v\n", err)
		return 1
	}
	manager := scheduler.NewRobotManager(nil, cfg, nil)
	manager.ConfigureBackendRuntime(info, s4a21backend.NewPersistenceInspector(loadoutDB), nil)
	manager.SetBackendRobotCreator(info, nil)
	manager.SetRobotStateDirectory(state)
	manager.SetBackendActionTransport(transports.actions)
	manager.SetBackendSessionTransport(transports.sessions)
	manager.SetTownMapCatalog(townMaps)
	nameTemplates := catalog.NameTemplates(paths.Templates)
	loadouts, err := s4a21backend.NewSQLiteLoadoutApplier(context.Background(), loadoutDB, rc, equipment, manager.RandIntn)
	if err != nil {
		foundationlog.Robotf("ADAPTER_LOADOUT_FAILED err=%v\n", err)
		return 1
	}
	defer loadouts.Close()
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
			foundationlog.Robotf("ADAPTER_MANAGER_SHUTDOWN_FAILED err=%v\n", err)
		}
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.RobotPort)
	tcpServer := network.NewTCPServer(addr)
	tcpServer.SetLimits(256, 90*time.Second, 15*time.Second)
	tcpServer.OnMessage(func(clientID string, raw []byte) {
		response := tcpapi.HandlePacket(clientID, string(raw), manager)
		if response != "" {
			if err := tcpServer.SendTo(clientID, []byte(response)); err != nil {
				foundationlog.Robotf("ADAPTER_TCP_RESPONSE_FAILED client=%s err=%v\n", clientID, err)
			}
		}
	})
	if err := tcpServer.Start(); err != nil {
		foundationlog.Robotf("ADAPTER_TCP_START_FAILED addr=%s err=%v\n", addr, err)
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
	foundationlog.Robotf("ROBOT_STARTED backend=%s tcp=%s\n", info.ID, addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh
	foundationlog.Robotf("ROBOT_STOPPING backend=%s\n", info.ID)
	return 0
}

func backendSetting(selection shared.BackendSelection, key string) string {
	return selection.Settings[key]
}

func ensureAdapterOpenFileLimit(rc robotconfig.RuntimeConfig) error {
	if rc.MaxOnlineRobots < 1 {
		return fmt.Errorf("max_online_robots must be positive")
	}
	return nil
}
