package s4a21

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"robot/internal/shared"
)

// ServerNoticeStock prepares and probes the items one server-notice trigger
// consumes while the robot account is offline. The writes follow the same
// persistence boundary as the expert-job profession writer: the game server
// projects character inventory at select time, so stock only takes effect on
// the next login.
//
// The lottery trigger opens the default box ([upgradable legacy], stack limit
// 1000, no gold or key cost). The upgrade trigger reinforces equipment already
// at the table notice level (12): the server broadcasts on both success
// (newLevel >= notice) and failure (oldLevel >= notice).
type ServerNoticeStock struct {
	DatabasePath string
	Equipment    []shared.EquipmentCatalogItem

	// typeByID is built once at composition time; a nil map falls back to a
	// per-call build so tests and zero-value users keep working.
	typeByID map[int]int
}

// NewServerNoticeStock precomputes the item-type lookup the upgrade trigger
// needs. The returned value is copy-safe (contains no locks).
func NewServerNoticeStock(databasePath string, equipment []shared.EquipmentCatalogItem) ServerNoticeStock {
	stock := ServerNoticeStock{DatabasePath: databasePath, Equipment: equipment}
	stock.typeByID = buildNoticeTypeIndex(equipment)
	return stock
}

func buildNoticeTypeIndex(equipment []shared.EquipmentCatalogItem) map[int]int {
	index := make(map[int]int, len(equipment))
	for _, item := range equipment {
		if item.ID > 0 && item.ItemType > 0 {
			index[item.ID] = item.ItemType
		}
	}
	return index
}

const (
	// s4a21NoticeLotteryItemID is the default box: 罗杰的高级袖珍罐.
	s4a21NoticeLotteryItemID = 7941
	// s4a21NoticeLotterySlot is the start of the main consumable range.
	s4a21NoticeLotterySlot = 65
	// s4a21NoticeLotteryStack keeps a full PVF stack (stack limit 1000).
	s4a21NoticeLotteryStack = 1000
	// s4a21NoticeLotteryRefreshBelow refills the box when fewer remain.
	s4a21NoticeLotteryRefreshBelow = 50

	// s4a21NoticeGoldSlot is the character gold virtual slot.
	s4a21NoticeGoldSlot = 0
	// s4a21NoticeGoldMinimum is the gold floor required by one trigger.
	s4a21NoticeGoldMinimum = 500_000
	// s4a21NoticeGoldTarget is the offline top-up value.
	s4a21NoticeGoldTarget = 5_000_000

	// s4a21NoticeUpgradeLevel is the PVF reinforcement notice level
	// (etc/upgrade.etc [notice] = 12). An item at or above it broadcasts on
	// every attempt.
	s4a21NoticeUpgradeLevel = 12
	// s4a21NoticeUpgradeSlotStart/Count place the prepared targets in the main
	// equipment range.
	s4a21NoticeUpgradeSlotStart = 9
	s4a21NoticeUpgradeSlotCount = 8
	// s4a21NoticeUpgradeRefreshBelow refills when fewer targets remain.
	s4a21NoticeUpgradeRefreshBelow = 2

	// s4a21NoticeCubeItemID is cube clear (3037); the server consumes it from
	// the account virtual cube slot, so the request material slot is ignored.
	s4a21NoticeCubeItemID = 3037
	// s4a21NoticeCubeTarget covers thousands of +12 attempts (140 cubes each).
	s4a21NoticeCubeTarget = 500_000
	// s4a21NoticeCubeMinimum is the cube floor required by one trigger.
	s4a21NoticeCubeMinimum = 2_000

	s4a21NoticeWriteTimeout = 8 * time.Second
)

// noticeTriggerPlan is the adapter-owned layout of one ready trigger.
type noticeTriggerPlan struct {
	Kind         shared.ServerNoticeKind
	Slot         int16
	TargetItemID int32
	MaterialSlot int16
	TicketSlot   int16
}

// noticeCore is the subset of the 99-byte item core this adapter reads and
// writes for notice stock.
type noticeCore struct {
	kind       byte
	itemID     int
	count      int
	upgrade    int
	durability int
}

