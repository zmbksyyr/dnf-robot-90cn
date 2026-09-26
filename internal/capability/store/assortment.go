package store

import (
	"math/rand"
	"sort"
	"strings"

	equipcap "robot/internal/capability/equipment"
	"robot/internal/shared"
)

const (
	// Private stores without a shop doll accept display indexes 0..6.
	// Field verification found three reliable material positions and four
	// equipment positions, so generated stores intentionally use a 3+4 layout.
	StoreMaterialSlots   = 3
	StoreEquipmentSlots  = 4
	StoreTotalPriceLimit = 500000000
)

type PoolEntry struct {
	Item      shared.EquipmentCatalogItem
	SlotBytes [61]byte
}

type ItemPool struct {
	Materials []PoolEntry
	Equipment []PoolEntry
}

func BuildItemPool(equipment, stackable []shared.EquipmentCatalogItem, intensifyMin, intensifyMax int) *ItemPool {
	if intensifyMin < 0 {
		intensifyMin = 0
	}
	if intensifyMax < intensifyMin {
		intensifyMax = intensifyMin
	}
	if intensifyMin > 255 {
		intensifyMin = 255
	}
	if intensifyMax > 255 {
		intensifyMax = 255
	}
	pool := &ItemPool{}
	seenStackable := make(map[int]struct{})
	for _, item := range stackable {
		if !isStoreMaterial(item) || !storeTradeAllowed(item) {
			continue
		}
		if _, exists := seenStackable[item.ID]; exists {
			continue
		}
		if item.StackLimit == 1 {
			continue
		}
		seenStackable[item.ID] = struct{}{}
		pool.Materials = append(pool.Materials, PoolEntry{Item: item})
	}

	seenEquipment := make(map[int]struct{})
	for _, item := range equipment {
		// The tested store validator accepts equipment types 1..10.
		// Later support/magic-stone types can exist in PVF but are rejected by
		// CPrivateStore::CheckValidItem with CMD 90 error 0x11.
		if item.ID <= 0 || item.ItemType < 1 || item.ItemType > 10 || item.Expire || !shared.ClientCompatibleEquipment(item) || item.NoTrade || item.TradeBlock {
			continue
		}
		if item.CanTrade != nil && !*item.CanTrade {
			continue
		}
		attach := strings.ToLower(strings.TrimSpace(item.Attach))
		if !strings.Contains(attach, "sealing") {
			continue
		}
		if _, exists := seenEquipment[item.ID]; exists {
			continue
		}
		seenEquipment[item.ID] = struct{}{}
		entry := PoolEntry{Item: item}
		rng := rand.New(rand.NewSource(int64(item.ID)))
		intensify := intensifyMin
		if intensifyMax > intensifyMin {
			intensify += rng.Intn(intensifyMax - intensifyMin + 1)
		}
		equipcap.WriteStoreEquipSlot(entry.SlotBytes[:], item, rng, intensify)
		pool.Equipment = append(pool.Equipment, entry)
	}
	sort.Slice(pool.Materials, func(i, j int) bool { return pool.Materials[i].Item.ID < pool.Materials[j].Item.ID })
	sort.Slice(pool.Equipment, func(i, j int) bool { return pool.Equipment[i].Item.ID < pool.Equipment[j].Item.ID })
	return pool
}

func (p *ItemPool) Draw(uid int) (materials, equipment []PoolEntry) {
	if p == nil {
		return nil, nil
	}
	seed := int64(uid)*0x5deece66d + 0xb
	rng := rand.New(rand.NewSource(seed))
	return drawPoolEntries(p.Materials, StoreMaterialSlots, rng), drawPoolEntries(p.Equipment, StoreEquipmentSlots, rng)
}

func drawPoolEntries(source []PoolEntry, count int, rng *rand.Rand) []PoolEntry {
	if count <= 0 || len(source) == 0 {
		return nil
	}
	if count > len(source) {
		count = len(source)
	}
	out := make([]PoolEntry, 0, count)
	selected := make(map[int]struct{}, count)
	for len(out) < count {
		index := rng.Intn(len(source))
		if _, exists := selected[index]; exists {
			continue
		}
		selected[index] = struct{}{}
		out = append(out, source[index])
	}
	return out
}

func isStoreMaterial(item shared.EquipmentCatalogItem) bool {
	if item.ID <= 0 || item.Expire || item.NoTrade || item.TradeBlock {
		return false
	}
	// The tested store validator rejects indirect stackables with 0x11 even
	// when PVF marks them tradeable. Only basic free materials are field-verified.
	if !item.BasicMaterial || !strings.EqualFold(strings.TrimSpace(item.Attach), "free") {
		return false
	}
	slot := strings.ToLower(strings.TrimSpace(item.Slot))
	path := strings.ToLower(strings.TrimSpace(item.Path))
	// Professional materials belong to inventory type 10. Monster cards can
	// carry an expert-job label but are verified in the normal material bag.
	if strings.Contains(path, "professional/") {
		return false
	}
	icon := strings.ToLower(strings.TrimSpace(item.Icon))
	if !strings.Contains(icon, "stackable/material.img") && !strings.Contains(icon, "monstercard") {
		return false
	}
	return strings.Contains(slot, "material")
}

func storeTradeAllowed(item shared.EquipmentCatalogItem) bool {
	if item.CanTrade != nil && !*item.CanTrade {
		return false
	}
	attach := strings.ToLower(strings.TrimSpace(item.Attach))
	if attach == "" {
		return item.Trade
	}
	return attach == "trade" || attach == "free" || attach == "sealing" || attach == "sealing trade"
}
