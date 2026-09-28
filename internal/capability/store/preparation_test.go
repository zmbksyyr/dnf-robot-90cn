package store

import (
	"encoding/binary"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestPreparePoolInventoryUsesVerifiedMaterialAndEquipmentSlots(t *testing.T) {
	pool := &ItemPool{}
	for id := 1; id <= 12; id++ {
		pool.Materials = append(pool.Materials, PoolEntry{
			Item: shared.EquipmentCatalogItem{ID: 3000 + id},
		})
		entry := PoolEntry{Item: shared.EquipmentCatalogItem{ID: 10000 + id}}
		entry.SlotBytes[1] = 1
		binary.LittleEndian.PutUint32(entry.SlotBytes[2:6], uint32(entry.Item.ID))
		entry.SlotBytes[6] = 13
		pool.Equipment = append(pool.Equipment, entry)
	}
	var saved []byte
	var stalls []StallItem
	initial := make([]byte, 249*61)
	for rawIndex := 105; rawIndex <= 109; rawIndex++ {
		WriteInventoryStack(initial[rawIndex*61:(rawIndex+1)*61], shared.EquipmentCatalogItem{ID: 9000 + rawIndex}, 1, 3)
	}
	env := testPreparationEnv{inventory: initial, saved: &saved, stalls: &stalls}
	preparer := Preparer{Env: env, Pool: pool, WorldHorns: NewWorldHornCache()}
	rc := robotconfig.Default()
	rc.StoreEquipmentStartBox = 7
	rc.StoreMaterialStartBox = 105
	if err := preparer.EnsureInventoryAndStall(robotcap.Info{UID: 17000001, CID: 1}, rc); err != nil {
		t.Fatal(err)
	}
	if len(stalls) != 7 {
		t.Fatalf("stall rows = %d, want 7", len(stalls))
	}
	if len(saved) != 249*61 {
		t.Fatalf("saved inventory bytes = %d", len(saved))
	}
	assertInventoryRawRangeType(t, saved, 9, 4, 1)
	assertInventoryRawRangeType(t, saved, 105, 3, 3)
	for rawIndex := 108; rawIndex <= 109; rawIndex++ {
		if itemID := binary.LittleEndian.Uint32(saved[rawIndex*61+2 : rawIndex*61+6]); itemID != 0 {
			t.Fatalf("legacy material raw position=%d item=%d, want empty", rawIndex, itemID)
		}
	}
	if got := countInventoryType(saved, 2); got != 0 {
		t.Fatalf("unexpected consumable inventory slots = %d", got)
	}
}

func TestPreparePoolInventoryUsesPVFMaterialStackLimitCappedAtOneThousand(t *testing.T) {
	tests := []struct {
		name       string
		stackLimit int
		want       int
	}{
		{name: "pvf limit", stackLimit: 200, want: 200},
		{name: "cap", stackLimit: 2000, want: 1000},
		{name: "missing", stackLimit: 0, want: 1000},
		{name: "invalid", stackLimit: -1, want: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := &ItemPool{Materials: []PoolEntry{{Item: shared.EquipmentCatalogItem{ID: 3037, StackLimit: tt.stackLimit}}}}
			var saved []byte
			var stalls []StallItem
			env := testPreparationEnv{saved: &saved, stalls: &stalls}
			preparer := Preparer{Env: env, Pool: pool, WorldHorns: NewWorldHornCache()}
			rc := robotconfig.Default()
			rc.StoreMaterialStartBox = 105

			if err := preparer.EnsureInventoryAndStall(robotcap.Info{UID: 17000001, CID: 1}, rc); err != nil {
				t.Fatal(err)
			}
			if len(stalls) != 1 || stalls[0].Count != tt.want {
				t.Fatalf("stall items = %+v, want one material with count %d", stalls, tt.want)
			}
			rawIndex := rc.StoreMaterialStartBox
			got := int(binary.LittleEndian.Uint32(saved[rawIndex*61+7 : rawIndex*61+11]))
			if got != tt.want {
				t.Fatalf("inventory count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPrepareInventoryRequiresCurrentItemPool(t *testing.T) {
	preparer := Preparer{Env: testPreparationEnv{}, WorldHorns: NewWorldHornCache()}
	if err := preparer.EnsureInventoryAndStall(robotcap.Info{UID: 17000001, CID: 1}, robotconfig.Default()); err == nil {
		t.Fatal("missing item pool unexpectedly used a legacy fallback")
	}
}

func countInventoryType(raw []byte, inventoryType int) int {
	count := 0
	for rawIndex := 0; rawIndex < 249; rawIndex++ {
		slot := raw[rawIndex*61 : (rawIndex+1)*61]
		if int(binary.BigEndian.Uint16(slot[:2])) == inventoryType && binary.LittleEndian.Uint32(slot[2:6]) != 0 {
			count++
		}
	}
	return count
}

func assertInventoryRawRangeType(t *testing.T, raw []byte, start, count, inventoryType int) {
	t.Helper()
	for index := 0; index < count; index++ {
		rawIndex := start + index
		slot := raw[rawIndex*61 : (rawIndex+1)*61]
		if int(binary.BigEndian.Uint16(slot[:2])) != inventoryType {
			t.Fatalf("raw position=%d inventory type=%d want=%d", rawIndex, binary.BigEndian.Uint16(slot[:2]), inventoryType)
		}
		if binary.LittleEndian.Uint32(slot[2:6]) == 0 {
			t.Fatalf("raw position=%d is empty", rawIndex)
		}
	}
}

func TestStorePoolPricesUseSeparateMaterialAndEquipmentRanges(t *testing.T) {
	env := testPreparationEnv{}
	materials := []StallItem{{Count: 1000}}
	equipment := []StallItem{{Count: 1}}
	rc := robotconfig.RuntimeConfig{
		StoreMaterialPriceMin:  10,
		StoreMaterialPriceMax:  50,
		StoreEquipmentPriceMin: 500000,
		StoreEquipmentPriceMax: 1000000,
	}
	assignStorePoolPrices(env, rc, materials, equipment, nil)
	if materials[0].Price != 10 || equipment[0].Price != 500000 {
		t.Fatalf("prices material=%d equipment=%d", materials[0].Price, equipment[0].Price)
	}
}

func TestStorePoolPricesScaleAllRowsWhenWholeDisplayExceedsLimit(t *testing.T) {
	env := testPreparationEnv{}
	materials := []StallItem{{Count: 1000}}
	equipment := []StallItem{{Count: 1}}
	rc := robotconfig.RuntimeConfig{
		StoreMaterialPriceMin:  1000000,
		StoreMaterialPriceMax:  1000000,
		StoreEquipmentPriceMin: 2000000,
		StoreEquipmentPriceMax: 2000000,
	}
	assignStorePoolPrices(env, rc, materials, equipment, nil)
	if total := storeItemsTotalPrice(materials) + storeItemsTotalPrice(equipment); total > StoreTotalPriceLimit {
		t.Fatalf("whole store total=%d exceeds limit=%d", total, StoreTotalPriceLimit)
	}
	if difference := equipment[0].Price - 2*materials[0].Price; difference < -1 || difference > 1 {
		t.Fatalf("proportional prices material=%d equipment=%d", materials[0].Price, equipment[0].Price)
	}
}

func TestStoreEquipmentPriceUsesLevelRarityAndUpgrade(t *testing.T) {
	const minPrice, maxPrice = 500000, 1000000
	rc := testStorePriceConfig(minPrice, maxPrice)
	base := storeEquipmentPrice(1, 0, 0, rc)
	highLevel := storeEquipmentPrice(70, 0, 0, rc)
	highRarity := storeEquipmentPrice(1, 5, 0, rc)
	highUpgrade := storeEquipmentPrice(1, 0, 13, rc)
	if highLevel <= base || highRarity <= base || highUpgrade <= base {
		t.Fatalf("prices base=%d level=%d rarity=%d upgrade=%d", base, highLevel, highRarity, highUpgrade)
	}
	if got := storeEquipmentPrice(70, 5, 13, rc); got != maxPrice {
		t.Fatalf("maximum quality price=%d want=%d", got, maxPrice)
	}
}

func TestStoreEquipmentPriceAddsUpgradeRiskPremiumAboveTen(t *testing.T) {
	const minPrice, maxPrice = 500000, 1000000
	rc := testStorePriceConfig(minPrice, maxPrice)
	price10 := storeEquipmentPrice(35, 2, 10, rc)
	price11 := storeEquipmentPrice(35, 2, 11, rc)
	price12 := storeEquipmentPrice(35, 2, 12, rc)
	if price11 <= price10 || price12-price11 <= price11-price10 {
		t.Fatalf("upgrade prices +10=%d +11=%d +12=%d", price10, price11, price12)
	}
}

func TestStoreEquipmentPriceClampsInputsAndRange(t *testing.T) {
	rc := testStorePriceConfig(500000, 1000000)
	if got := storeEquipmentPrice(-10, -2, -1, rc); got != 500000 {
		t.Fatalf("minimum price=%d want=500000", got)
	}
	if got := storeEquipmentPrice(999, 99, 255, rc); got != 1000000 {
		t.Fatalf("clamped maximum price=%d want=1000000", got)
	}
	rc.StoreEquipmentPriceMin, rc.StoreEquipmentPriceMax = 900, 800
	if got := storeEquipmentPrice(70, 5, 13, rc); got != 900 {
		t.Fatalf("invalid range price=%d want=900", got)
	}
}

func TestStoreEquipmentPriceNormalizesConfiguredWeights(t *testing.T) {
	percent := testStorePriceConfig(500000, 1000000)
	ratio := percent
	ratio.StoreEquipmentLevelWeight = 7
	ratio.StoreEquipmentRarityWeight = 8
	ratio.StoreEquipmentIntensifyWeight = 5
	if got, want := storeEquipmentPrice(52, 4, 11, ratio), storeEquipmentPrice(52, 4, 11, percent); got != want {
		t.Fatalf("normalized price=%d want=%d", got, want)
	}
}

func TestStorePoolPricesUseActualEquipmentMetadata(t *testing.T) {
	env := testPreparationEnv{}
	equipment := []StallItem{{Count: 1}, {Count: 1}}
	entries := []PoolEntry{
		{Item: shared.EquipmentCatalogItem{Level: 1, Rarity: 0}},
		{Item: shared.EquipmentCatalogItem{Level: 70, Rarity: 5}},
	}
	entries[0].SlotBytes[6] = 0
	entries[1].SlotBytes[6] = 13
	rc := testStorePriceConfig(500000, 1000000)
	assignStorePoolPrices(env, rc, nil, equipment, entries)
	if equipment[0].Price != storeEquipmentPrice(1, 0, 0, rc) || equipment[1].Price != 1000000 {
		t.Fatalf("equipment prices=%+v", equipment)
	}
}

func testStorePriceConfig(minPrice, maxPrice int) robotconfig.RuntimeConfig {
	return robotconfig.RuntimeConfig{
		StoreEquipmentPriceMin:        minPrice,
		StoreEquipmentPriceMax:        maxPrice,
		StoreEquipmentLevelWeight:     35,
		StoreEquipmentRarityWeight:    40,
		StoreEquipmentIntensifyWeight: 25,
	}
}

type testPreparationEnv struct {
	inventory []byte
	saved     *[]byte
	stalls    *[]StallItem
	randValue int
}

func (e testPreparationEnv) EnsureStorePermissionRecord(uid, cid int) (PermissionStatus, error) {
	return PermissionStatus{}, nil
}

func (e testPreparationEnv) LoadInventory(cid int) ([]byte, error) {
	return append([]byte(nil), e.inventory...), nil
}

func (e testPreparationEnv) Logf(format string, args ...interface{}) {}

func (e testPreparationEnv) RandBetween(min, max int) int {
	if e.randValue > 0 {
		return e.randValue
	}
	return min
}

func (e testPreparationEnv) ReplaceStoreStall(uid int, title string, items []StallItem) (StallResult, error) {
	if e.stalls != nil {
		*e.stalls = append([]StallItem(nil), items...)
	}
	return StallResult{StallRows: len(items)}, nil
}

func (e testPreparationEnv) SaveInventory(cid int, capacity int, raw []byte) error {
	if e.saved != nil {
		*e.saved = append([]byte(nil), raw...)
	}
	return nil
}

func (e testPreparationEnv) SaveInventoryRaw(cid int, raw []byte) error {
	return nil
}

func (e testPreparationEnv) StoreTitle(uid int, rc robotconfig.RuntimeConfig) string {
	return fallbackStoreTitle(uid)
}
