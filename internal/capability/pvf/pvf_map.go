package pvf

import (
	"fmt"
	"strings"

	"robot/internal/shared"
)

type TownTextArchive interface {
	ReadText(path string) (string, error)
}

// ReadTownMapCatalog projects one PVF archive into the backend-neutral town
// map model consumed by robot spawn and movement policies.
func ReadTownMapCatalog(pvfPath string) ([]shared.MapCatalogItem, error) {
	if strings.TrimSpace(pvfPath) == "" {
		return nil, fmt.Errorf("PVF path is required")
	}
	archive, err := openPVF(pvfPath)
	if err != nil {
		return nil, fmt.Errorf("open PVF town catalog: %w", err)
	}
	maps := extractMapList(archive, "town/town.lst", "town/")
	if len(maps) == 0 {
		return nil, fmt.Errorf("PVF town catalog is empty")
	}
	return maps, nil
}

// ProjectTownMapCatalog converts backend-specific PVF text access into the
// shared town map model without exposing archive details to movement policy.
func ProjectTownMapCatalog(archive TownTextArchive) ([]shared.MapCatalogItem, error) {
	if archive == nil {
		return nil, fmt.Errorf("PVF town text archive is required")
	}
	maps := extractMapListFromText(func(path string) string {
		text, _ := archive.ReadText(path)
		return text
	}, "town/town.lst", "town/")
	if len(maps) == 0 {
		return nil, fmt.Errorf("PVF town catalog is empty")
	}
	return maps, nil
}

// ProjectItemCatalogs projects backend-specific PVF text access into the
// shared item model used by equipment and avatar selection policy.
func ProjectItemCatalogs(archive TownTextArchive) ([]shared.EquipmentCatalogItem, []shared.EquipmentCatalogItem, error) {
	if archive == nil {
		return nil, nil, fmt.Errorf("PVF item text archive is required")
	}
	readText := func(path string) string {
		text, _ := archive.ReadText(path)
		return text
	}
	equipment := extractItemListFromText(readText, "equipment/equipment.lst", "equipment/", false)
	equipment = appendItemInfoCreatureArtifacts(equipment, readText("etc/iteminfo.dat"))
	stackable := extractItemListFromText(readText, "stackable/stackable.lst", "stackable/", true)
	if len(equipment) == 0 {
		return nil, nil, fmt.Errorf("PVF equipment catalog is empty")
	}
	return equipment, stackable, nil
}
