package s4a21

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
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

// RuntimeBundle holds the assembled S4A21 runtime components. The composition
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
	TownMaps       []shared.MapCatalogItem
	FollowAccounts FollowAccountLocator
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
// the S4A21 runtime.
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

// ComposeRuntime builds every S4A21 runtime component: PVF catalogs, the
// database path and startup inventory, the persistence applier, the protocol
// transport and the lifecycle ports. Path and protocol rules stay in this
// adapter; the composition root only wires the returned components.
func ComposeRuntime(ctx context.Context, opts RuntimeComposeOptions) (RuntimeBundle, error) {
	bundle := RuntimeBundle{}
	prefix := strings.TrimSpace(opts.AccountPrefix)
	if prefix == "" {
		return bundle, fmt.Errorf("S4A21 account prefix is required")
	}
	if opts.ConnectIP == "" || opts.GamePort <= 0 {
		return bundle, fmt.Errorf("S4A21 game address is incomplete")
	}
	if opts.RandIntn == nil || opts.RandBetween == nil {
		return bundle, fmt.Errorf("S4A21 runtime requires random sources")
	}

	stageStarted := time.Now()
	logStartupStage := func(stage string) {
		foundationlog.Robotf("S4A21_STARTUP_STAGE stage=%s elapsed_ms=%d\n", stage, time.Since(stageStarted).Milliseconds())
		stageStarted = time.Now()
	}

	pvfPath, pvfSource, err := resolvePVFPath(opts.ServerDirectory)
	if err != nil {
		return bundle, fmt.Errorf("PVF: %w", err)
	}
	foundationlog.Robotf("S4A21_PVF_RESOLVED path=%s source=%s\n", pvfPath, pvfSource)
	logStartupStage("pvf_resolve")
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		return bundle, fmt.Errorf("PVF catalog: %w", err)
	}
	logStartupStage("pvf_catalog")
	if len(catalogs.StatFallbackJobs) > 0 {
		foundationlog.Robotf("S4A21_STAT_FALLBACK jobs=%v\n", catalogs.StatFallbackJobs)
	}
	if err := ExportItemCatalogs(opts.Paths, catalogs.Equipment, catalogs.Stackable); err != nil {
		return bundle, fmt.Errorf("item catalog: %w", err)
	}
	if err := ExportTownMapCatalog(opts.Paths, catalogs.TownMaps); err != nil {
		return bundle, fmt.Errorf("town map catalog: %w", err)
	}
	logStartupStage("item_catalog_export")
	databasePath, databaseSource, err := resolveDatabasePath(opts.ServerDirectory, opts.DatabasePath)
	if err != nil {
		return bundle, fmt.Errorf("loadout database: %w", err)
	}
	foundationlog.Robotf("S4A21_DATABASE_RESOLVED path=%s source=%s\n", databasePath, databaseSource)
	logDatabaseSourceConflict(opts.ServerDirectory, opts.DatabasePath)
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
		foundationlog.Robotf("S4A21_GROWTH_RECONCILED count=%d\n", reconciled)
	}
	seeded, err := SeedRobotQuestGates(ctx, databasePath, inventory.Robots, catalogs.QuestGates)
	if err != nil {
		return bundle, fmt.Errorf("quest gates: %w", err)
	}
	logStartupStage("quest_gates")
	if seeded > 0 {
		foundationlog.Robotf("S4A21_QUEST_GATES_SEEDED completed=%d active=%d robots=%d\n",
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
		foundationlog.Robotf("S4A21_LOADOUT_RECONCILED robots=%d\n", replaced)
	}
	transport, err := NewRuntimeTransport(opts.ConnectIP, opts.GamePort)
	if err != nil {
		_ = loadouts.Close()
		return bundle, fmt.Errorf("transport: %w", err)
	}
	logStartupStage("transport")
	address := net.JoinHostPort(opts.ConnectIP, fmt.Sprint(opts.GamePort))
	names := catalog.NameTemplates(opts.Paths.Templates)

	bundle.Inventory = inventory
	bundle.State = state
	bundle.Loadouts = loadouts
	bundle.Transport = transport
	bundle.DatabasePath = databasePath
	bundle.TownMaps = catalogs.TownMaps
	bundle.FollowAccounts = FollowAccountLocator{DatabasePath: databasePath}
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

// NewRuntimeTransport dials the S4A21 game server for both actions and session
// lifecycle. The same transport implements both scheduler ports.
func NewRuntimeTransport(connectIP string, gamePort int) (*ActionTransport, error) {
	if strings.TrimSpace(connectIP) == "" || gamePort <= 0 {
		return nil, fmt.Errorf("S4A21 game address is incomplete")
	}
	factory := SessionFactory{Address: net.JoinHostPort(connectIP, fmt.Sprint(gamePort))}
	return NewActionTransport(factory), nil
}

// ExportItemCatalogs publishes the projected equipment and stackable catalogs
// for other Robot stages that read them from disk.
func ExportItemCatalogs(paths layout.Paths, equipment, stackable []shared.EquipmentCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFEquipment(), equipment); err != nil {
		return fmt.Errorf("write S4A21 equipment catalog: %w", err)
	}
	if err := capabilitypvf.WriteJSON(paths.PVFStackable(), stackable); err != nil {
		return fmt.Errorf("write S4A21 stackable catalog: %w", err)
	}
	return nil
}

