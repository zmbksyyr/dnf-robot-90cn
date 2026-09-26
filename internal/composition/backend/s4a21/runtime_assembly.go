package s4a21

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"robot/internal/capability/catalog"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

// RuntimeBundle holds the assembled S4A21 runtime components. The composition
// root installs them on the scheduler through the backend-neutral ports.
type RuntimeBundle struct {
	Inventory     StartupInventory
	State         *robotstate.MemoryStore
	Loadouts      *SQLiteLoadoutApplier
	Transport     *ActionTransport
	Creator       RobotCreator
	Cleaner       RobotCleaner
	Purger        SQLiteRobotPurger
	Inspector     SQLitePopulationInspector
	DatabasePath  string
	TownMaps      []shared.MapCatalogItem
	NameTemplates robottemplate.NameTemplates
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

	pvfPath, err := ResolvePVFPath(opts.ServerDirectory)
	if err != nil {
		return bundle, fmt.Errorf("town map: %w", err)
	}
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		return bundle, fmt.Errorf("PVF catalog: %w", err)
	}
	if err := ExportItemCatalogs(opts.Paths, catalogs.Equipment, catalogs.Stackable); err != nil {
		return bundle, fmt.Errorf("item catalog: %w", err)
	}
	databasePath, err := ResolveDatabasePath(opts.ServerDirectory, opts.DatabasePath)
	if err != nil {
		return bundle, fmt.Errorf("loadout database: %w", err)
	}
	inventory, err := (SQLiteStartupInventory{
		DatabasePath: databasePath, AccountPrefix: prefix, Config: opts.Config, Equipment: catalogs.Equipment,
	}).ScanAndClean(ctx)
	if err != nil {
		return bundle, fmt.Errorf("startup inventory: %w", err)
	}
	state := robotstate.NewMemoryStore(inventory.Robots)
	if err := state.RegisterIdentities(ctx, inventory.Identities); err != nil {
		return bundle, fmt.Errorf("startup identities: %w", err)
	}
	loadouts, err := NewSQLiteLoadoutApplier(ctx, databasePath, opts.Config, catalogs.Equipment, opts.RandIntn)
	if err != nil {
		return bundle, fmt.Errorf("loadout applier: %w", err)
	}
	transport, err := NewRuntimeTransport(opts.ConnectIP, opts.GamePort)
	if err != nil {
		_ = loadouts.Close()
		return bundle, fmt.Errorf("transport: %w", err)
	}
	address := net.JoinHostPort(opts.ConnectIP, fmt.Sprint(opts.GamePort))
	names := catalog.NameTemplates(opts.Paths.Templates)

	bundle.Inventory = inventory
	bundle.State = state
	bundle.Loadouts = loadouts
	bundle.Transport = transport
	bundle.DatabasePath = databasePath
	bundle.TownMaps = catalogs.TownMaps
	bundle.Creator = RobotCreator{
		Provisioner: Provisioner{Address: address},
		BatchStore:  state, IdentityStore: state, RobotCatalog: state,
		Config: opts.Config, Names: names, Maps: catalogs.TownMaps,
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
	bundle.NameTemplates = names
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

// ResolvePVFPath accepts either the server executable, the server directory or
// the Script.pvf file itself.
func ResolvePVFPath(serverDirectory string) (string, error) {
	value := strings.TrimSpace(serverDirectory)
	if value == "" {
		return "", fmt.Errorf("S4A21 PVF path cannot be resolved from empty ServerDirectory")
	}
	if strings.EqualFold(filepath.Ext(value), ".pvf") {
		if _, err := os.Stat(value); err != nil {
			return "", fmt.Errorf("S4A21 PVF %q: %w", value, err)
		}
		return value, nil
	}
	// Some adapter bundles configure ServerDirectory as the server executable,
	// while Linux-oriented bundles may configure it as the server directory.
	// Resolve both forms without making backend selection implicit.
	base := filepath.Dir(value)
	if stat, err := os.Stat(value); err == nil && stat.IsDir() {
		base = value
	}
	candidate := filepath.Join(base, "Script.pvf")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("S4A21 PVF %q: %w", candidate, err)
	}
	return candidate, nil
}

// ResolveDatabasePath prefers the explicitly configured path and otherwise
// derives Data/inventory.db beside the server executable or directory.
func ResolveDatabasePath(serverDirectory, configured string) (string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		if _, err := os.Stat(value); err != nil {
			return "", fmt.Errorf("S4A21 database %q: %w", value, err)
		}
		return value, nil
	}
	value := strings.TrimSpace(serverDirectory)
	if value == "" {
		return "", fmt.Errorf("S4A21 database path cannot be resolved from empty ServerDirectory")
	}
	base := value
	if stat, err := os.Stat(value); err == nil && !stat.IsDir() {
		base = filepath.Dir(value)
	} else if filepath.Ext(value) != "" {
		base = filepath.Dir(value)
	}
	candidate := filepath.Join(base, "Data", "inventory.db")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("S4A21 database %q: %w", candidate, err)
	}
	return candidate, nil
}