func parseNoticeCore(raw []byte) (noticeCore, bool) {
	if len(raw) < a21ItemCoreSize {
		return noticeCore{}, false
	}
	core := noticeCore{
		kind:       raw[0],
		itemID:     int(int32(binary.LittleEndian.Uint32(raw[1:5]))),
		count:      int(int32(binary.LittleEndian.Uint32(raw[5:9]))),
		upgrade:    int(raw[9]),
		durability: int(binary.LittleEndian.Uint16(raw[10:12])),
	}
	if core.itemID <= 0 && core.kind == 0 {
		return noticeCore{}, false
	}
	return core, true
}

// noticeCoreBytes mirrors a21ItemCore without the catalog dependency. Stackable
// items keep their stack count in the value field; equipment keeps durability.
func noticeCoreBytes(kind byte, itemID, count, upgrade, durability int) []byte {
	core := make([]byte, a21ItemCoreSize)
	core[0] = kind
	binary.LittleEndian.PutUint32(core[1:5], uint32(int32(itemID)))
	binary.LittleEndian.PutUint32(core[5:9], uint32(int32(count)))
	if upgrade < 0 {
		upgrade = 0
	}
	if upgrade > 31 {
		upgrade = 31
	}
	core[9] = byte(upgrade)
	if kind == a21ItemKindEquipment && durability > 0 {
		if durability > math.MaxUint16 {
			durability = math.MaxUint16
		}
		binary.LittleEndian.PutUint16(core[10:12], uint16(durability))
	}
	binary.LittleEndian.PutUint32(core[21:25], math.MaxUint32)
	core[66] = 0xFF
	return core
}

func (s *ServerNoticeStock) equipmentTypeByID() map[int]int {
	if s != nil && s.typeByID != nil {
		return s.typeByID
	}
	if s == nil {
		return nil
	}
	return buildNoticeTypeIndex(s.Equipment)
}

// isUpgradeTargetType mirrors the server's EquipmentType.IsUpgradeTargetType:
// weapons, armor, accessories and special equipment.
func isUpgradeTargetType(itemType int) bool {
	switch itemType {
	case 1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12:
		return true
	default:
		return false
	}
}

