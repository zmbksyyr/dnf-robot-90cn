package s4a21

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/charset"
	"robot/internal/shared"

	_ "modernc.org/sqlite"
)

const (
	a21ItemCoreSize      = 99
	a21ListTypeEquipment = 3
	a21ItemKindEquipment = 1
	a21ItemKindAvatar    = 8
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

type SQLiteLoadoutApplier struct {
	DatabasePath string
	Config       robotconfig.RuntimeConfig
	Equipment    []shared.EquipmentCatalogItem
	RandIntn     func(int) int
}

type PersistenceInspector struct {
	DatabasePath string
}

func (i PersistenceInspector) Status(ctx context.Context) shared.PersistenceStatus {
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
	db, err := sql.Open("sqlite", i.DatabasePath)
	if err != nil {
		return fail(fmt.Errorf("open S4A21 database: %w", err))
	}
	defer db.Close()
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
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fail(fmt.Errorf("verify S4A21 database integrity: result=%s err=%v", integrity, err))
	}
	status.OK = true
	status.LatencyMS = time.Since(started).Milliseconds()
	return status
}

func (a SQLiteLoadoutApplier) ApplyCharacterLoadout(ctx context.Context, account string, info robotcap.Info) error {
	if strings.TrimSpace(a.DatabasePath) == "" {
		return fmt.Errorf("S4A21 loadout database path is required")
	}
	db, err := sql.Open("sqlite", a.DatabasePath)
	if err != nil {
		return fmt.Errorf("open S4A21 loadout database: %w", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("configure S4A21 loadout database: %w", err)
	}
	if err := validateLoadoutSchema(ctx, db); err != nil {
		return err
	}
	accountID, characterID, actual, err := resolveCharacterProfile(ctx, db, account, info)
	if err != nil {
		return err
	}
	info = actual
	selectedEquipment := equipmentcap.SelectEquipment(a.Equipment, info.Level, info.Job, a.Config, a.RandIntn)
	selectedAvatar := equipmentcap.SelectAvatar(a.Equipment, s4a21AvatarJob(info.Job), a.Config, a.RandIntn)
	if len(selectedEquipment) == 0 {
		return fmt.Errorf("S4A21 loadout has no compatible equipment for level=%d job=%d", info.Level, info.Job)
	}
	if a.Config.MinAvatarSlots > 0 && len(selectedAvatar) < a.Config.MinAvatarSlots {
		return fmt.Errorf("S4A21 loadout has %d compatible avatar slots, need %d for job=%d", len(selectedAvatar), a.Config.MinAvatarSlots, info.Job)
	}
	compatible, err := existingLoadoutCompatible(ctx, db, characterID, info, a.Equipment, len(selectedEquipment), len(selectedAvatar))
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
	if err := replaceLoadout(ctx, tx, accountID, characterID, selectedEquipment, selectedAvatar, a.Config, a.RandIntn); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit S4A21 loadout: %w", err)
	}
	return nil
}