// ExportTownMapCatalog publishes the projected town map catalog. The shared
// store point coordinator reads it from disk and validates its MD5 against the
// generated point cache.
func ExportTownMapCatalog(paths layout.Paths, maps []shared.MapCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFMaps(), maps); err != nil {
		return fmt.Errorf("write S4A21 map catalog: %w", err)
	}
	return nil
}

// pvfArchivePathEnv is the environment override ServerS4A21 itself reads when
// locating the PVF archive.
const pvfArchivePathEnv = "PVF_ARCHIVE_PATH"

// inventoryDatabasePathEnv is the environment override ServerS4A21 itself
// reads when locating the SQLite inventory database.
const inventoryDatabasePathEnv = "INVENTORY_DATABASE_PATH"

// ResolvePVFPath mirrors the S4A21 server's GameWorldConfig.PvfArchivePath
// resolution relative to the server base directory: PVF_ARCHIVE_PATH when that
// file exists, otherwise Data/Pvf/Script.pvf, otherwise the first *.pvf under
// Data/Pvf, otherwise the first *.pvf directly under the server base. The
// archive directory is never treated as the server base.
func ResolvePVFPath(serverDirectory string) (string, error) {
	path, _, err := resolvePVFPath(serverDirectory)
	return path, err
}

// resolvePVFPath also reports which rule selected the archive so startup can
// log the exact PVF source.
func resolvePVFPath(serverDirectory string) (string, string, error) {
	base, err := resolveServerBase(serverDirectory)
	if err != nil {
		return "", "", err
	}
	if override := strings.TrimSpace(os.Getenv(pvfArchivePathEnv)); override != "" {
		candidate := override
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(base, candidate)
		}
		if isRegularFile(candidate) {
			return candidate, "env " + pvfArchivePathEnv, nil
		}
	}
	if candidate := filepath.Join(base, "Data", "Pvf", "Script.pvf"); isRegularFile(candidate) {
		return candidate, "Data/Pvf/Script.pvf", nil
	}
	if candidate := firstPVFArchive(filepath.Join(base, "Data", "Pvf")); candidate != "" {
		return candidate, "first .pvf under Data/Pvf", nil
	}
	if candidate := firstPVFArchive(base); candidate != "" {
		return candidate, "first .pvf under the server directory", nil
	}
	return "", "", fmt.Errorf("S4A21 PVF not found under %q; expected Data/Pvf/Script.pvf or %s", base, pvfArchivePathEnv)
}

