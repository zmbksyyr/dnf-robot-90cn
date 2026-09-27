package s4a21

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/charset"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"

	_ "modernc.org/sqlite"
)

const s4a21PersistenceTimeout = 15 * time.Second

const (
	a21ItemCoreSize      = 99
	a21ListTypeEquipment = 3
	a21ItemKindEquipment = 1
	a21ItemKindCreature  = 5
	a21ItemKindArtifact  = 6
	a21ItemKindAvatar    = 8
	a21CreatureSlot      = 25
	a21ArtifactSlotBase  = 26
)

type CharacterLoadoutApplier interface {
	ApplyCharacterLoadout(context.Context, string, robotcap.Info) error
}

type CharacterProfileReader interface {
	ResolveCharacterProfile(context.Context, string, robotcap.Info) (robotcap.Info, error)
}

type CharacterProfileAdapter interface {
	CharacterProfileReader
	ApplyPlannedCharacterLevel(context.Context, string, robotcap.Info, int) (robotcap.Info, error)
	ReconcileConfiguredCharacterLevel(context.Context, string, robotcap.Info) (robotcap.Info, error)
}

type CharacterInitializer interface {
	InitializeCharacter(context.Context, string, robotcap.Info, int, int) (robotcap.Info, error)
}

type SQLiteLoadoutApplier struct {
	DatabasePath string
	Config       robotconfig.RuntimeConfig
	Equipment    []shared.EquipmentCatalogItem
	QuestGates   capabilitypvf.QuestGates
	RandIntn     func(int) int
	db           *sql.DB
	items        map[int]shared.EquipmentCatalogItem
	petSchema    bool
	executorMu   lockhub.Locker
	executor     *persistenceExecutor
	closed       bool
}

func NewSQLiteLoadoutApplier(ctx context.Context, databasePath string, config robotconfig.RuntimeConfig, equipment []shared.EquipmentCatalogItem, randIntn func(int) int) (*SQLiteLoadoutApplier, error) {
	if strings.TrimSpace(databasePath) == "" {
		return nil, fmt.Errorf("S4A21 loadout database path is required")
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open S4A21 loadout database: %w", err)
	}
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure S4A21 loadout database: %w", err)
	}
	if err := validateLoadoutSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteLoadoutApplier{
		DatabasePath: databasePath, Config: config, Equipment: equipment, RandIntn: randIntn,
		db: db, items: equipmentByID(equipment), petSchema: petSchemaAvailable(ctx, db),
		executor: newPersistenceExecutor(s4a21PersistenceQueueSize),
	}, nil
}

func (a *SQLiteLoadoutApplier) Close() error {
	if a == nil {
		return nil
	}
	a.executorMu.Lock()
	executor := a.executor
	a.executor = nil
	a.closed = true
	a.executorMu.Unlock()
	executor.Close()
	if a.db == nil {
		return nil
	}
	err := a.db.Close()
	a.db = nil
	return err
}

func (a *SQLiteLoadoutApplier) persistenceDo(ctx context.Context, run func(context.Context) error) error {
	if a == nil {
		return fmt.Errorf("S4A21 persistence adapter is nil")
	}
	a.executorMu.Lock()
	if a.closed {
		a.executorMu.Unlock()
		return errPersistenceExecutorClosed
	}
	if a.executor == nil {
		a.executor = newPersistenceExecutor(s4a21PersistenceQueueSize)
	}
	executor := a.executor
	a.executorMu.Unlock()
	return executor.Do(ctx, func(jobCtx context.Context) error {
		jobCtx, cancel := context.WithTimeout(jobCtx, s4a21PersistenceTimeout)
		defer cancel()
		return run(jobCtx)
	})
}

func (a *SQLiteLoadoutApplier) database(ctx context.Context) (*sql.DB, func(), error) {
	if a.db != nil {
		return a.db, func() {}, nil
	}
	db, err := sql.Open("sqlite", a.DatabasePath)
	if err != nil {
		return nil, nil, err
	}
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, func() { _ = db.Close() }, nil
}

