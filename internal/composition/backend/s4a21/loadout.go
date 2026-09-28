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
	"strconv"
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
	a21ItemCoreSize            = 99
	a21ListTypeEquipment       = 3
	a21ItemKindEquipment       = 1
	a21ItemKindConsumable      = 2
	a21ItemKindMaterial        = 3
	a21ItemKindCreature        = 5
	a21ItemKindArtifact        = 6
	a21ItemKindAvatar          = 8
	a21ItemKindSpecialMaterial = 11
	a21CreatureSlot            = 25
	a21ArtifactSlotBase        = 26
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
}

type CharacterInitializer interface {
	InitializeCharacter(context.Context, string, robotcap.Info, int, int) (robotcap.Info, error)
}

type SQLiteLoadoutApplier struct {
	DatabasePath    string
	Config          robotconfig.RuntimeConfig
	Equipment       []shared.EquipmentCatalogItem
	QuestGates      capabilitypvf.QuestGates
	StatTables      map[int]capabilitypvf.CharacterStatTables
	LevelThresholds []int
	RandIntn        func(int) int
	db              *sql.DB
	items           map[int]shared.EquipmentCatalogItem
	petSchema       bool
	avatars         *equipmentcap.AvatarCandidateCatalog
	executorMu      lockhub.Locker
	executor        *persistenceExecutor
	closed          bool
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
		avatars:  equipmentcap.PrepareAvatarCatalog(equipment, config),
		executor: newPersistenceExecutor(s4a21PersistenceQueueSize),
	}, nil
}

// avatarCatalog lazily prepares the per-job avatar candidates for adapters
// built as a bare struct (tests) so repeated selections stay cheap.
func (a *SQLiteLoadoutApplier) avatarCatalog() *equipmentcap.AvatarCandidateCatalog {
	if a.avatars == nil {
		a.avatars = equipmentcap.PrepareAvatarCatalog(a.Equipment, a.Config)
	}
	return a.avatars
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
	_, err := a.applyCharacterLoadout(ctx, account, info)
	return err
}

// applyCharacterLoadout applies the generated loadout and reports whether the
// stored equipment had to be replaced.
func (a *SQLiteLoadoutApplier) applyCharacterLoadout(ctx context.Context, account string, info robotcap.Info) (bool, error) {
	replaced := false
	err := a.persistenceDo(ctx, func(ctx context.Context) error {
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
		changed, err := a.applyResolvedCharacterLoadout(ctx, db, accountID, characterID, actual, items, petSchema)
		if err != nil {
			return err
		}
		replaced = changed
		return nil
	})
	return replaced, err
}

