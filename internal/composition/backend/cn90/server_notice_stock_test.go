package cn90

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"robot/internal/shared"
)

const serverNoticeStockTestSchema = `
CREATE TABLE accounts(account_id INTEGER PRIMARY KEY, m_id TEXT UNIQUE, cube_clear INTEGER NOT NULL DEFAULT 0);
CREATE TABLE characters(character_id INTEGER PRIMARY KEY, account_id INTEGER, name TEXT, delete_flag INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_inventory_items(item_uid INTEGER PRIMARY KEY AUTOINCREMENT, character_id INTEGER, list_type INTEGER, slot_index INTEGER, item_core BLOB, created_at TEXT, updated_at TEXT, UNIQUE(character_id,list_type,slot_index));
`

func newServerNoticeStockTestDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notice.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(serverNoticeStockTestSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id,cube_clear) VALUES(3,'robot-test',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,delete_flag) VALUES(7,3,'robot',0)`); err != nil {
		t.Fatal(err)
	}
	// The character's equipped weapon is the upgrade clone source.
	if _, err := db.Exec(`INSERT INTO character_inventory_items(character_id,list_type,slot_index,item_core) VALUES(7,3,0,?)`,
		noticeCoreBytes(cn90ItemKindEquipment, 1001, 1, 0, 120)); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestServerNoticeStockLotteryRoundTrip(t *testing.T) {
	path, db := newServerNoticeStockTestDB(t)
	stock := ServerNoticeStock{DatabasePath: path, Equipment: []shared.EquipmentCatalogItem{{ID: 1001, ItemType: 1, Durability: 120}}}

	if ready, err := stock.ServerNoticeStockReady(7, shared.ServerNoticeLottery); err != nil || ready {
		t.Fatalf("empty stock ready=%t err=%v", ready, err)
	}
	if err := stock.EnsureServerNoticeStock(7, shared.ServerNoticeLottery); err != nil {
		t.Fatal(err)
	}
	ready, err := stock.ServerNoticeStockReady(7, shared.ServerNoticeLottery)
	if err != nil || !ready {
		t.Fatalf("stocked ready=%t err=%v", ready, err)
	}
	plan, ok, err := stock.TriggerPlan(7, shared.ServerNoticeLottery)
	if err != nil || !ok {
		t.Fatalf("plan ok=%t err=%v", ok, err)
	}
	if plan.Slot != cn90NoticeLotterySlot {
		t.Fatalf("box slot = %d, want %d", plan.Slot, cn90NoticeLotterySlot)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT item_core FROM character_inventory_items WHERE character_id=7 AND list_type=0 AND slot_index=?`, plan.Slot).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	core, ok := parseNoticeCore(raw)
	if !ok || core.itemID != cn90NoticeLotteryItemID || core.count != cn90NoticeLotteryStack || core.kind != cn90ItemKindConsumable {
		t.Fatalf("box core = %+v ok=%t", core, ok)
	}
	// Gold alone must not keep the lottery ready without a box.
	if _, err := db.Exec(`UPDATE character_inventory_items SET item_core=? WHERE character_id=7 AND list_type=0 AND slot_index=?`,
		noticeCoreBytes(cn90ItemKindConsumable, cn90NoticeLotteryItemID, 0, 0, 0), plan.Slot); err != nil {
		t.Fatal(err)
	}
	if ready, err := stock.ServerNoticeStockReady(7, shared.ServerNoticeLottery); err != nil || ready {
		t.Fatalf("consumed stock ready=%t err=%v", ready, err)
	}
}

func TestServerNoticeStockUpgradeRoundTrip(t *testing.T) {
	path, db := newServerNoticeStockTestDB(t)
	stock := ServerNoticeStock{DatabasePath: path, Equipment: []shared.EquipmentCatalogItem{{ID: 1001, ItemType: 1, Durability: 120}}}

	// The equipped clone carries kind=equipment; the catalog type map keeps it
	// a valid upgrade target.
	if err := stock.EnsureServerNoticeStock(7, shared.ServerNoticeUpgrade); err != nil {
		t.Fatal(err)
	}
	ready, err := stock.ServerNoticeStockReady(7, shared.ServerNoticeUpgrade)
	if err != nil || !ready {
		t.Fatalf("stocked ready=%t err=%v", ready, err)
	}
	plan, ok, err := stock.TriggerPlan(7, shared.ServerNoticeUpgrade)
	if err != nil || !ok {
		t.Fatalf("plan ok=%t err=%v", ok, err)
	}
	if plan.TargetItemID != 1001 || plan.Slot != cn90NoticeUpgradeSlotStart {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.MaterialSlot != -1 || plan.TicketSlot != -1 {
		t.Fatalf("material/ticket slots = %d/%d, want -1/-1 (virtual cube path)", plan.MaterialSlot, plan.TicketSlot)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT item_core FROM character_inventory_items WHERE character_id=7 AND list_type=0 AND slot_index=?`, plan.Slot).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	core, ok := parseNoticeCore(raw)
	if !ok || core.kind != cn90ItemKindEquipment || core.upgrade != cn90NoticeUpgradeLevel || core.durability != 120 {
		t.Fatalf("target core = %+v ok=%t", core, ok)
	}
	var cubes int
	if err := db.QueryRow(`SELECT cube_clear FROM accounts WHERE account_id=3`).Scan(&cubes); err != nil {
		t.Fatal(err)
	}
	if cubes < cn90NoticeCubeMinimum {
		t.Fatalf("cube_clear = %d, want >= %d", cubes, cn90NoticeCubeMinimum)
	}
	gold := 0
	if err := db.QueryRow(`SELECT item_core FROM character_inventory_items WHERE character_id=7 AND list_type=0 AND slot_index=0`).Scan(&raw); err == nil {
		if core, ok := parseNoticeCore(raw); ok {
			gold = core.count
		}
	}
	if gold < cn90NoticeGoldMinimum {
		t.Fatalf("gold = %d, want >= %d", gold, cn90NoticeGoldMinimum)
	}
}

func TestServerNoticeStockRejectsUnknownCharacter(t *testing.T) {
	path, _ := newServerNoticeStockTestDB(t)
	stock := ServerNoticeStock{DatabasePath: path}
	if err := stock.EnsureServerNoticeStock(99, shared.ServerNoticeLottery); err == nil {
		t.Fatal("missing character accepted")
	}
	if _, err := stock.ServerNoticeStockReady(0, shared.ServerNoticeLottery); err == nil {
		t.Fatal("invalid cid accepted")
	}
	if _, _, err := stock.TriggerPlan(7, shared.ServerNoticeKind("bogus")); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestNoticeCoreBytesLayout(t *testing.T) {
	core := noticeCoreBytes(cn90ItemKindEquipment, 1234, 1, 12, 300)
	if len(core) != cn90ItemCoreSize {
		t.Fatalf("core length = %d", len(core))
	}
	parsed, ok := parseNoticeCore(core)
	if !ok || parsed.kind != cn90ItemKindEquipment || parsed.itemID != 1234 || parsed.upgrade != 12 || parsed.durability != 300 {
		t.Fatalf("parsed = %+v ok=%t", parsed, ok)
	}
}