// configureSQLitePool keeps every adapter-owned connection bounded to one
// writer/reader. The S4A21 server may hold the same file open, so creating a
// pool per actor would only increase lock contention and SQLITE_BUSY retries.
func configureSQLitePool(db *sql.DB) {
	if db == nil {
		return
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
}

type PersistenceInspector struct {
	DatabasePath string
	cache        *persistenceStatusCache
}

type persistenceStatusCache struct {
	mu      lockhub.Locker
	status  shared.PersistenceStatus
	expires time.Time
}

const persistenceStatusTTL = 15 * time.Second

func NewPersistenceInspector(databasePath string) PersistenceInspector {
	return PersistenceInspector{DatabasePath: databasePath, cache: &persistenceStatusCache{}}
}

func (i PersistenceInspector) Status(ctx context.Context) shared.PersistenceStatus {
	if i.cache == nil {
		return i.checkStatus(ctx)
	}
	i.cache.mu.Lock()
	defer i.cache.mu.Unlock()
	if time.Now().Before(i.cache.expires) {
		return i.cache.status
	}
	status := i.checkStatus(ctx)
	i.cache.status = status
	i.cache.expires = time.Now().Add(persistenceStatusTTL)
	return status
}

func (i PersistenceInspector) checkStatus(ctx context.Context) shared.PersistenceStatus {
	started := time.Now()
	status := shared.PersistenceStatus{
		Engine: "sqlite", Target: filepath.Clean(strings.TrimSpace(i.DatabasePath)), CheckedAt: started,
	}
	fail := func(err error) shared.PersistenceStatus {
		status.Error = err.Error()
		status.LatencyMS = time.Since(started).Milliseconds()
		return status
	}
	if strings.TrimSpace(i.DatabasePath) == "" {
		return fail(fmt.Errorf("S4A21 database path is empty"))
	}
	file, err := os.OpenFile(i.DatabasePath, os.O_RDWR, 0)
	if err != nil {
		return fail(fmt.Errorf("open S4A21 database read-write: %w", err))
	}
	if err := file.Close(); err != nil {
		return fail(fmt.Errorf("close S4A21 database probe: %w", err))
	}
	status.Writable = true
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(i.DatabasePath))
	if err != nil {
		return fail(fmt.Errorf("open S4A21 database: %w", err))
	}
	configureSQLitePool(db)
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return fail(fmt.Errorf("configure S4A21 database inspection: %w", err))
	}
	if err := db.PingContext(ctx); err != nil {
		return fail(fmt.Errorf("ping S4A21 database: %w", err))
	}
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		return fail(fmt.Errorf("query S4A21 database: value=%d err=%v", one, err))
	}
	status.SelectVerified = true
	if err := validateLoadoutSchema(ctx, db); err != nil {
		return fail(err)
	}
	status.OK = true
	status.LatencyMS = time.Since(started).Milliseconds()
	return status
}

func sqliteReadOnlyDSN(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String()
}

func (a *SQLiteLoadoutApplier) ApplyCharacterLoadout(ctx context.Context, account string, info robotcap.Info) error {
	return a.persistenceDo(ctx, func(ctx context.Context) error {
		if strings.TrimSpace(a.DatabasePath) == "" {
			return fmt.Errorf("S4A21 loadout database path is required")
		}
		db, closeDB, err := a.database(ctx)
		if err != nil {
			return fmt.Errorf("open S4A21 loadout database: %w", err)
		}
		defer closeDB()
		if a.db == nil {
			if err := validateLoadoutSchema(ctx, db); err != nil {
				return err
			}
		}
		accountID, characterID, actual, err := resolveCharacterProfile(ctx, db, account, info)
		if err != nil {
			return err
		}
		items := a.items
		if items == nil {
			items = equipmentByID(a.Equipment)
		}
		petSchema := a.petSchema
		if a.db == nil {
			petSchema = petSchemaAvailable(ctx, db)
		}
		return a.applyResolvedCharacterLoadout(ctx, db, accountID, characterID, actual, items, petSchema)
	})
}

func (a *SQLiteLoadoutApplier) InitializeCharacter(ctx context.Context, account string, info robotcap.Info, level, grow int) (robotcap.Info, error) {
	if level < 1 || level > math.MaxUint8 {
		return info, fmt.Errorf("S4A21 character level must be between 1 and %d", math.MaxUint8)
	}
	if err := validateGrowType(grow); err != nil {
		return info, err
	}
	actual := info
	err := a.persistenceDo(ctx, func(ctx context.Context) error {
		db, closeDB, err := a.database(ctx)
		if err != nil {
			return fmt.Errorf("open S4A21 character database: %w", err)
		}
		defer closeDB()
		if a.db == nil {
			if err := validateLoadoutSchema(ctx, db); err != nil {
				return err
			}
		}
		accountID, characterID, resolved, err := resolveCharacterProfile(ctx, db, account, info)
		if err != nil {
			return err
		}
		resolved, err = writeResolvedCharacterProgression(ctx, db, characterID, resolved, level, grow)
		if err != nil {
			return err
		}
		items := a.items
		if items == nil {
			items = equipmentByID(a.Equipment)
		}
		petSchema := a.petSchema
		if a.db == nil {
			petSchema = petSchemaAvailable(ctx, db)
		}
		if err := a.applyResolvedCharacterLoadout(ctx, db, accountID, characterID, resolved, items, petSchema); err != nil {
			return err
		}
		if err := applyQuestGates(ctx, db, characterID, a.QuestGates); err != nil {
			return fmt.Errorf("seed S4A21 quest gates uid=%d: %w", resolved.UID, err)
		}
		actual = resolved
		return nil
	})
	return actual, err
}