// ReconcileRobotLoadouts re-applies the generated loadout to robots whose saved
// equipment no longer matches the selection window for their level and job
// (for example stale initial weapons after a level change). Robots whose
// loadout is already current are left untouched.
func (a *SQLiteLoadoutApplier) ReconcileRobotLoadouts(ctx context.Context, accountPrefix string, robots []robotcap.Info) (int, error) {
	if a == nil || len(robots) == 0 {
		return 0, nil
	}
	prefix := strings.TrimSpace(accountPrefix)
	if prefix == "" {
		return 0, fmt.Errorf("S4A21 loadout reconcile account prefix is required")
	}
	replaced := 0
	for index := range robots {
		info := robots[index]
		if info.UID <= 0 || strings.TrimSpace(info.Name) == "" {
			continue
		}
		changed, err := a.applyCharacterLoadout(ctx, prefix+strconv.Itoa(info.UID), info)
		if err != nil {
			return replaced, fmt.Errorf("reconcile S4A21 loadout uid=%d: %w", info.UID, err)
		}
		if changed {
			replaced++
		}
	}
	return replaced, nil
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
		resolved, err = a.writeResolvedCharacterProgression(ctx, db, characterID, resolved.Job, resolved, level, grow)
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
		if _, err := a.applyResolvedCharacterLoadout(ctx, db, accountID, characterID, resolved, items, petSchema); err != nil {
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

func (a *SQLiteLoadoutApplier) applyResolvedCharacterLoadout(ctx context.Context, db *sql.DB, accountID, characterID int, info robotcap.Info, items map[int]shared.EquipmentCatalogItem, petSchema bool) (bool, error) {
	selectedEquipment := selectS4A21Equipment(a.Equipment, info.Level, info.Job, a.Config, a.RandIntn)
	selectedAvatar := a.avatarCatalog().Select(s4a21AvatarJob(info.Job), a.Config, a.RandIntn)
	selectedPet, selectedArtifacts, petSelected := equipmentcap.SelectPet(a.Equipment, a.Config, a.RandIntn)
	if petSelected && !petSchema {
		petSelected = false
		selectedArtifacts = nil
	}
	if len(selectedEquipment) == 0 {
		return false, fmt.Errorf("S4A21 loadout has no compatible equipment for level=%d job=%d", info.Level, info.Job)
	}
	if a.Config.MinAvatarSlots > 0 && len(selectedAvatar) < a.Config.MinAvatarSlots {
		return false, fmt.Errorf("S4A21 loadout has %d compatible avatar slots, need %d for job=%d", len(selectedAvatar), a.Config.MinAvatarSlots, info.Job)
	}
	bestLevels := equipmentcap.BestEquipmentLevels(a.Equipment, info.Level, info.Job, a.Config)
	compatible, err := existingLoadoutCompatible(ctx, db, characterID, info, items, a.Config, bestLevels, len(selectedEquipment), len(selectedAvatar), petSchema)
	if err != nil {
		return false, err
	}
	if compatible {
		return false, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := replaceLoadout(ctx, tx, accountID, characterID, selectedEquipment, selectedAvatar, selectedPet, selectedArtifacts, petSelected, a.Config, a.RandIntn); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit S4A21 loadout: %w", err)
	}
	return true, nil
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
		actual, err = a.writeResolvedCharacterProgression(ctx, db, characterID, resolved.Job, resolved, level, resolved.Grow)
		return err
	})
	return actual, err
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

// writeResolvedCharacterProgression applies the planned level, cumulative
// experience and transfer/awakening state in one transaction. The server
// rebuilds the skill panel from (job, grow, level) at the next character
// select, so skills saved under the previous grow are cleared the same way the
// GM tool's grow overwrite does.
func (a *SQLiteLoadoutApplier) writeResolvedCharacterProgression(ctx context.Context, db *sql.DB, characterID, job int, actual robotcap.Info, level, grow int) (robotcap.Info, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return actual, err
	}
	defer tx.Rollback()
	if err := a.updateCharacterProgression(ctx, tx, characterID, job, level, grow); err != nil {
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

// updateCharacterProgression writes level, the cumulative experience of that
// level and the combat stat columns. Level, experience and stats must land in
// the same transaction: crashing between them leaves a character whose panel
// numbers do not match its level.
func (a *SQLiteLoadoutApplier) updateCharacterProgression(ctx context.Context, tx *sql.Tx, characterID, job, level, grow int) error {
	exp := capabilitypvf.LevelExpFor(a.LevelThresholds, level)
	result, err := tx.ExecContext(ctx, `UPDATE characters
SET level=?, exp=?, grow_type=?, updated_at=CURRENT_TIMESTAMP
	WHERE character_id=? AND delete_flag=0`, level, exp, grow, characterID)
	if err != nil {
		return fmt.Errorf("write S4A21 character progression id=%d: %w", characterID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("write S4A21 character progression id=%d affected=%d err=%v", characterID, affected, err)
	}
	return writeCombatStats(ctx, tx, characterID, job, level, grow, a.StatTables)
}

// writeCombatStats recomputes the character_subtype1_fields stat columns from
// the PVF growth tables, mirroring the server's CharacterStatComputer. The
// stat blob is built from the post-write (job, level, grow) triple.
func writeCombatStats(ctx context.Context, tx *sql.Tx, characterID, job, level, grow int, statTables map[int]capabilitypvf.CharacterStatTables) error {
	tables, ok := statTables[job]
	if !ok {
		// The server falls back to its fixed table when a .chr file cannot be
		// parsed, so provisioning stays possible instead of failing outright.
		tables = capabilitypvf.FallbackCharacterStatTables()
	}
	blob, err := capabilitypvf.BuildAdditionalInfo(tables, level, grow&0x0F, (grow>>4)&0x0F)
	if err != nil {
		return fmt.Errorf("build S4A21 combat stats id=%d job=%d level=%d grow=0x%02X: %w", characterID, job, level, grow, err)
	}
	fields, err := capabilitypvf.ParseCombatStatFields(blob)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO character_subtype1_fields(character_id) VALUES(?)`, characterID); err != nil {
		return fmt.Errorf("ensure S4A21 combat stats row id=%d: %w", characterID, err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE character_subtype1_fields SET
	stat_hp_max=?, stat_mp_max=?,
	stat_physical_attack=?, stat_physical_defense=?,
	stat_magical_attack=?, stat_magical_defense=?,
	stat_fire_resistance=?, stat_water_resistance=?,
	stat_dark_resistance=?, stat_light_resistance=?,
	stat_inventory_limit=?,
	stat_hp_regen_speed=?, stat_mp_regen_speed=?,
	stat_move_speed=?, stat_attack_speed=?,
	stat_cast_speed=?, stat_hit_recovery=?,
	stat_jump_power=?, stat_weight=?, stat_level=?
	WHERE character_id=?`,
		fields.HpMax, fields.MpMax,
		fields.PhysAtk, fields.PhysDef,
		fields.MagAtk, fields.MagDef,
		fields.FireRes, fields.WaterRes,
		fields.DarkRes, fields.LightRes,
		fields.InventoryLimit,
		fields.HpRegen, fields.MpRegen,
		fields.MoveSpeed, fields.AttackSpeed,
		fields.CastSpeed, fields.HitRecovery,
		fields.JumpPower, fields.Weight, 100,
		characterID)
	if err != nil {
		return fmt.Errorf("write S4A21 combat stats id=%d: %w", characterID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("write S4A21 combat stats id=%d affected=%d err=%v", characterID, affected, err)
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

func existingLoadoutCompatible(ctx context.Context, db *sql.DB, characterID int, info robotcap.Info, items map[int]shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, bestLevels map[int]int, wantEquipment, wantAvatar int, petSchema bool) (bool, error) {
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
	var creatureCore []byte
	for rows.Next() {
		var slot int
		var core []byte
		if err := rows.Scan(&slot, &core); err != nil {
			return false, err
		}
		if len(core) < 5 {
			return false, nil
		}
		if slot == a21CreatureSlot {
			creatureCore = append([]byte(nil), core...)
			continue
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
			if core[0] != a21ItemKindEquipment {
				return false, nil
			}
			equipmentCount++
			for _, setKey := range strings.Split(item.SetKey, "|") {
				if setKey = strings.TrimSpace(setKey); setKey != "" {
					equipmentSetCounts[setKey]++
				}
			}
			levelIncompatible := slot < 22 && (item.Level > info.Level || item.Level < bestLevels[slot-11]-10)
			if item.ItemType != slot-11 || levelIncompatible || !equipmentcap.UsableByJob(item.UseJob, info.Job) {
				return false, nil
			}
		case slot >= 0 && slot <= 9:
			if core[0] != a21ItemKindAvatar {
				return false, nil
			}
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
	return petLoadoutCompatible(ctx, db, characterID, items, rc, petSchema, creatureCore, artifactCores)
}

// petLoadoutCompatible keeps a stored pet when it satisfies the configured
// window instead of comparing it to the freshly rolled pet: comparing random
// selections would rewrite the whole loadout on every startup. The rules mirror
// the startup pet compliance check.
func petLoadoutCompatible(ctx context.Context, db *sql.DB, characterID int, items map[int]shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, petSchema bool, creatureCore []byte, artifactCores map[int][]byte) (bool, error) {
	if !rc.PetEnabled || !petSchema {
		// Pets are disabled: no creature core, creature row or artifact may stay.
		if len(creatureCore) > 0 || len(artifactCores) > 0 {
			return false, nil
		}
		return true, nil
	}
	if len(creatureCore) == 0 {
		return len(artifactCores) == 0, nil
	}
	pet, ok := startupCoreItem(creatureCore, a21ItemKindCreature, items)
	if !ok || pet.ItemType != 30 || len(creatureCore) < 9 {
		return false, nil
	}
	creatureUID := int(binary.LittleEndian.Uint32(creatureCore[5:9]))
	if creatureUID <= 0 {
		return false, nil
	}
	var storedUID int
	if err := db.QueryRowContext(ctx, `SELECT creature_key FROM character_creatures WHERE character_id=? AND sort_order=0`, characterID).Scan(&storedUID); err != nil || storedUID != creatureUID {
		return false, nil
	}
	return petArtifactsCompatible(rc, items, artifactCores), nil
}

// petArtifactsCompatible mirrors startup pet compliance: only configured
// artifact slots count and the total must stay inside the configured range.
func petArtifactsCompatible(rc robotconfig.RuntimeConfig, items map[int]shared.EquipmentCatalogItem, artifactCores map[int][]byte) bool {
	types := rc.PetArtifactSlots
	if len(types) == 0 {
		types = []int{31, 32, 33}
	}
	count := 0
	for slot, core := range artifactCores {
		itemType := 31 + (slot - a21ArtifactSlotBase)
		if !configuredIntValue(types, itemType) {
			continue
		}
		item, ok := startupCoreItem(core, a21ItemKindArtifact, items)
		if !ok || item.ItemType != itemType || !equipmentcap.PetArtifactRenderable(item) {
			return false
		}
		count++
	}
	if !rc.PetArtifactEnabled {
		return count == 0
	}
	return count >= rc.MinPetArtifactSlots && count <= rc.MaxPetArtifactSlots
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
VALUES (?,0,?,100,0,0,0,0,1,x'',0,'{}')`, characterID, creatureUID); err != nil {
			return fmt.Errorf("insert S4A21 creature character=%d uid=%d: %w", characterID, creatureUID, err)
		}
		// creature_buffer is the name-tag byte window of the subtype0 tail
		// (name tag id + expire time), not pet data. Robots carry no active
		// name tag, so the window stays cleared.
		creatureBuffer := make([]byte, 8)
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
	if (kind == a21ItemKindEquipment || kind == a21ItemKindArtifact) && item.Durability > 0 {
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
