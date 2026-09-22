package s4a21

import (
	"fmt"

	capabilitypvf "robot/internal/capability/pvf"
	"robot/internal/shared"
)

func ReadTownMapCatalog(pvfPath string) ([]shared.MapCatalogItem, error) {
	archive, err := openA21PVF(pvfPath)
	if err != nil {
		return nil, err
	}
	maps, err := capabilitypvf.ProjectTownMapCatalog(archive)
	if err != nil {
		return nil, fmt.Errorf("project S4A21 town maps: %w", err)
	}
	return maps, nil
}
