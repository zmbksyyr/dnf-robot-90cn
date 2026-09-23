package s4a21

import (
	"testing"

	"robot/internal/shared"
)

func TestMetadataReflectsVerifiedCapabilities(t *testing.T) {
	info := Info()
	if info.ID != shared.BackendS4A21 || !info.Selectable {
		t.Fatalf("S4A21 metadata = %+v", info)
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityProvision, shared.CapabilityTownMove, shared.CapabilityDungeonFollow,
		shared.CapabilityShout, shared.CapabilityDatabase, shared.CapabilityCleanup,
	} {
		if !info.Supports(capability) {
			t.Fatalf("verified capability %s is disabled", capability)
		}
	}
	if info.Capabilities[shared.CapabilityDungeonFollow].Mode != "toggle" {
		t.Fatalf("dungeon follower mode = %+v", info.Capabilities[shared.CapabilityDungeonFollow])
	}
	if info.Capabilities[shared.CapabilityDatabase].Mode != "sqlite_health" {
		t.Fatalf("database mode = %+v", info.Capabilities[shared.CapabilityDatabase])
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityDungeonMove, shared.CapabilityWorldShout, shared.CapabilityParty,
		shared.CapabilitySkill, shared.CapabilityStore, shared.CapabilityMarket,
		shared.CapabilityCompatibility, shared.CapabilityKeypair, shared.CapabilityDiagnostics,
		shared.CapabilitySystemAnnouncement, shared.CapabilityServiceControl,
	} {
		if info.Supports(capability) || info.Capabilities[capability].Reason == "" {
			t.Fatalf("%s must remain disabled with reason: %+v", capability, info.Capabilities[capability])
		}
	}
}
