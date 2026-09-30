package cn90

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"robot/internal/capability/catalog"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

// RuntimeBundle holds the assembled 90CN runtime components. The composition
// root installs them on the scheduler through the backend-neutral ports.
type RuntimeBundle struct {
	Inventory      StartupInventory
	State          *robotstate.MemoryStore
	Loadouts       *SQLiteLoadoutApplier
	Transport      *ActionTransport
	Creator        RobotCreator
	Cleaner        RobotCleaner
	Purger         SQLiteRobotPurger
	Inspector      SQLitePopulationInspector
	DatabasePath   string
	GameAddress    string
	GamePort       int
	AdminAddress   string
	AdminToken     string
	TownMaps       []shared.MapCatalogItem
	FollowAccounts FollowAccountLocator
	NoticeStock    ServerNoticeStock
}

// Close releases the adapter-owned resources in shutdown order.
func (b RuntimeBundle) Close() error {
	var err error
	if b.Loadouts != nil {
		err = errors.Join(err, b.Loadouts.Close())
	}
	if b.Transport != nil {
		err = errors.Join(err, b.Transport.CloseAll())
	}
	return err
}

// RuntimeComposeOptions carries the backend-neutral inputs needed to assemble
// the 90CN runtime. GamePort 0 means "resolve the channel port from the DNF90
// runtime instance"; a positive value is an operator override.
type RuntimeComposeOptions struct {
	ServerDirectory string
	DatabasePath    string
	AccountPrefix   string
	ConnectIP       string
	GamePort        int
	Paths           layout.Paths
	Config          robotconfig.RuntimeConfig
	RandIntn        func(int) int
	RandBetween     func(int, int) int
}

