package store

import (
	"encoding/binary"
	"fmt"
	"math"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type Preparer struct {
	Env        PreparationEnv
	WorldHorns *WorldHornCache
	Pool       *ItemPool
}

type PreparationEnv interface {
	EnsureStorePermissionRecord(uid, cid int) (PermissionStatus, error)
	LoadInventory(cid int) ([]byte, error)
	Logf(format string, args ...interface{})
	RandBetween(min, max int) int
	ReplaceStoreStall(uid int, title string, items []StallItem) (StallResult, error)
	SaveInventory(cid int, capacity int, raw []byte) error
	SaveInventoryRaw(cid int, raw []byte) error
	StoreTitle(uid int, rc robotconfig.RuntimeConfig) string
}

func (p Preparer) EnsureInventoryAndStall(info robotcap.Info, rc robotconfig.RuntimeConfig) error {
	if err := p.EnsureStorePermission(info.UID, info.CID); err != nil {
		return err
	}
	if p.Pool == nil {
		return fmt.Errorf("store item pool is not configured")
	}
	return p.preparePoolInventoryAndStall(info, rc)
}

func (p Preparer) preparePoolInventoryAndStall(info robotcap.Info, rc robotconfig.RuntimeConfig) error {
	env := p.Env
	invRaw, err := env.LoadInventory(info.CID)
	if err != nil || len(invRaw) < 249*61 {
		invRaw = make([]byte, 249*61)
	}
	// Equipment box indexes are encoded two positions after the configured bag
	// index. Material positions are already global inventory indexes. Clear the
	// two legacy +2 material positions as well so an older preparation cannot
	// leave duplicate stacks behind.
	clearInventoryRawRange(invRaw, rc.StoreEquipmentStartBox+2, StoreEquipmentSlots)
	clearInventoryRawRange(invRaw, rc.StoreMaterialStartBox, StoreMaterialSlots+2)

	materials, equipment := p.Pool.Draw(info.UID)
	stallItems := make([]StallItem, 0, len(materials)+len(equipment))
	pricedEquipment := make([]PoolEntry, 0, len(equipment))
	materialIndex := 0
	for _, entry := range materials {
		rawIndex := rc.StoreMaterialStartBox + materialIndex
		materialIndex++
		if rawIndex < 0 || rawIndex >= 249 {
			continue
		}
		count := storeMaterialCount(entry.Item)
		WriteInventoryStack(invRaw[rawIndex*61:(rawIndex+1)*61], entry.Item, count, 3)
		stallItems = append(stallItems, StallItem{Count: count})
	}
	materialRows := len(stallItems)
	for index, entry := range equipment {
		rawIndex := rc.StoreEquipmentStartBox + index + 2
		if rawIndex < 0 || rawIndex >= 249 {
			continue
		}
		copy(invRaw[rawIndex*61:(rawIndex+1)*61], entry.SlotBytes[:])
		stallItems = append(stallItems, StallItem{Count: 1})
		pricedEquipment = append(pricedEquipment, entry)
	}
	if len(stallItems) == 0 {
		env.Logf("[StorePrepare] uid=%d cid=%d pool_empty=1\n", info.UID, info.CID)
		return nil
	}
	assignStorePoolPrices(env, rc, stallItems[:materialRows], stallItems[materialRows:], pricedEquipment)
	if err := env.SaveInventory(info.CID, rc.InventoryCapacity, invRaw); err != nil {
		return err
	}
	p.WorldHorns.Invalidate(info.CID)
	title := env.StoreTitle(info.UID, rc)
	result, err := env.ReplaceStoreStall(info.UID, title, stallItems)
	if err != nil {
		return err
	}
	env.Logf("[StorePrepare] uid=%d cid=%d pool_material=%d pool_equipment=%d stall_rows=%d title=%s\n",
		info.UID, info.CID, materialIndex, len(equipment), result.StallRows, title)
	return nil
}

func storeMaterialCount(item shared.EquipmentCatalogItem) int {
	// Prefer the PVF-defined stack limit. Some archives omit the field or
	// export an invalid value; those materials use the requested safe default.
	count := item.StackLimit
	if count <= 0 || count > 1000 {
		count = 1000
	}
	return count
}

func clearInventoryRawRange(raw []byte, start, count int) {
	for index := 0; index < count; index++ {
		rawIndex := start + index
		if rawIndex >= 0 && rawIndex < 249 && (rawIndex+1)*61 <= len(raw) {
			clear(raw[rawIndex*61 : (rawIndex+1)*61])
		}
	}
}

func assignStorePoolPrices(env PreparationEnv, rc robotconfig.RuntimeConfig, materials, equipment []StallItem, equipmentEntries []PoolEntry) {
	assignStorePrices(env, materials, rc.StoreMaterialPriceMin, rc.StoreMaterialPriceMax)
	assignStoreEquipmentPrices(equipment, equipmentEntries, rc)

	totalPrice := storeItemsTotalPrice(materials) + storeItemsTotalPrice(equipment)
	if totalPrice <= StoreTotalPriceLimit {
		return
	}
	scaleStorePrices(materials, totalPrice)
	scaleStorePrices(equipment, totalPrice)
}

func assignStoreEquipmentPrices(items []StallItem, entries []PoolEntry, rc robotconfig.RuntimeConfig) {
	for index := range items {
		price := rc.StoreEquipmentPriceMin
		if index < len(entries) {
			entry := entries[index]
			price = storeEquipmentPrice(entry.Item.Level, entry.Item.Rarity, int(entry.SlotBytes[6]), rc)
		}
		if price <= 0 {
			price = 1
		}
		items[index].Price = price
	}
}

func storeEquipmentPrice(level, rarity, upgrade int, rc robotconfig.RuntimeConfig) int {
	minPrice, maxPrice := rc.StoreEquipmentPriceMin, rc.StoreEquipmentPriceMax
	if minPrice <= 0 {
		minPrice = 1
	}
	if maxPrice <= minPrice {
		return minPrice
	}
	levelScore := clampStorePriceScore(float64(level) / 70)
	rarityScore := clampStorePriceScore(float64(rarity) / 5)
	effectiveUpgrade := upgrade
	if effectiveUpgrade < 0 {
		effectiveUpgrade = 0
	}
	if upgrade > 10 {
		riskLevels := upgrade - 10
		effectiveUpgrade += riskLevels * riskLevels
	}
	upgradeScore := clampStorePriceScore(float64(effectiveUpgrade) / 22)
	levelWeight := float64(rc.StoreEquipmentLevelWeight)
	rarityWeight := float64(rc.StoreEquipmentRarityWeight)
	intensifyWeight := float64(rc.StoreEquipmentIntensifyWeight)
	totalWeight := levelWeight + rarityWeight + intensifyWeight
	if totalWeight <= 0 {
		levelWeight, rarityWeight, intensifyWeight, totalWeight = 35, 40, 25, 100
	}
	quality := (levelScore*levelWeight + rarityScore*rarityWeight + upgradeScore*intensifyWeight) / totalWeight
	price := float64(minPrice) * math.Pow(float64(maxPrice)/float64(minPrice), quality)
	if price >= float64(maxPrice) {
		return maxPrice
	}
	return int(price)
}

func clampStorePriceScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func assignStorePrices(env PreparationEnv, items []StallItem, minPrice, maxPrice int) {
	for index := range items {
		price := env.RandBetween(minPrice, maxPrice)
		if price <= 0 {
			price = 1
		}
		items[index].Price = price
	}
}

func scaleStorePrices(items []StallItem, totalPrice int64) {
	for index := range items {
		price := int(int64(items[index].Price) * StoreTotalPriceLimit / totalPrice)
		if price <= 0 {
			price = 1
		}
		items[index].Price = price
	}
}

func storeItemsTotalPrice(items []StallItem) int64 {
	total := int64(0)
	for _, item := range items {
		count := item.Count
		if count <= 0 {
			count = 1
		}
		total += int64(item.Price) * int64(count)
	}
	return total
}

func (p Preparer) EnsureStorePermission(uid, cid int) error {
	env := p.Env
	status, err := env.EnsureStorePermissionRecord(uid, cid)
	if err != nil {
		return err
	}
	env.Logf("[StorePrepare] uid=%d cid=%d permission premium=%d miles=%d prod_user=%d pu_user=%d event_entry=%d\n",
		uid, cid, status.Premium, status.Miles, status.ProdUser, status.PUUser, status.EventEntry)
	return nil
}

func (p Preparer) EnsureWorldHornByCID(cid int) error {
	return p.WorldHorns.Ensure(cid, func() error {
		return p.ensureWorldHornByCID(cid)
	})
}

func (p Preparer) ensureWorldHornByCID(cid int) error {
	invRaw, err := p.Env.LoadInventory(cid)
	if err != nil {
		return fmt.Errorf("world horn inventory cid=%d: %w", cid, err)
	}
	if len(invRaw) < 249*61 {
		return fmt.Errorf("world horn inventory blob is too short")
	}
	slot := invRaw[WorldHornRawIndex*61 : (WorldHornRawIndex+1)*61]
	itemID := int(binary.LittleEndian.Uint32(slot[2:6]))
	count := int(binary.LittleEndian.Uint32(slot[7:11]))
	if int(binary.BigEndian.Uint16(slot[0:2])) == InventoryTypeForBoxIndex(WorldHornBoxIndex) && itemID == WorldHornItemID && count > 0 {
		return nil
	}
	WriteInventoryStack(slot, shared.EquipmentCatalogItem{ID: WorldHornItemID}, WorldHornCount, InventoryTypeForBoxIndex(WorldHornBoxIndex))
	if err := p.Env.SaveInventoryRaw(cid, invRaw); err != nil {
		return fmt.Errorf("update world horn inventory cid=%d: %w", cid, err)
	}
	return nil
}