// noticeQueryer is satisfied by *sql.DB and *sql.Tx.
type noticeQueryer interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// mainNoticeCores loads one main-list slot window into slot -> core.
func mainNoticeCores(ctx context.Context, q noticeQueryer, cid, start, end int) (map[int]noticeCore, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT slot_index, item_core FROM character_inventory_items
		WHERE character_id=? AND list_type=0 AND slot_index BETWEEN ? AND ?`,
		cid, start, end)
	if err != nil {
		return nil, fmt.Errorf("read S4A21 server notice slots cid=%d: %w", cid, err)
	}
	defer rows.Close()
	cores := make(map[int]noticeCore)
	for rows.Next() {
		var slot int
		var raw []byte
		if err := rows.Scan(&slot, &raw); err != nil {
			return nil, fmt.Errorf("scan S4A21 server notice slot cid=%d: %w", cid, err)
		}
		if core, ok := parseNoticeCore(raw); ok {
			cores[slot] = core
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate S4A21 server notice slots cid=%d: %w", cid, err)
	}
	return cores, nil
}

func (s ServerNoticeStock) db(ctx context.Context) (*sql.DB, error) {
	if strings.TrimSpace(s.DatabasePath) == "" {
		return nil, fmt.Errorf("S4A21 server notice database path is empty")
	}
	db, err := sql.Open("sqlite", s.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open S4A21 server notice database: %w", err)
	}
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure S4A21 server notice database: %w", err)
	}
	return db, nil
}

// EnsureServerNoticeStock writes everything one kind needs while the account is
// offline. It is idempotent and preserves higher existing gold and cube counts.
func (s ServerNoticeStock) EnsureServerNoticeStock(cid int, kind shared.ServerNoticeKind) error {
	if cid <= 0 {
		return fmt.Errorf("S4A21 server notice character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s4a21NoticeWriteTimeout)
	defer cancel()
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	var deleteFlag int
	if err := db.QueryRowContext(ctx, `SELECT delete_flag FROM characters WHERE character_id=?`, cid).Scan(&deleteFlag); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("S4A21 server notice character %d does not exist", cid)
		}
		return fmt.Errorf("read S4A21 server notice character %d: %w", cid, err)
	}
	if deleteFlag != 0 {
		return fmt.Errorf("S4A21 server notice character %d is deleted", cid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin S4A21 server notice write: %w", err)
	}
	defer tx.Rollback()
	if err := s.ensureGold(ctx, tx, cid); err != nil {
		return err
	}
	switch kind {
	case shared.ServerNoticeLottery:
		if err := s.ensureLotteryBox(ctx, tx, cid); err != nil {
			return err
		}
	case shared.ServerNoticeUpgrade:
		if err := s.ensureUpgradeTargets(ctx, tx, cid); err != nil {
			return err
		}
		if err := s.ensureCubeClear(ctx, tx, cid); err != nil {
			return err
		}
	default:
		return fmt.Errorf("S4A21 server notice kind %q is unsupported", kind)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit S4A21 server notice cid=%d kind=%s: %w", cid, kind.Name(), err)
	}
	return nil
}

// ensureGold upserts the character gold virtual slot (kind 11, item id 0).
func (s ServerNoticeStock) ensureGold(ctx context.Context, tx *sql.Tx, cid int) error {
	cores, err := mainNoticeCores(ctx, tx, cid, s4a21NoticeGoldSlot, s4a21NoticeGoldSlot)
	if err != nil {
		return err
	}
	if cores[s4a21NoticeGoldSlot].count >= s4a21NoticeGoldTarget {
		return nil
	}
	core := noticeCoreBytes(a21ItemKindSpecialMaterial, s4a21NoticeGoldSlot, s4a21NoticeGoldTarget, 0, 0)
	if err := upsertNoticeSlot(ctx, tx, cid, s4a21NoticeGoldSlot, core); err != nil {
		return fmt.Errorf("write S4A21 server notice gold cid=%d: %w", cid, err)
	}
	return nil
}

// ensureLotteryBox writes a full box stack into a consumable-range slot.
func (s ServerNoticeStock) ensureLotteryBox(ctx context.Context, tx *sql.Tx, cid int) error {
	cores, err := mainNoticeCores(ctx, tx, cid, s4a21NoticeLotterySlot, s4a21NoticeLotterySlot+55)
	if err != nil {
		return err
	}
	boxSlot := -1
	for slot := s4a21NoticeLotterySlot; slot <= s4a21NoticeLotterySlot+55; slot++ {
		core, occupied := cores[slot]
		if !occupied {
			if boxSlot < 0 {
				boxSlot = slot
			}
			continue
		}
		if core.itemID == s4a21NoticeLotteryItemID {
			if core.count >= s4a21NoticeLotteryRefreshBelow {
				return nil
			}
			boxSlot = slot
			break
		}
	}
	if boxSlot < 0 {
		return fmt.Errorf("write S4A21 server notice box cid=%d: consumable slots are full", cid)
	}
	core := noticeCoreBytes(a21ItemKindConsumable, s4a21NoticeLotteryItemID, s4a21NoticeLotteryStack, 0, 0)
	if err := upsertNoticeSlot(ctx, tx, cid, boxSlot, core); err != nil {
		return fmt.Errorf("write S4A21 server notice box cid=%d slot=%d: %w", cid, boxSlot, err)
	}
	return nil
}

// ensureUpgradeTargets keeps a set of +12 equipment in the main equipment
// range. Targets clone the character's own equipped gear when possible so the
// item metadata, durability and job restrictions all match this server's PVF.
func (s ServerNoticeStock) ensureUpgradeTargets(ctx context.Context, tx *sql.Tx, cid int) error {
	cores, err := mainNoticeCores(ctx, tx, cid, s4a21NoticeUpgradeSlotStart, s4a21NoticeUpgradeSlotStart+55)
	if err != nil {
		return err
	}
	typeByID := s.equipmentTypeByID()
	valid := 0
	for _, core := range cores {
		if core.kind == a21ItemKindEquipment && core.upgrade >= s4a21NoticeUpgradeLevel && isUpgradeTargetType(typeByID[core.itemID]) {
			valid++
		}
	}
	if valid >= s4a21NoticeUpgradeRefreshBelow {
		return nil
	}
	var freeSlots []int
	for slot := s4a21NoticeUpgradeSlotStart; slot <= s4a21NoticeUpgradeSlotStart+55; slot++ {
		if _, occupied := cores[slot]; !occupied {
			freeSlots = append(freeSlots, slot)
		}
	}
	if len(freeSlots) == 0 {
		return nil
	}
	source, ok := s.cloneSource(ctx, tx, cid, typeByID)
	if !ok {
		return fmt.Errorf("write S4A21 server notice targets cid=%d: no upgradeable equipment source", cid)
	}
	need := s4a21NoticeUpgradeSlotCount - valid
	for index := 0; index < need && index < len(freeSlots); index++ {
		core := noticeCoreBytes(a21ItemKindEquipment, source.itemID, source.count, s4a21NoticeUpgradeLevel, source.durability)
		if err := upsertNoticeSlot(ctx, tx, cid, freeSlots[index], core); err != nil {
			return fmt.Errorf("write S4A21 server notice target cid=%d slot=%d: %w", cid, freeSlots[index], err)
		}
	}
	return nil
}

// cloneSource prefers the character's equipped gear; the catalog fallback
// keeps the first upgradeable item with durability.
func (s ServerNoticeStock) cloneSource(ctx context.Context, tx *sql.Tx, cid int, typeByID map[int]int) (noticeCore, bool) {
	rows, err := tx.QueryContext(ctx, `
		SELECT item_core FROM character_inventory_items
		WHERE character_id=? AND list_type=? AND slot_index BETWEEN 0 AND 28`,
		cid, a21ListTypeEquipment)
	if err == nil {
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				break
			}
			core, ok := parseNoticeCore(raw)
			if !ok || core.kind != a21ItemKindEquipment || !isUpgradeTargetType(typeByID[core.itemID]) {
				continue
			}
			if core.durability <= 0 {
				continue
			}
			rows.Close()
			return core, true
		}
		rows.Close()
	}
	for _, item := range s.Equipment {
		if item.ID <= 0 || !isUpgradeTargetType(item.ItemType) || item.Durability <= 0 || item.ClientIncompatible {
			continue
		}
		return noticeCore{kind: a21ItemKindEquipment, itemID: item.ID, count: 1, durability: item.Durability}, true
	}
	return noticeCore{}, false
}

// ensureCubeClear tops up the account cube_clear count. The server consumes
// cube costs from the account virtual slot, so the request material slot is
// ignored for cube materials.
func (s ServerNoticeStock) ensureCubeClear(ctx context.Context, tx *sql.Tx, cid int) error {
	var accountID, current int
	if err := tx.QueryRowContext(ctx, `
		SELECT c.account_id, COALESCE(a.cube_clear, 0)
		FROM characters c LEFT JOIN accounts a ON a.account_id=c.account_id
		WHERE c.character_id=?`, cid).Scan(&accountID, &current); err != nil {
		return fmt.Errorf("read S4A21 server notice cube cid=%d: %w", cid, err)
	}
	if current >= s4a21NoticeCubeTarget {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET cube_clear=? WHERE account_id=?`, s4a21NoticeCubeTarget, accountID); err != nil {
		return fmt.Errorf("write S4A21 server notice cube cid=%d account=%d: %w", cid, accountID, err)
	}
	return nil
}