// ComposeRuntime builds every 90CN runtime component: PVF catalogs, the
// database path and startup inventory, the persistence applier, the protocol
// transport and the lifecycle ports. Path and protocol rules stay in this
// adapter; the composition root only wires the returned components.
func ComposeRuntime(ctx context.Context, opts RuntimeComposeOptions) (RuntimeBundle, error) {
	bundle := RuntimeBundle{}
	prefix := strings.TrimSpace(opts.AccountPrefix)
	if prefix == "" {
		return bundle, fmt.Errorf("90CN account prefix is required")
	}
	if strings.TrimSpace(opts.ConnectIP) == "" {
		return bundle, fmt.Errorf("90CN game address is incomplete")
	}
	if opts.RandIntn == nil || opts.RandBetween == nil {
		return bundle, fmt.Errorf("90CN runtime requires random sources")
	}

	stageStarted := time.Now()
	logStartupStage := func(stage string) {
		foundationlog.Robotf("CN90_STARTUP_STAGE stage=%s elapsed_ms=%d\n", stage, time.Since(stageStarted).Milliseconds())
		stageStarted = time.Now()
	}

	serverLayout, err := resolveRuntimeLayout(opts.ServerDirectory)
	if err != nil {
		return bundle, fmt.Errorf("90CN runtime: %w", err)
	}
	pvfPath, pvfSource, err := serverLayout.pvfPath()
	if err != nil {
		return bundle, fmt.Errorf("PVF: %w", err)
	}
	foundationlog.Robotf("CN90_PVF_RESOLVED path=%s source=%s\n", pvfPath, pvfSource)
	logStartupStage("pvf_resolve")
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		return bundle, fmt.Errorf("PVF catalog: %w", err)
	}
	logStartupStage("pvf_catalog")
	if len(catalogs.StatFallbackJobs) > 0 {
		foundationlog.Robotf("CN90_STAT_FALLBACK jobs=%v\n", catalogs.StatFallbackJobs)
	}
	if err := ExportItemCatalogs(opts.Paths, catalogs.Equipment, catalogs.Stackable); err != nil {
		return bundle, fmt.Errorf("item catalog: %w", err)
	}
	if err := ExportTownMapCatalog(opts.Paths, catalogs.TownMaps); err != nil {
		return bundle, fmt.Errorf("town map catalog: %w", err)
	}
	logStartupStage("item_catalog_export")
	databasePath, databaseSource, err := resolveDatabasePath(serverLayout, opts.DatabasePath)
	if err != nil {
		return bundle, fmt.Errorf("loadout database: %w", err)
	}
	foundationlog.Robotf("CN90_DATABASE_RESOLVED path=%s source=%s\n", databasePath, databaseSource)
	gamePort, err := resolveGamePort(serverLayout, opts.GamePort)
	if err != nil {
		return bundle, fmt.Errorf("90CN channel port: %w", err)
	}
	adminAddress, adminToken, err := serverLayout.adminEndpoint()
	if err != nil {
		return bundle, fmt.Errorf("90CN admin endpoint: %w", err)
	}
	foundationlog.Robotf("CN90_CHANNEL_RESOLVED address=%s source=%s\n",
		net.JoinHostPort(opts.ConnectIP, fmt.Sprint(gamePort)), gamePortSource(opts.GamePort))
	inventory, err := (SQLiteStartupInventory{
		DatabasePath: databasePath, AccountPrefix: prefix, Config: opts.Config, Equipment: catalogs.Equipment,
		JobGrows: catalogs.JobGrows, StatTables: catalogs.StatTables,
	}).ScanAndClean(ctx)
	if err != nil {
		return bundle, fmt.Errorf("startup inventory: %w", err)
	}
	logStartupStage("startup_inventory")
	reconciled, err := ReconcileRobotGrowth(ctx, databasePath, inventory.Robots, opts.Config.GrowTypes, catalogs.JobGrows, catalogs.StatTables, opts.Config.ReconcileAwakening, opts.RandIntn)
	if err != nil {
		return bundle, fmt.Errorf("growth reconcile: %w", err)
	}
	logStartupStage("growth_reconcile")
	if reconciled > 0 {
		foundationlog.Robotf("CN90_GROWTH_RECONCILED count=%d\n", reconciled)
	}
	seeded, err := SeedRobotQuestGates(ctx, databasePath, inventory.Robots, catalogs.QuestGates)
	if err != nil {
		return bundle, fmt.Errorf("quest gates: %w", err)
	}
	logStartupStage("quest_gates")
	if seeded > 0 {
		foundationlog.Robotf("CN90_QUEST_GATES_SEEDED completed=%d active=%d robots=%d\n",
			len(catalogs.QuestGates.CompletedQuestIDs), len(catalogs.QuestGates.ActiveQuestIDs), seeded)
	}
	state := robotstate.NewMemoryStore(inventory.Robots)
	if err := state.RegisterIdentities(ctx, inventory.Identities); err != nil {
		return bundle, fmt.Errorf("startup identities: %w", err)
	}
	logStartupStage("state_identities")
	loadouts, err := NewSQLiteLoadoutApplier(ctx, databasePath, opts.Config, catalogs.Equipment, opts.RandIntn)
	if err != nil {
		return bundle, fmt.Errorf("loadout applier: %w", err)
	}
	logStartupStage("loadout_applier")
	loadouts.QuestGates = catalogs.QuestGates
	loadouts.StatTables = catalogs.StatTables
	loadouts.LevelThresholds = catalogs.LevelThresholds
	replaced, err := loadouts.ReconcileRobotLoadouts(ctx, prefix, inventory.Robots)
	if err != nil {
		_ = loadouts.Close()
		return bundle, fmt.Errorf("loadout reconcile: %w", err)
	}
	logStartupStage("loadout_reconcile")
	if replaced > 0 {
		foundationlog.Robotf("CN90_LOADOUT_RECONCILED robots=%d\n", replaced)
	}
	transport, err := NewRuntimeTransport(opts.ConnectIP, gamePort)
	if err != nil {
		_ = loadouts.Close()
		return bundle, fmt.Errorf("transport: %w", err)
	}
	noticeStock := NewServerNoticeStock(databasePath, catalogs.Equipment)
	transport.SetServerNoticeStock(noticeStock)
	logStartupStage("transport")
	address := net.JoinHostPort(opts.ConnectIP, fmt.Sprint(gamePort))
	names := catalog.NameTemplates(opts.Paths.Templates)

	bundle.Inventory = inventory
	bundle.State = state
	bundle.Loadouts = loadouts
	bundle.Transport = transport
	bundle.DatabasePath = databasePath
	bundle.GameAddress = address
	bundle.GamePort = gamePort
	bundle.AdminAddress = adminAddress
	bundle.AdminToken = adminToken
	bundle.TownMaps = catalogs.TownMaps
	bundle.FollowAccounts = FollowAccountLocator{DatabasePath: databasePath}
	bundle.NoticeStock = noticeStock
	bundle.Creator = RobotCreator{
		Provisioner: Provisioner{Address: address},
		BatchStore:  state, IdentityStore: state, RobotCatalog: state,
		Config: opts.Config, Names: names, Maps: catalogs.TownMaps, JobGrows: catalogs.JobGrows,
		AccountPrefix: prefix, IDStart: opts.Config.RobotUIDStart,
		RandIntn: opts.RandIntn, RandBetween: opts.RandBetween,
		Loadouts: loadouts, Profiles: loadouts,
	}
	bundle.Cleaner = RobotCleaner{
		Protocol: CharacterDeleter{Address: address},
		State:    state, Sessions: transport,
	}
	bundle.Purger = SQLiteRobotPurger{
		DatabasePath: databasePath, AccountPrefix: prefix, State: state, Sessions: transport,
	}
	bundle.Inspector = SQLitePopulationInspector{
		DatabasePath: databasePath, AccountPrefix: prefix, Config: opts.Config,
		Equipment: catalogs.Equipment, Maps: catalogs.TownMaps,
	}
	return bundle, nil
}

