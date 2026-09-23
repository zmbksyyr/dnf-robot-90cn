package s4a21

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

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

type SQLiteLoadoutApplier struct {
	DatabasePath string
	Config       robotconfig.RuntimeConfig
	Equipment    []shared.EquipmentCatalogItem
	RandIntn     func(int) int
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
	encodedName, err := charset.EncodeGBKString(info.Name)
	if err != nil {
		return fmt.Errorf("encode S4A21 loadout character name %q: %w", info.Name, err)
	}
	var accountID, characterID int
	err = db.QueryRowContext(ctx, `SELECT a.account_id, c.character_id
FROM accounts a JOIN characters c ON c.account_id = a.account_id
WHERE a.m_id = ? AND (c.name = ? OR CAST(c.name AS TEXT) = ?) AND c.delete_flag = 0 LIMIT 1`, account, encodedName, info.Name).Scan(&accountID, &characterID)
	if err != nil {
		return fmt.Errorf("resolve S4A21 loadout character %s/%s: %w", account, info.Name, err)
	}
	selectedEquipment := equipmentcap.SelectEquipment(a.Equipment, info.Level, info.Job, a.Config, a.RandIntn)
	selectedAvatar := equipmentcap.SelectAvatar(a.Equipment, info.Job, a.Config, a.RandIntn)
	if len(selectedEquipment) == 0 {
		return fmt.Errorf("S4A21 loadout has no compatible equipment for level=%d job=%d", info.Level, info.Job)
	}
	if a.Config.MinAvatarSlots > 0 && len(selectedAvatar) < a.Config.MinAvatarSlots {
		return fmt.Errorf("S4A21 loadout has %d compatible avatar slots, need %d for job=%d", len(selectedAvatar), a.Config.MinAvatarSlots, info.Job)
	}
	var equipmentCount, avatarCount int
	if err := db.QueryRowContext(ctx, `SELECT
COUNT(CASE WHEN slot_index BETWEEN 12 AND 23 THEN 1 END),
COUNT(CASE WHEN slot_index BETWEEN 0 AND 9 THEN 1 END)
FROM character_inventory_items WHERE character_id=? AND list_type=?`, characterID, a21ListTypeEquipment).Scan(&equipmentCount, &avatarCount); err != nil {
		return fmt.Errorf("inspect S4A21 loadout: %w", err)
	}
	if equipmentCount >= len(selectedEquipment) && avatarCount >= len(selectedAvatar) {
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
	rows, err := tx.QueryContext(ctx, `SELECT item_core FROM character_inventory_items
WHERE character_id = ? AND list_type = ? AND slot_index BETWEEN 0 AND 11`, characterID, a21ListTypeEquipment)
	if err != nil {
		return fmt.Errorf("read S4A21 equipped avatars: %w", err)
	}
	var avatarUIDs []int64
	for rows.Next() {
		var core []byte
		if err := rows.Scan(&core); err != nil {
			rows.Close()
			return err
		}
		if len(core) >= 9 && core[0] == a21ItemKindAvatar {
			avatarUIDs = append(avatarUIDs, int64(binary.LittleEndian.Uint32(core[5:9])))
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, avatarUID := range avatarUIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM character_avatar_detail WHERE item_uid = ?`, avatarUID); err != nil {
			return fmt.Errorf("clear S4A21 avatar detail uid=%d: %w", avatarUID, err)
		}
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