func (a SQLiteLoadoutApplier) ResolveCharacterProfile(ctx context.Context, account string, info robotcap.Info) (robotcap.Info, error) {
	if strings.TrimSpace(a.DatabasePath) == "" {
		return info, fmt.Errorf("S4A21 profile database path is required")
	}
	db, err := sql.Open("sqlite", a.DatabasePath)
	if err != nil {
		return info, fmt.Errorf("open S4A21 profile database: %w", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return info, err
	}
	_, _, actual, err := resolveCharacterProfile(ctx, db, account, info)
	return actual, err
}

func (a SQLiteLoadoutApplier) ApplyPlannedCharacterLevel(ctx context.Context, account string, info robotcap.Info, level int) (robotcap.Info, error) {
	if level < 1 || level > math.MaxUint8 {
		return info, fmt.Errorf("S4A21 character level must be between 1 and %d", math.MaxUint8)
	}
	return a.writeCharacterLevel(ctx, account, info, level)
}

func (a SQLiteLoadoutApplier) ReconcileConfiguredCharacterLevel(ctx context.Context, account string, info robotcap.Info) (robotcap.Info, error) {
	actual, err := a.ResolveCharacterProfile(ctx, account, info)
	if err != nil {
		return info, err
	}
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
	return a.writeCharacterLevel(ctx, account, actual, randomBetween(a.RandIntn, minLevel, maxLevel))
}

func (a SQLiteLoadoutApplier) writeCharacterLevel(ctx context.Context, account string, info robotcap.Info, level int) (robotcap.Info, error) {
	if strings.TrimSpace(a.DatabasePath) == "" {
		return info, fmt.Errorf("S4A21 profile database path is required")
	}
	db, err := sql.Open("sqlite", a.DatabasePath)
	if err != nil {
		return info, fmt.Errorf("open S4A21 profile database: %w", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return info, err
	}
	_, characterID, actual, err := resolveCharacterProfile(ctx, db, account, info)
	if err != nil {
		return info, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return info, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE characters
SET level=?, exp=0, updated_at=CURRENT_TIMESTAMP
WHERE character_id=? AND delete_flag=0`, level, characterID)
	if err != nil {
		return info, fmt.Errorf("write S4A21 character level id=%d: %w", characterID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return info, fmt.Errorf("write S4A21 character level id=%d affected=%d err=%v", characterID, affected, err)
	}
	if err := tx.Commit(); err != nil {
		return info, fmt.Errorf("commit S4A21 character level id=%d: %w", characterID, err)
	}
	actual.Level = level
	return actual, nil
}

func resolveCharacterProfile(ctx context.Context, db *sql.DB, account string, info robotcap.Info) (int, int, robotcap.Info, error) {
	encodedName, err := charset.EncodeGBKString(info.Name)
	if err != nil {
		return 0, 0, info, fmt.Errorf("encode S4A21 character name %q: %w", info.Name, err)
	}
	var accountID, characterID int
	err = db.QueryRowContext(ctx, `SELECT a.account_id, c.character_id, c.job, c.grow_type, c.level
FROM accounts a JOIN characters c ON c.account_id = a.account_id
WHERE a.m_id = ? AND (c.name = ? OR CAST(c.name AS TEXT) = ?) AND c.delete_flag = 0 LIMIT 1`, account, encodedName, info.Name).
		Scan(&accountID, &characterID, &info.Job, &info.Grow, &info.Level)
	if err != nil {
		return 0, 0, info, fmt.Errorf("resolve S4A21 character %s/%s: %w", account, info.Name, err)
	}
	return accountID, characterID, info, nil
}

func existingLoadoutCompatible(ctx context.Context, db *sql.DB, characterID int, info robotcap.Info, catalog []shared.EquipmentCatalogItem, wantEquipment, wantAvatar int) (bool, error) {
	items := make(map[int]shared.EquipmentCatalogItem, len(catalog))
	for _, item := range catalog {
		items[item.ID] = item
	}
	rows, err := db.QueryContext(ctx, `SELECT slot_index,item_core FROM character_inventory_items
WHERE character_id=? AND list_type=? AND slot_index BETWEEN 0 AND 23`, characterID, a21ListTypeEquipment)
	if err != nil {
		return false, fmt.Errorf("inspect S4A21 loadout: %w", err)
	}
	defer rows.Close()
	equipmentCount, avatarCount := 0, 0
	for rows.Next() {
		var slot int
		var core []byte
		if err := rows.Scan(&slot, &core); err != nil {
			return false, err
		}
		if len(core) < 5 {
			return false, nil
		}
		item, ok := items[int(binary.LittleEndian.Uint32(core[1:5]))]
		if !ok || item.ID <= 0 || item.Expire || !shared.ClientCompatibleEquipment(item) {
			return false, nil
		}
		switch {
		case slot >= 12 && slot <= 23:
			equipmentCount++
			if item.ItemType != slot-11 || item.Level > info.Level || !equipmentcap.UsableByJob(item.UseJob, info.Job) {
				return false, nil
			}
		case slot >= 0 && slot <= 9:
			avatarCount++
			if item.ItemType != slot+20 || !equipmentcap.AvatarRenderable(item) || !equipmentcap.AvatarUsableByJob(item, s4a21AvatarJob(info.Job)) {
				return false, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return equipmentCount >= wantEquipment && avatarCount >= wantAvatar, nil
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

func replaceLoadout(ctx context.Context, tx *sql.Tx, accountID, characterID int, equipment map[int]shared.EquipmentCatalogItem, avatars map[int]shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, randIntn func(int) int) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_avatar_detail WHERE character_id = ?`, characterID); err != nil {
		return fmt.Errorf("clear S4A21 avatar details character=%d: %w", characterID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_inventory_items
WHERE character_id = ? AND list_type = ? AND slot_index BETWEEN 0 AND 24`, characterID, a21ListTypeEquipment); err != nil {
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
	return nil
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