// NewRuntimeTransport dials the 90CN game server for both actions and session
// lifecycle. The same transport implements both scheduler ports.
func NewRuntimeTransport(connectIP string, gamePort int) (*ActionTransport, error) {
	if strings.TrimSpace(connectIP) == "" || gamePort <= 0 {
		return nil, fmt.Errorf("90CN game address is incomplete")
	}
	factory := SessionFactory{Address: net.JoinHostPort(connectIP, fmt.Sprint(gamePort))}
	return NewActionTransport(factory), nil
}

// ExportItemCatalogs publishes the projected equipment and stackable catalogs
// for other Robot stages that read them from disk.
func ExportItemCatalogs(paths layout.Paths, equipment, stackable []shared.EquipmentCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFEquipment(), equipment); err != nil {
		return fmt.Errorf("write 90CN equipment catalog: %w", err)
	}
	if err := capabilitypvf.WriteJSON(paths.PVFStackable(), stackable); err != nil {
		return fmt.Errorf("write 90CN stackable catalog: %w", err)
	}
	return nil
}

// ExportTownMapCatalog publishes the projected town map catalog. The shared
// store point coordinator reads it from disk and validates its MD5 against the
// generated point cache.
func ExportTownMapCatalog(paths layout.Paths, maps []shared.MapCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFMaps(), maps); err != nil {
		return fmt.Errorf("write 90CN map catalog: %w", err)
	}
	return nil
}

// ResolvePVFPath resolves the Script.pvf the DNF90 runtime reads: the instance
// game.pvfPath (relative to the runtime directory), defaulting to the locked
// data/dnf/Script.pvf profile path. The server directory may be the one-click
// project root or the runtime directory itself.
func ResolvePVFPath(serverDirectory string) (string, error) {
	serverLayout, err := resolveRuntimeLayout(serverDirectory)
	if err != nil {
		return "", err
	}
	path, _, err := serverLayout.pvfPath()
	return path, err
}

// ResolveDatabasePath resolves the SQLite database the robot writes: the
// explicit adapter setting wins (absolute, or relative to the runtime
// directory), then the instance database.path (relative to the runtime
// directory), then the locked data/dnf90.db default.
func ResolveDatabasePath(serverDirectory, configured string) (string, error) {
	serverLayout, err := resolveRuntimeLayout(serverDirectory)
	if err != nil {
		return "", err
	}
	path, _, err := resolveDatabasePath(serverLayout, configured)
	return path, err
}

// resolveDatabasePath also reports which rule selected the database file so
// startup can log the exact database source.
func resolveDatabasePath(serverLayout runtimeLayout, configured string) (string, string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		candidate := value
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(serverLayout.root, candidate)
		}
		if !isRegularFile(candidate) {
			return "", "", fmt.Errorf("90CN database %q does not exist", candidate)
		}
		return candidate, "adapter database_path setting", nil
	}
	path, source, err := serverLayout.databasePath()
	if err != nil {
		return "", "", err
	}
	if !isRegularFile(path) {
		return "", "", fmt.Errorf("90CN database %q does not exist; start the DNF90 server once so it creates the SQLite file", path)
	}
	return path, source, nil
}

// resolveGamePort returns the operator override when one is configured,
// otherwise the channel port from the runtime instance. The DNF90 channel
// listen address is loopback-locked by the server itself.
func resolveGamePort(serverLayout runtimeLayout, configured int) (int, error) {
	if configured > 0 {
		if configured > 65535 {
			return 0, fmt.Errorf("game port %d is out of range", configured)
		}
		return configured, nil
	}
	return serverLayout.channelPort()
}

func gamePortSource(configured int) string {
	if configured > 0 {
		return "adapter game_port setting"
	}
	return "instance server.channelListen"
}

func isRegularFile(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}