func (a *SQLiteLoadoutApplier) applyResolvedCharacterLoadout(ctx context.Context, db *sql.DB, accountID, characterID int, info robotcap.Info, items map[int]shared.EquipmentCatalogItem, petSchema bool) error {
	selectedEquipment := selectS4A21Equipment(a.Equipment, info.Level, info.Job, a.Config, a.RandIntn)
	selectedAvatar := equipmentcap.SelectAvatar(a.Equipment, s4a21AvatarJob(info.Job), a.Config, a.RandIntn)
	selectedPet, selectedArtifacts, petSelected := equipmentcap.SelectPet(a.Equipment, a.Config, a.RandIntn)
	if petSelected && !petSchema {
		petSelected = false
		selectedArtifacts = nil
	}
	if len(selectedEquipment) == 0 {
		return fmt.Errorf("S4A21 loadout has no compatible equipment for level=%d job=%d", info.Level, info.Job)
	}
	if a.Config.MinAvatarSlots > 0 && len(selectedAvatar) < a.Config.MinAvatarSlots {
		return fmt.Errorf("S4A21 loadout has %d compatible avatar slots, need %d for job=%d", len(selectedAvatar), a.Config.MinAvatarSlots, info.Job)
	}
	compatible, err := existingLoadoutCompatible(ctx, db, characterID, info, items, a.Config, len(selectedEquipment), len(selectedAvatar), petSelected, selectedPet, selectedArtifacts)
	if err != nil {
		return err
	}
	if compatible {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceLoadout(ctx, tx, accountID, characterID, selectedEquipment, selectedAvatar, selectedPet, selectedArtifacts, petSelected, a.Config, a.RandIntn); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit S4A21 loadout: %w", err)
	}
	return nil
}

// S4A21 exposes support and magic-stone slots for every generated character,
// while its PVF only contains those items at level 60 and above. Keep the
// shared selector's level rules for ordinary equipment, then fill either
// special slot with the lowest compatible PVF item when the configured random
// level is below the first available item.
func selectS4A21Equipment(items []shared.EquipmentCatalogItem, level, job int, rc robotconfig.RuntimeConfig, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	selected := equipmentcap.SelectEquipment(items, level, job, rc, randIntn)
	configured := func(slot int) bool {
		if len(rc.EquipSlots) == 0 {
			return true
		}
		for _, configuredSlot := range rc.EquipSlots {
			if configuredSlot == slot {
				return true
			}
		}
		return false
	}
	for _, slot := range []int{11, 12} {
		if !configured(slot) {
			continue
		}
		if _, ok := selected[slot]; ok {
			continue
		}
		candidates := make([]shared.EquipmentCatalogItem, 0)
		lowestLevel := math.MaxInt
		for _, item := range items {
			if item.ID <= 0 || item.ItemType != slot || item.Expire || !s4a21LoadoutCompatible(item) || !equipmentcap.UsableByJob(item.UseJob, job) {
				continue
			}
			if rc.EquipRarityMax > 0 && (item.Rarity < rc.EquipRarityMin || item.Rarity > rc.EquipRarityMax) {
				continue
			}
			if item.Level < lowestLevel {
				lowestLevel = item.Level
				candidates = candidates[:0]
			}
			if item.Level == lowestLevel {
				candidates = append(candidates, item)
			}
		}
		if len(candidates) > 0 {
			selected[slot] = candidates[randomBetween(randIntn, 0, len(candidates)-1)]
		}
	}
	return selected
}

