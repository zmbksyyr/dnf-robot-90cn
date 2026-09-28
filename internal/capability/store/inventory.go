package store

import (
	"encoding/binary"

	"robot/internal/shared"
)

const (
	WorldHornItemID   = 36
	WorldHornCount    = 200
	WorldHornBoxIndex = 55
	WorldHornRawIndex = WorldHornBoxIndex + 2
)

type StallItem struct {
	Count int
	Price int
}

type StallResult struct {
	StallRows int
}

type PermissionStatus struct {
	Premium    int
	Miles      int
	ProdUser   int
	PUUser     int
	EventEntry int
}

func InventoryTypeForBoxIndex(boxIndex int) int {
	switch {
	case boxIndex >= 7 && boxIndex <= 54:
		return 1
	case boxIndex >= 55 && boxIndex <= 102:
		return 2
	case boxIndex >= 103 && boxIndex <= 150:
		return 3
	case boxIndex >= 151 && boxIndex <= 198:
		return 4
	case boxIndex >= 199 && boxIndex <= 246:
		return 10
	default:
		return 2
	}
}

func WriteInventoryStack(dst []byte, item shared.EquipmentCatalogItem, count int, inventoryType int) {
	if len(dst) < 61 {
		return
	}
	clear(dst)
	dst[0] = 0x00
	dst[1] = byte(inventoryType)
	binary.LittleEndian.PutUint32(dst[2:6], uint32(item.ID))
	binary.LittleEndian.PutUint32(dst[7:11], uint32(count))
}
