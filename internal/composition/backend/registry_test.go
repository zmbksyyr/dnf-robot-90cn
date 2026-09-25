package backend

import (
	"testing"

	"robot/internal/shared"
)

func TestSelectS4A21AcrossSupportedPlatforms(t *testing.T) {
	if _, err := Select("missing", "linux"); err == nil {
		t.Fatal("unknown backend must fail closed")
	}
	for _, platform := range []string{"linux", "windows"} {
		if info, err := Select(shared.BackendS4A21, platform); err != nil || !info.Selectable {
			t.Fatalf("S4A21 on %s = %+v, %v", platform, info, err)
		}
	}
}

func TestAvailableBackendsExposeCompleteCapabilityMatrix(t *testing.T) {
	want := []shared.BackendCapability{
		shared.CapabilityProvision,
		shared.CapabilityTownMove,
		shared.CapabilityDungeonMove,
		shared.CapabilityShout,
		shared.CapabilityWorldShout,
		shared.CapabilityStore,
		shared.CapabilityParty,
		shared.CapabilityGuildInvite,
		shared.CapabilityPartyCompatibility,
		shared.CapabilityPartyDebug,
		shared.CapabilitySkill,
		shared.CapabilityMarket,
		shared.CapabilityCleanup,
		shared.CapabilityMailboxGuard,
		shared.CapabilityMailNotification,
		shared.CapabilityKeypair,
		shared.CapabilityDatabase,
	}
	for _, backend := range Available() {
		if backend.MaxOnline <= 0 {
			t.Fatalf("backend %s omitted max online limit", backend.ID)
		}
		for _, capability := range want {
			if _, ok := backend.Capabilities[capability]; !ok {
				t.Fatalf("backend %s omitted capability %s", backend.ID, capability)
			}
		}
	}
}