func (a *SQLiteLoadoutApplier) ResolveCharacterProfile(ctx context.Context, account string, info robotcap.Info) (robotcap.Info, error) {
	actual := info
	err := a.persistenceDo(ctx, func(ctx context.Context) error {
		if strings.TrimSpace(a.DatabasePath) == "" {
			return fmt.Errorf("S4A21 profile database path is required")
		}
		db, closeDB, err := a.database(ctx)
		if err != nil {
			return fmt.Errorf("open S4A21 profile database: %w", err)
		}
		defer closeDB()
		_, _, resolved, err := resolveCharacterProfile(ctx, db, account, info)
		actual = resolved
		return err
	})
	return actual, err
}

func (a *SQLiteLoadoutApplier) ApplyPlannedCharacterLevel(ctx context.Context, account string, info robotcap.Info, level int) (robotcap.Info, error) {
	if level < 1 || level > math.MaxUint8 {
		return info, fmt.Errorf("S4A21 character level must be between 1 and %d", math.MaxUint8)
	}
	return a.writeCharacterLevel(ctx, account, info, level)
}

func (a *SQLiteLoadoutApplier) ReconcileConfiguredCharacterLevel(ctx context.Context, account string, info robotcap.Info) (robotcap.Info, error) {
	actual := info
	err := a.persistenceDo(ctx, func(ctx context.Context) error {
		if strings.TrimSpace(a.DatabasePath) == "" {
			return fmt.Errorf("S4A21 profile database path is required")
		}
		db, closeDB, err := a.database(ctx)
		if err != nil {
			return fmt.Errorf("open S4A21 profile database: %w", err)
		}
		defer closeDB()
		_, characterID, resolved, err := resolveCharacterProfile(ctx, db, account, info)
		if err != nil {
			return err
		}
		actual, err = a.reconcileResolvedCharacterLevel(ctx, db, characterID, resolved)
		return err
	})
	return actual, err
}

func (a *SQLiteLoadoutApplier) reconcileResolvedCharacterLevel(ctx context.Context, db *sql.DB, characterID int, actual robotcap.Info) (robotcap.Info, error) {
	minLevel, maxLevel := a.Config.LevelMin, a.Config.LevelMax
	if minLevel < 1 {
		minLevel = 1
	}
	if maxLevel < minLevel {
		maxLevel = minLevel
	}
	if maxLevel > math.MaxUint8 {
		maxLevel = math.MaxUint8
	}
	if actual.Level >= minLevel && actual.Level <= maxLevel {
		return actual, nil
	}
	return writeResolvedCharacterLevel(ctx, db, characterID, actual, randomBetween(a.RandIntn, minLevel, maxLevel))
}

func (a *SQLiteLoadoutApplier) writeCharacterLevel(ctx context.Context, account string, info robotcap.Info, level int) (robotcap.Info, error) {
	actual := info
	err := a.persistenceDo(ctx, func(ctx context.Context) error {
		if strings.TrimSpace(a.DatabasePath) == "" {
			return fmt.Errorf("S4A21 profile database path is required")
		}
		db, closeDB, err := a.database(ctx)
		if err != nil {
			return fmt.Errorf("open S4A21 profile database: %w", err)
		}
		defer closeDB()
		_, characterID, resolved, err := resolveCharacterProfile(ctx, db, account, info)
		if err != nil {
			return err
		}
		actual, err = writeResolvedCharacterLevel(ctx, db, characterID, resolved, level)
		return err
	})
	return actual, err
}

