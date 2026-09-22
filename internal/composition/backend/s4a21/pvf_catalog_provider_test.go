package s4a21

import (
	"context"
	"errors"
	"testing"
)

func TestTownMapCatalogProviderHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (TownMapCatalogProvider{PVFPath: "missing.pvf"}).TownMapCatalog(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