// ResolveDatabasePath mirrors the S4A21 server's ServerPaths.DatabasePath
// resolution: the explicit adapter setting wins, then INVENTORY_DATABASE_PATH
// (absolute, or relative to the server base), then Data/inventory.db under the
// server base. The PVF location never contributes to this path.
func ResolveDatabasePath(serverDirectory, configured string) (string, error) {
	path, _, err := resolveDatabasePath(serverDirectory, configured)
	return path, err
}

// resolveDatabasePath also reports which rule selected the database file so
// startup can log the exact database source.
func resolveDatabasePath(serverDirectory, configured string) (string, string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		if _, err := os.Stat(value); err != nil {
			return "", "", fmt.Errorf("S4A21 database %q: %w", value, err)
		}
		return value, "adapter database_path setting", nil
	}
	if override := strings.TrimSpace(os.Getenv(inventoryDatabasePathEnv)); override != "" {
		candidate := override
		if !filepath.IsAbs(candidate) {
			base, err := resolveServerBase(serverDirectory)
			if err != nil {
				return "", "", err
			}
			candidate = filepath.Join(base, candidate)
		}
		if _, err := os.Stat(candidate); err != nil {
			return "", "", fmt.Errorf("S4A21 database %q: %w", candidate, err)
		}
		return candidate, "env " + inventoryDatabasePathEnv, nil
	}
	base, err := resolveServerBase(serverDirectory)
	if err != nil {
		return "", "", err
	}
	candidate := filepath.Join(base, "Data", "inventory.db")
	if _, err := os.Stat(candidate); err != nil {
		return "", "", fmt.Errorf("S4A21 database %q: %w", candidate, err)
	}
	return candidate, "Data/inventory.db", nil
}

// logDatabaseSourceConflict warns when the adapter database setting and
// INVENTORY_DATABASE_PATH name different files. The adapter setting wins for
// the robot, but the game server itself opens the environment path, so both
// processes may silently use different databases.
func logDatabaseSourceConflict(serverDirectory, configured string) {
	configured = strings.TrimSpace(configured)
	override := strings.TrimSpace(os.Getenv(inventoryDatabasePathEnv))
	if configured == "" || override == "" {
		return
	}
	base, err := resolveServerBase(serverDirectory)
	if err != nil {
		return
	}
	if !filepath.IsAbs(override) {
		override = filepath.Join(base, override)
	}
	if samePath(configured, override) {
		return
	}
	foundationlog.Robotf("S4A21_DATABASE_SOURCE_CONFLICT setting=%s %s=%s effective=%s\n",
		configured, inventoryDatabasePathEnv, override, configured)
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	leftAbs = filepath.Clean(leftAbs)
	rightAbs = filepath.Clean(rightAbs)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}

// resolveServerBase maps the configured server directory, or the configured
// server executable, onto the directory ServerS4A21 uses as its base. A .pvf
// path cannot identify that base and is rejected instead of guessing.
func resolveServerBase(serverDirectory string) (string, error) {
	value := strings.TrimSpace(serverDirectory)
	if value == "" {
		return "", fmt.Errorf("S4A21 server directory is empty")
	}
	if strings.EqualFold(filepath.Ext(value), ".pvf") {
		return "", fmt.Errorf("S4A21 server directory %q is a .pvf file; configure the directory that contains the server executable", value)
	}
	switch stat, err := os.Stat(value); {
	case err == nil && stat.IsDir():
		return value, nil
	case err == nil:
		return filepath.Dir(value), nil
	case filepath.Ext(value) != "":
		return filepath.Dir(value), nil
	default:
		return value, nil
	}
}

func isRegularFile(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}

// firstPVFArchive mirrors the server's fallback of picking the first archive
// in a directory when Script.pvf is absent.
func firstPVFArchive(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pvf") {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		if isRegularFile(candidate) {
			return candidate
		}
	}
	return ""
}