func writeResolvedCharacterLevel(ctx context.Context, db *sql.DB, characterID int, actual robotcap.Info, level int) (robotcap.Info, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return actual, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE characters
SET level=?, exp=0, updated_at=CURRENT_TIMESTAMP
	WHERE character_id=? AND delete_flag=0`, level, characterID)
	if err != nil {
		return actual, fmt.Errorf("write S4A21 character level id=%d: %w", characterID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return actual, fmt.Errorf("write S4A21 character level id=%d affected=%d err=%v", characterID, affected, err)
	}
	if err := tx.Commit(); err != nil {
		return actual, fmt.Errorf("commit S4A21 character level id=%d: %w", characterID, err)
	}
	actual.Level = level
	return actual, nil
}

// validateGrowType mirrors the server's CharacterStatComputer guard: the low
// nibble is the transfer branch (0..5) and the high nibble the awakening stage
// (0..2). An awakening without a transfer is not a state the server produces.
func validateGrowType(grow int) error {
	first := grow & 0x0F
	second := (grow >> 4) & 0x0F
	if grow < 0 || grow > math.MaxUint8 || first > 5 || second > 2 || (second > 0 && first == 0) {
		return fmt.Errorf("S4A21 grow type 0x%02X is not a valid transfer/awakening state", grow)
	}
	return nil
}

// writeResolvedCharacterProgression applies the planned level and transfer or
// awakening state in one transaction. The server rebuilds the skill panel from
// (job, grow, level) at the next character select, so skills saved under the
// previous grow are cleared the same way the GM tool's grow overwrite does.
func writeResolvedCharacterProgression(ctx context.Context, db *sql.DB, characterID int, actual robotcap.Info, level, grow int) (robotcap.Info, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return actual, err
	}
	defer tx.Rollback()
	if err := updateCharacterProgression(ctx, tx, characterID, level, grow); err != nil {
		return actual, err
	}
	if actual.Grow != grow {
		if err := resetCharacterSkills(ctx, tx, characterID); err != nil {
			return actual, err
		}
	}
	if err := tx.Commit(); err != nil {
		return actual, fmt.Errorf("commit S4A21 character progression id=%d: %w", characterID, err)
	}
	actual.Level = level
	actual.Grow = grow
	return actual, nil
}

func updateCharacterProgression(ctx context.Context, tx *sql.Tx, characterID, level, grow int) error {
	result, err := tx.ExecContext(ctx, `UPDATE characters
SET level=?, exp=0, grow_type=?, updated_at=CURRENT_TIMESTAMP
	WHERE character_id=? AND delete_flag=0`, level, grow, characterID)
	if err != nil {
		return fmt.Errorf("write S4A21 character progression id=%d: %w", characterID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("write S4A21 character progression id=%d affected=%d err=%v", characterID, affected, err)
	}
	return nil
}

// updateCharacterGrow changes only grow_type so an untransferred character can
// be reconciled without touching its level or accumulated experience.
func updateCharacterGrow(ctx context.Context, tx *sql.Tx, characterID, grow int) error {
	result, err := tx.ExecContext(ctx, `UPDATE characters
SET grow_type=?, updated_at=CURRENT_TIMESTAMP
	WHERE character_id=? AND delete_flag=0`, grow, characterID)
	if err != nil {
		return fmt.Errorf("write S4A21 character grow id=%d: %w", characterID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("write S4A21 character grow id=%d affected=%d err=%v", characterID, affected, err)
	}
	return nil
}

func resetCharacterSkills(ctx context.Context, tx *sql.Tx, characterID int) error {
	available, err := tableAvailable(ctx, tx, "character_skills")
	if err != nil {
		return err
	}
	if !available {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_skills WHERE character_id=?`, characterID); err != nil {
		return fmt.Errorf("reset S4A21 character skills id=%d: %w", characterID, err)
	}
	return nil
}

func tableAvailable(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect S4A21 schema for %s: %w", name, err)
	}
	return found == name, nil
}

func equipmentByID(catalog []shared.EquipmentCatalogItem) map[int]shared.EquipmentCatalogItem {
	items := make(map[int]shared.EquipmentCatalogItem, len(catalog))
	for _, item := range catalog {
		items[item.ID] = item
	}
	return items
}

func resolveCharacterProfile(ctx context.Context, db *sql.DB, account string, info robotcap.Info) (int, int, robotcap.Info, error) {
	// Older A21 SQLite files store the name column as raw GBK bytes. A scan
	// may therefore produce a Go string that is not valid UTF-8. Preserve the
	// raw bytes for that case instead of making profile reconciliation fail.
	var encodedName interface{} = info.Name
	if encoded, encodeErr := charset.EncodeGBKString(info.Name); encodeErr == nil {
		encodedName = encoded
	}
	var accountID, characterID int
	err := db.QueryRowContext(ctx, `SELECT a.account_id, c.character_id, c.job, c.grow_type, c.level
FROM accounts a JOIN characters c ON c.account_id = a.account_id
WHERE a.m_id = ? AND (c.name = ? OR CAST(c.name AS TEXT) = ?) AND c.delete_flag = 0 LIMIT 1`, account, encodedName, info.Name).
		Scan(&accountID, &characterID, &info.Job, &info.Grow, &info.Level)
	if err != nil {
		return 0, 0, info, fmt.Errorf("resolve S4A21 character %s/%s: %w", account, info.Name, err)
	}
	return accountID, characterID, info, nil
}