// ServerNoticeStockReady reports whether the live stock still covers one
// trigger, so the scheduler can skip the offline cycle.
func (s ServerNoticeStock) ServerNoticeStockReady(cid int, kind shared.ServerNoticeKind) (bool, error) {
	if cid <= 0 {
		return false, fmt.Errorf("S4A21 server notice character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s4a21NoticeWriteTimeout)
	defer cancel()
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	defer db.Close()
	gold, err := mainNoticeCores(ctx, db, cid, s4a21NoticeGoldSlot, s4a21NoticeGoldSlot)
	if err != nil {
		return false, err
	}
	if gold[s4a21NoticeGoldSlot].count < s4a21NoticeGoldMinimum {
		return false, nil
	}
	switch kind {
	case shared.ServerNoticeLottery:
		cores, err := mainNoticeCores(ctx, db, cid, s4a21NoticeLotterySlot, s4a21NoticeLotterySlot+55)
		if err != nil {
			return false, err
		}
		for _, core := range cores {
			if core.itemID == s4a21NoticeLotteryItemID && core.count > 0 {
				return true, nil
			}
		}
		return false, nil
	case shared.ServerNoticeUpgrade:
		cores, err := mainNoticeCores(ctx, db, cid, s4a21NoticeUpgradeSlotStart, s4a21NoticeUpgradeSlotStart+55)
		if err != nil {
			return false, err
		}
		typeByID := s.equipmentTypeByID()
		hasTarget := false
		for _, core := range cores {
			if core.kind == a21ItemKindEquipment && core.upgrade >= s4a21NoticeUpgradeLevel && isUpgradeTargetType(typeByID[core.itemID]) {
				hasTarget = true
				break
			}
		}
		if !hasTarget {
			return false, nil
		}
		var cubes int
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(a.cube_clear,0) FROM characters c
			LEFT JOIN accounts a ON a.account_id=c.account_id
			WHERE c.character_id=?`, cid).Scan(&cubes); err != nil {
			return false, fmt.Errorf("probe S4A21 server notice cube cid=%d: %w", cid, err)
		}
		return cubes >= s4a21NoticeCubeMinimum, nil
	default:
		return false, fmt.Errorf("S4A21 server notice kind %q is unsupported", kind)
	}
}

// TriggerPlan resolves the live slot layout for one kind. It reads the same
// rows the scheduler's trigger then sends in the protocol request.
func (s ServerNoticeStock) TriggerPlan(cid int, kind shared.ServerNoticeKind) (noticeTriggerPlan, bool, error) {
	if cid <= 0 {
		return noticeTriggerPlan{}, false, fmt.Errorf("S4A21 server notice character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s4a21NoticeWriteTimeout)
	defer cancel()
	db, err := s.db(ctx)
	if err != nil {
		return noticeTriggerPlan{}, false, err
	}
	defer db.Close()
	switch kind {
	case shared.ServerNoticeLottery:
		cores, err := mainNoticeCores(ctx, db, cid, s4a21NoticeLotterySlot, s4a21NoticeLotterySlot+55)
		if err != nil {
			return noticeTriggerPlan{}, false, err
		}
		best := -1
		for slot, core := range cores {
			if core.itemID != s4a21NoticeLotteryItemID || core.count <= 0 {
				continue
			}
			if best < 0 || slot < best {
				best = slot
			}
		}
		if best < 0 {
			return noticeTriggerPlan{}, false, nil
		}
		return noticeTriggerPlan{Kind: kind, Slot: int16(best)}, true, nil
	case shared.ServerNoticeUpgrade:
		cores, err := mainNoticeCores(ctx, db, cid, s4a21NoticeUpgradeSlotStart, s4a21NoticeUpgradeSlotStart+55)
		if err != nil {
			return noticeTriggerPlan{}, false, err
		}
		typeByID := s.equipmentTypeByID()
		best := -1
		target := 0
		for slot, core := range cores {
			if core.kind != a21ItemKindEquipment || core.upgrade < s4a21NoticeUpgradeLevel || !isUpgradeTargetType(typeByID[core.itemID]) {
				continue
			}
			if best < 0 || slot < best {
				best, target = slot, core.itemID
			}
		}
		if best < 0 {
			return noticeTriggerPlan{}, false, nil
		}
		return noticeTriggerPlan{
			Kind: kind, Slot: int16(best), TargetItemID: int32(target),
			MaterialSlot: -1, TicketSlot: -1,
		}, true, nil
	default:
		return noticeTriggerPlan{}, false, fmt.Errorf("S4A21 server notice kind %q is unsupported", kind)
	}
}

// upsertNoticeSlot writes one item core into a main-list slot.
func upsertNoticeSlot(ctx context.Context, tx *sql.Tx, cid, slot int, core []byte) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO character_inventory_items (character_id, list_type, slot_index, item_core, created_at, updated_at)
		VALUES (?, 0, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(character_id, list_type, slot_index) DO UPDATE SET
			item_core=excluded.item_core, updated_at=CURRENT_TIMESTAMP`,
		cid, slot, core)
	return err
}

var _ shared.ServerNoticeStockWriter = ServerNoticeStock{}
