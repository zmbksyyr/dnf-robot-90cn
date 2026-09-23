package s4a21

import (
	"context"
	"fmt"

	capabilitypvf "robot/internal/capability/pvf"
	"robot/internal/shared"
)

type TownMapCatalogProvider struct {
	PVFPath string
}

func (p TownMapCatalogProvider) TownMapCatalog(ctx context.Context) ([]shared.MapCatalogItem, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return ReadTownMapCatalog(p.PVFPath)
}

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

func ReadItemCatalogs(pvfPath string) ([]shared.EquipmentCatalogItem, []shared.EquipmentCatalogItem, error) {
	archive, err := openA21PVF(pvfPath)
	if err != nil {
		return nil, nil, err
	}
	equipment, stackable, err := capabilitypvf.ProjectItemCatalogs(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("project S4A21 item catalogs: %w", err)
	}
	return equipment, stackable, nil
}

var _ shared.TownMapCatalogProvider = TownMapCatalogProvider{}
