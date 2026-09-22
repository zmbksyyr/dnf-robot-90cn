package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/capability/keypair"
	"robot/internal/capability/mailnotify"
	"robot/internal/capability/marketapp"
	"robot/internal/capability/robotconfig"
	"robot/internal/composition/auctionapp"
	"robot/internal/entry/tcpapi"
	"robot/internal/entry/webadmin"
	"robot/internal/foundation/config"
	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/foundation/network"
	"robot/internal/foundation/process"
	"robot/internal/protocol/dnf"
	"robot/internal/protocol/dnfruntime"
	"robot/internal/protocol/monitor"
	"robot/internal/protocol/nocache"
	"robot/internal/scheduler"
	schedulerrepo "robot/internal/scheduler/repository"
	"robot/internal/shared"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	webAdminMode := flag.Bool("web-admin", false, "run web admin child process")
	robotAddr := flag.String("robot-addr", "", "robot TCP address for web admin")
	webAddr := flag.String("web-addr", "", "web admin listen address")
	webConfigStdin := flag.Bool("web-config-stdin", false, "read the parent runtime config snapshot from stdin")
	flag.Parse()
	if boundedLogSinkRequested() {
		if err := runBoundedLogSink(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "bounded log sink failed: %v\n", err)
			return 1
		}
		return 0
	}

	if *webAdminMode {
		if err := runWebAdmin(*robotAddr, *webAddr, *webConfigStdin); err != nil {
			fmt.Fprintf(os.Stderr, "web admin failed: %v\n", err)
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
	backendInfo, err := shared.SelectBackend(backendSelection.BackendID, runtime.GOOS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend selection error: %v\n", err)
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
	dnf.LogString(fmt.Sprintf("NETWORK_CONFIG game=%s:%d setting=%s login_ip=%s relay=%s:%d auction=%s:%d point=%s:%d service_root=%s run_script=%s\n",
		cfg.RobotConnectIP, cfg.RobotGamePort, cfg.RobotConnectIPSetting, cfg.RobotInnerIP,
		cfg.RelayHost, cfg.RelayPort, cfg.AuctionHost, cfg.AuctionPort, cfg.PointHost, cfg.PointPort, cfg.ServiceRoot, cfg.ServiceRunScript))

	if err := runtimeinit.Init(cfg); err != nil {
		dnf.LogString(fmt.Sprintf("ROBOT_RUNTIME_INIT_FAILED err=%v\n", err))
		dnf.PrintfRed("runtime init failed: %v\n", err)
		return 1
	}
	robotRuntimeConfig, err := loadRequiredRobotConfig(paths.RobotConfig())
	if err != nil {
		dnf.LogString(fmt.Sprintf("ROBOT_RUNTIME_CONFIG_LOAD_FAILED err=%v\n", err))
		dnf.PrintfRed("load robot runtime config failed: %v\n", err)
		return 1
	}
	if err := process.EnsureOpenFileLimit(robotRuntimeConfig.MaxOnlineRobots, cfg.DBMaxSize); err != nil {
		dnf.LogString(fmt.Sprintf("OPEN_FILE_CAPACITY_FAILED err=%v\n", err))
		dnf.PrintfRed("open file capacity check failed: %v\n", err)
		return 1
	}
	dnf.ConfigurePartyRelayHost(cfg.RelayHost)
	dnf.ConfigurePartyRelayPort(cfg.RelayPort)
	route0Sink, err := dnf.StartPartyRoute0Sink(cfg.PartyRoute0Port)
	if err != nil {
		dnf.LogString(fmt.Sprintf("PARTY_ROUTE0_SINK_FAILED addr=0.0.0.0:%d err=%v\n", cfg.PartyRoute0Port, err))
		dnf.PrintfRed("party route0 sink failed: %v\n", err)
		return 1
	}
	defer route0Sink.Close()
	dnf.LogString(fmt.Sprintf("PARTY_ROUTE0_SINK_READY addr=0.0.0.0:%d\n", cfg.PartyRoute0Port))
	dnf.ConfigurePartyRobotAccountRange(robotRuntimeConfig.RobotUIDStart, robotRuntimeConfig.RobotUIDEnd)
	dnf.LogString(fmt.Sprintf("PARTY_ACCOUNT_RANGE start=%d end=%d\n", robotRuntimeConfig.RobotUIDStart, robotRuntimeConfig.RobotUIDEnd))
	keypair.SetRuntimeKeySink(dnf.SetRSAKey)

	initRSA(cfg)
	defer keypair.ClosePrivateKey()

	db, err := openDatabase(cfg)
	if err != nil {
		dnf.PrintfRed("database open failed: %v\n", err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			dnf.LogString(fmt.Sprintf("DATABASE_CLOSE_FAILED err=%v\n", err))
			dnf.PrintfRed("database close error: %v\n", err)
		}
	}()
	dnf.SetDBPool(db)
	defer dnf.SetDBPool(nil)

	robotSvc := dnfruntime.NewRobotService()
	defer robotSvc.Shutdown()
	manager := scheduler.NewRobotManager(schedulerrepo.NewSQLRepository(db), cfg, robotSvc)
	defer func() {
		if err := manager.Shutdown(); err != nil {
			dnf.LogString(fmt.Sprintf("ROBOT_MANAGER_SHUTDOWN_FAILED err=%v\n", err))
			dnf.PrintfRed("robot manager shutdown error: %v\n", err)
		}
	}()
	transportBundle, err := composeBackendTransports(backendInfo, cfg)
	if err != nil {
		dnf.LogString(fmt.Sprintf("BACKEND_TRANSPORT_COMPOSE_FAILED backend=%s err=%v\n", backendInfo.ID, err))
		dnf.PrintfRed("backend transport compose failed: %v\n", err)
		return 1
	}
	defer transportBundle.close()
	manager.SetBackendActionTransport(transportBundle.actions)
	manager.SetBackendSessionTransport(transportBundle.sessions)
	townMaps, err := loadBackendTownMapCatalog(context.Background(), backendInfo, cfg)
	if err != nil {
		dnf.LogString(fmt.Sprintf("BACKEND_TOWN_MAP_CATALOG_FAILED backend=%s err=%v\n", backendInfo.ID, err))
		dnf.PrintfRed("backend town map catalog failed: %v\n", err)
		return 1
	}
	manager.SetTownMapCatalog(townMaps)
	manager.SetPartyAccountRangeSink(dnf.ConfigurePartyRobotAccountRange)
	cacheInvalidator, err := nocache.NewClient(cfg.RobotConnectIP, cfg.RobotGamePort, cfg.GameServerGroup)
	if err != nil {
		dnf.LogString(fmt.Sprintf("CACHE_INVALIDATOR_INIT_FAILED err=%v\n", err))
		dnf.PrintfRed("cache invalidator init failed: %v\n", err)
		return 1
	}
	manager.SetCharacterCacheInvalidator(cacheInvalidator)
	monitorClient := &monitor.Client{Address: fmt.Sprintf("127.0.0.1:%d", cfg.MonitorPort)}
	manager.SetWorldShout(monitorClient)
	mailNotifier := mailnotify.New(db, monitorClient, paths.State)
	manager.SetMailNotifier(mailNotifier)
	marketApp, err := marketapp.New(db, cfg, auctionapp.NewFactory())
	if err != nil {
		dnf.LogString(fmt.Sprintf("MARKET_INIT_FAILED err=%v\n", err))
		dnf.PrintfRed("market init failed: %v\n", err)
		return 1
	}
	tcpapi.SetMarketApp(marketApp)
	runtimeFiles := filewatch.New(time.Second, append(manager.RuntimeFileEntries(), marketApp.RuntimeFileEntries()...), func(entry filewatch.Entry, err error) {
		dnf.LogString(fmt.Sprintf("RUNTIME_FILE_REJECTED name=%s path=%s err=%v\n", entry.Name, entry.Path, err))
	})
	runtimeFiles.Start()
	defer func() {
		runtimeFiles.Close()
		marketApp.Shutdown()
	}()
	if _, err := manager.RepairRobotEquipment(); err != nil {
		dnf.LogString(fmt.Sprintf("EQUIPMENT_REPAIR_FAILED err=%v\n", err))
	}

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.RobotPort)
	tcpServer := network.NewTCPServer(addr)
	tcpServer.SetLimits(256, 90*time.Second, 15*time.Second)
	tcpServer.OnMessage(func(clientID string, raw []byte) {
		response := tcpapi.HandlePacket(clientID, string(raw), manager)
		if response != "" {
			if err := tcpServer.SendTo(clientID, []byte(response)); err != nil {
				dnf.LogString(fmt.Sprintf("TCP_RESPONSE_WRITE_FAILED client=%s err=%v\n", clientID, err))
			}
		}
	})
	if err := tcpServer.Start(); err != nil {
		dnf.LogString(fmt.Sprintf("TCP_SERVER_START_FAILED addr=%s err=%v\n", addr, err))
		dnf.PrintfRed("TCP server failed: %v\n", err)
		return 1
	}
	defer func() {
		if err := tcpServer.Close(); err != nil {
			dnf.LogString(fmt.Sprintf("TCP_SERVER_CLOSE_FAILED err=%v\n", err))
			dnf.PrintfRed("tcp server close error: %v\n", err)
		}
	}()
	logRobotActionf("TCP server listening on %s\n", addr)
	stopWebAdmin := webadmin.StartSupervisor(cfg)
	defer stopWebAdmin()
	manager.StartAutoActions()
	if marketApp.Config().Auto.Enabled {
		marketApp.StartAuto()
	}
	logRobotActionf("robot started\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh

	logRobotActionf("robot stopping...\n")
	return 0
}

func loadBackendSelection(path string) (shared.BackendSelection, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return shared.BackendSelection{BackendID: shared.BackendNative}, nil
	}
	if err != nil {
		return shared.BackendSelection{}, err
	}
	return shared.DecodeBackendSelection(data)
}

func runWebAdmin(robotAddr, webAddr string, configFromStdin bool) error {
	var cfg *config.SysConfig
	if configFromStdin {
		cfg = &config.SysConfig{}
		if err := config.DecodeJSONLimit(os.Stdin, 1<<20, cfg); err != nil {
			return fmt.Errorf("decode parent runtime config snapshot: %w", err)
		}
	} else {
		configPath, configDir, err := runtimeConfigPaths()
		if err != nil {
			return fmt.Errorf("resolve config path: %w", err)
		}
		loaded, err := config.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		loaded.ConfigDir = configDir
		cfg = loaded
	}
	if robotAddr == "" {
		robotAddr = fmt.Sprintf("127.0.0.1:%d", cfg.RobotPort)
	}
	if webAddr == "" {
		webAddr = fmt.Sprintf("0.0.0.0:%d", cfg.WebPort)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := webadmin.New(cfg, robotAddr, webAddr).Serve(ctx); err != nil {
		return err
	}
	return nil
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

func initRSA(cfg *config.SysConfig) {
	st := keypair.BuildKeypairStatus(cfg)
	if !st.GameValid {
		dnf.LogString(fmt.Sprintf("KEYPAIR_RSA_LOAD_BLOCKED state=%s reason=%s err=%s\n", st.KeyState, st.KeyReason, st.Error))
		dnf.PrintfBlue("WARNING: game RSA key is not valid. Robot business commands are blocked until a valid key is configured or default key is released.\n")
		return
	}
	path := layout.New(cfg.ConfigDir).PrivateKey()
	if err := keypair.InitPrivateKey(path); err == nil {
		dnf.SetRSAKey(keypair.GetRSAKey())
		dnf.LogString(fmt.Sprintf("KEYPAIR_RSA_LOADED source=%s state=%s fingerprint=%s\n", path, st.KeyState, st.Fingerprint))
		dnf.PrintfGreen("loaded RSA private key from %s\n", path)
		return
	}
	dnf.LogString(fmt.Sprintf("KEYPAIR_RSA_LOAD_FAILED source=%s\n", path))
	dnf.PrintfBlue("WARNING: privatekey.pem not found in config directory. Robot login tokens cannot be generated - ALL robots will fail authentication.\n")
}

func logRobotActionf(format string, args ...interface{}) {
	foundationlog.Robotf(format, args...)
}
