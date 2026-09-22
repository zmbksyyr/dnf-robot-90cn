package shared

import "context"

// TownMapCatalogProvider exposes backend-specific PVF decoding as the
// backend-neutral town map model consumed by movement policy.
type TownMapCatalogProvider interface {
	TownMapCatalog(context.Context) ([]MapCatalogItem, error)
}