func existingLoadoutCompatible(ctx context.Context, db *sql.DB, characterID int, info robotcap.Info, items map[int]shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, wantEquipment, wantAvatar int, wantPet bool, pet shared.EquipmentCatalogItem, artifacts map[int]shared.EquipmentCatalogItem) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT slot_index,item_core FROM character_inventory_items
WHERE character_id=? AND list_type=? AND slot_index BETWEEN 0 AND 28`, characterID, a21ListTypeEquipment)
	if err != nil {
		return false, fmt.Errorf("inspect S4A21 loadout: %w", err)
	}
	defer rows.Close()
	equipmentCount, avatarCount := 0, 0
	equipmentSetCounts := make(map[string]int)
	avatarSetCounts := make(map[string]int)
	artifactCores := make(map[int][]byte, 3)
	for rows.Next() {
		var slot int
		var core []byte
		if err := rows.Scan(&slot, &core); err != nil {
			return false, err
		}
		if len(core) < 5 {
			return false, nil
		}
		if slot >= a21ArtifactSlotBase && slot <= 28 {
			if _, exists := artifactCores[slot]; !exists {
				artifactCores[slot] = append([]byte(nil), core...)
			}
		}
		item, ok := items[int(binary.LittleEndian.Uint32(core[1:5]))]
		if !ok || item.ID <= 0 || item.Expire || !s4a21LoadoutCompatible(item) {
			return false, nil
		}
		switch {
		case slot >= 12 && slot <= 23:
			equipmentCount++
			for _, setKey := range strings.Split(item.SetKey, "|") {
				if setKey = strings.TrimSpace(setKey); setKey != "" {
					equipmentSetCounts[setKey]++
				}
			}
			levelIncompatible := slot < 22 && item.Level > info.Level
			if item.ItemType != slot-11 || levelIncompatible || !equipmentcap.UsableByJob(item.UseJob, info.Job) {
				return false, nil
			}
		case slot >= 0 && slot <= 9:
			avatarCount++
			for _, setKey := range strings.Split(item.SetKey, "|") {
				if setKey = strings.TrimSpace(setKey); setKey != "" {
					avatarSetCounts[setKey]++
				}
			}
			if item.ItemType != slot+20 || !equipmentcap.AvatarRenderable(item) || !equipmentcap.AvatarUsableByJob(item, s4a21AvatarJob(info.Job)) {
				return false, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if equipmentCount < wantEquipment || avatarCount < wantAvatar {
		return false, nil
	}
	if rc.PreferEquipSets && maxSetCount(equipmentSetCounts) < requiredSetCoverage(wantEquipment, rc.EquipSetMinSlots, 5) {
		return false, nil
	}
	if rc.PreferAvatarSets && maxSetCount(avatarSetCounts) < requiredSetCoverage(wantAvatar, rc.AvatarSetMinSlots, 6) {
		return false, nil
	}
	if wantPet {
		var creatureCore []byte
		if err := db.QueryRowContext(ctx, `SELECT item_core FROM character_inventory_items WHERE character_id=? AND list_type=? AND slot_index=?`, characterID, a21ListTypeEquipment, a21CreatureSlot).Scan(&creatureCore); err != nil {
			return false, nil
		}
		if len(creatureCore) < 9 || creatureCore[0] != a21ItemKindCreature || int(binary.LittleEndian.Uint32(creatureCore[1:5])) != pet.ID {
			return false, nil
		}
		creatureUID := int(binary.LittleEndian.Uint32(creatureCore[5:9]))
		if creatureUID <= 0 {
			return false, nil
		}
		petCore, err := db.QueryContext(ctx, `SELECT creature_buffer FROM character_subtype0_fields WHERE character_id=?`, characterID)
		if err != nil {
			return false, err
		}
		petPresent := false
		if petCore.Next() {
			var raw []byte
			if err := petCore.Scan(&raw); err != nil {
				petCore.Close()
				return false, err
			}
			petPresent = len(raw) >= 4 && binary.LittleEndian.Uint32(raw[:4]) != 0
		}
		if err := petCore.Err(); err != nil {
			petCore.Close()
			return false, err
		}
		petCore.Close()
		if !petPresent {
			return false, nil
		}
		var storedCreatureUID int
		if err := db.QueryRowContext(ctx, `SELECT creature_key FROM character_creatures WHERE character_id=? AND sort_order=0`, characterID).Scan(&storedCreatureUID); err != nil || storedCreatureUID != creatureUID {
			return false, nil
		}
		for itemType, item := range artifacts {
			slot := a21ArtifactSlotBase + itemType - 31
			raw, exists := artifactCores[slot]
			valid := exists && len(raw) >= 5 && raw[0] == a21ItemKindArtifact && int(binary.LittleEndian.Uint32(raw[1:5])) == item.ID
			if !valid {
				return false, nil
			}
		}
	}
	return true, nil
}

func requiredSetCoverage(want, configured, preferred int) int {
	if want <= 0 {
		return 0
	}
	if configured < 2 {
		configured = 2
	}
	if preferred > configured {
		configured = preferred
	}
	if configured > want {
		configured = want
	}
	return configured
}

func maxSetCount(counts map[string]int) int {
	max := 0
	for _, count := range counts {
		if count > max {
			max = count
		}
	}
	return max
}

// The shared incompatibility marker protects the DP2 item-info
// consumer from extended fields that precede [equipment type]. S4A21 reads
// its bundled PVF directly and supports those real support/magic-stone items,
// so the adapter may use them without weakening the shared policy.
func s4a21LoadoutCompatible(item shared.EquipmentCatalogItem) bool {
	return shared.ClientCompatibleEquipment(item) || item.ItemType == 11 || item.ItemType == 12
}

func petSchemaAvailable(ctx context.Context, db *sql.DB) bool {
	for _, table := range []string{"character_creatures", "character_creature_uid_sequence", "character_subtype0_fields", "character_subtype1_fields"} {
		var found string
		if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil {
			return false
		}
	}
	return true
}

func s4a21AvatarJob(job int) int {
	switch job {
	case 9:
		return 0
	case 10:
		return 3
	default:
		return job
	}
}

func validateLoadoutSchema(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"accounts", "characters", "character_inventory_items", "character_avatar_detail", "character_avatar_uid_sequence"} {
		var found string
		if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil {
			return fmt.Errorf("S4A21 loadout schema missing table %s: %w", table, err)
		}
	}
	return nil
}

func replaceLoadout(ctx context.Context, tx *sql.Tx, accountID, characterID int, equipment map[int]shared.EquipmentCatalogItem, avatars map[int]shared.EquipmentCatalogItem, pet shared.EquipmentCatalogItem, artifacts map[int]shared.EquipmentCatalogItem, petSelected bool, rc robotconfig.RuntimeConfig, randIntn func(int) int) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_avatar_detail WHERE character_id = ?`, characterID); err != nil {
		return fmt.Errorf("clear S4A21 avatar details character=%d: %w", characterID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_inventory_items
	WHERE character_id = ? AND list_type = ? AND slot_index BETWEEN 0 AND 28`, characterID, a21ListTypeEquipment); err != nil {
		return fmt.Errorf("clear S4A21 equipped loadout: %w", err)
	}
	for commonSlot, item := range equipment {
		a21Slot := commonSlot + 11
		core := a21ItemCore(a21ItemKindEquipment, item, randomBetween(randIntn, rc.EquipIntensifyMin, rc.EquipIntensifyMax), int32(randomPositive(randIntn)))
		if err := upsertEquippedCore(ctx, tx, characterID, a21Slot, core); err != nil {
			return err
		}
	}
	for slot, item := range avatars {
		var avatarUID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO character_avatar_uid_sequence DEFAULT VALUES RETURNING avatar_uid`).Scan(&avatarUID); err != nil {
			return fmt.Errorf("allocate S4A21 avatar uid: %w", err)
		}
		if avatarUID <= 0 || avatarUID > math.MaxInt32 {
			return fmt.Errorf("S4A21 avatar uid out of range: %d", avatarUID)
		}
		core := a21ItemCore(a21ItemKindAvatar, item, 0, int32(avatarUID))
		if err := upsertEquippedCore(ctx, tx, characterID, slot, core); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO character_avatar_detail
(item_uid, owner_id, character_id, item_id, expire_date, clear_avatar_id, jewel_socket, color1, color2, delete_date)
VALUES (?, ?, ?, ?, 0, 0, zeroblob(30), 0, 0, 0)`, avatarUID, accountID, characterID, item.ID); err != nil {
			return fmt.Errorf("insert S4A21 avatar detail slot=%d item=%d: %w", slot, item.ID, err)
		}
	}
	if petSelected {
		var creatureUID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO character_creature_uid_sequence DEFAULT VALUES RETURNING creature_uid`).Scan(&creatureUID); err != nil {
			return fmt.Errorf("allocate S4A21 creature uid: %w", err)
		}
		if creatureUID <= 0 || creatureUID > math.MaxInt32 {
			return fmt.Errorf("S4A21 creature uid out of range: %d", creatureUID)
		}
		if err := upsertEquippedCore(ctx, tx, characterID, a21CreatureSlot, a21ItemCore(a21ItemKindCreature, pet, 0, int32(creatureUID))); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO character_subtype0_fields(character_id) VALUES(?)`, characterID); err != nil {
			return fmt.Errorf("initialize S4A21 creature state character=%d: %w", characterID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO character_subtype1_fields(character_id) VALUES(?)`, characterID); err != nil {
			return fmt.Errorf("initialize S4A21 creature level state character=%d: %w", characterID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM character_creatures WHERE character_id=?`, characterID); err != nil {
			return fmt.Errorf("clear S4A21 creatures character=%d: %w", characterID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO character_creatures
(character_id,sort_order,creature_key,field04,mode_flag,progress_value,mode1_field0a,mode1_field0b,field_after_value,creature_text,tail_flag,extra_json)
VALUES (?,0,?,0,0,0,0,0,0,NULL,0,'{}')`, characterID, creatureUID); err != nil {
			return fmt.Errorf("insert S4A21 creature character=%d uid=%d: %w", characterID, creatureUID, err)
		}
		creatureBuffer := make([]byte, 8)
		binary.LittleEndian.PutUint32(creatureBuffer, uint32(pet.ID))
		if _, err := tx.ExecContext(ctx, `UPDATE character_subtype0_fields SET creature_buffer=?,pet_display_flag=1 WHERE character_id=?`, creatureBuffer, characterID); err != nil {
			return fmt.Errorf("activate S4A21 creature character=%d: %w", characterID, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE character_subtype1_fields SET equipped_creature_level=1 WHERE character_id=?`, characterID); err != nil {
			return fmt.Errorf("set S4A21 creature level character=%d: %w", characterID, err)
		}
		for itemType, item := range artifacts {
			slot := a21ArtifactSlotBase + itemType - 31
			if err := upsertEquippedCore(ctx, tx, characterID, slot, a21ItemCore(a21ItemKindArtifact, item, 0, int32(randomPositive(randIntn)))); err != nil {
				return err
			}
		}
	} else if petSchemaAvailableTx(ctx, tx) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM character_creatures WHERE character_id=?`, characterID); err != nil {
			return fmt.Errorf("clear S4A21 creatures character=%d: %w", characterID, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE character_subtype0_fields SET creature_buffer=NULL,pet_display_flag=0 WHERE character_id=?`, characterID); err != nil {
			return fmt.Errorf("clear S4A21 creature character=%d: %w", characterID, err)
		}
	}
	return nil
}

func petSchemaAvailableTx(ctx context.Context, tx *sql.Tx) bool {
	for _, table := range []string{"character_creatures", "character_creature_uid_sequence", "character_subtype0_fields", "character_subtype1_fields"} {
		var found string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil {
			return false
		}
	}
	return true
}

func upsertEquippedCore(ctx context.Context, tx *sql.Tx, characterID, slot int, core []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO character_inventory_items
(character_id, list_type, slot_index, item_core, created_at, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(character_id, list_type, slot_index) DO UPDATE SET item_core=excluded.item_core, updated_at=CURRENT_TIMESTAMP`,
		characterID, a21ListTypeEquipment, slot, core)
	if err != nil {
		return fmt.Errorf("write S4A21 equipped slot=%d: %w", slot, err)
	}
	return nil
}

func a21ItemCore(kind byte, item shared.EquipmentCatalogItem, upgrade int, value int32) []byte {
	core := make([]byte, a21ItemCoreSize)
	core[0] = kind
	binary.LittleEndian.PutUint32(core[1:5], uint32(item.ID))
	binary.LittleEndian.PutUint32(core[5:9], uint32(value))
	if upgrade < 0 {
		upgrade = 0
	}
	if upgrade > 31 {
		upgrade = 31
	}
	core[9] = byte(upgrade)
	if kind == a21ItemKindEquipment && item.Durability > 0 {
		durability := item.Durability
		if durability > math.MaxUint16 {
			durability = math.MaxUint16
		}
		binary.LittleEndian.PutUint16(core[10:12], uint16(durability))
	}
	binary.LittleEndian.PutUint32(core[21:25], math.MaxUint32)
	core[66] = 0xFF
	return core
}

func randomBetween(randIntn func(int) int, min, max int) int {
	if max < min {
		min, max = max, min
	}
	if max <= min || randIntn == nil {
		return min
	}
	return min + randIntn(max-min+1)
}

func randomPositive(randIntn func(int) int) int {
	if randIntn == nil {
		return 1
	}
	return 1 + randIntn(math.MaxInt32-1)
}
