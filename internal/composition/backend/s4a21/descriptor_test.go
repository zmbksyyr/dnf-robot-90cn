package s4a21

import (
	"testing"

	"robot/internal/shared"
)

func TestMetadataReflectsVerifiedCapabilities(t *testing.T) {
	info := Info()
	if info.ID != BackendID || !info.Selectable {
		t.Fatalf("S4A21 metadata = %+v", info)
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityProvision, shared.CapabilityTownMove, shared.CapabilityDungeonFollow, shared.CapabilityParty, shared.CapabilityGuildInvite,
		shared.CapabilityShout, shared.CapabilityDatabase, shared.CapabilityCleanup, shared.CapabilityDangerousDelete,
		shared.CapabilityStore,
	} {
		if !info.Supports(capability) {
			t.Fatalf("verified capability %s is disabled", capability)
		}
	}
	if info.Capabilities[shared.CapabilityStore].Mode != "expert_job" {
		t.Fatalf("store mode = %+v", info.Capabilities[shared.CapabilityStore])
	}
	if info.Capabilities[shared.CapabilityDungeonFollow].Mode != "auto_accept" {
		t.Fatalf("dungeon follower mode = %+v", info.Capabilities[shared.CapabilityDungeonFollow])
	}
	if info.Capabilities[shared.CapabilityDatabase].Mode != "sqlite_health" {
		t.Fatalf("database mode = %+v", info.Capabilities[shared.CapabilityDatabase])
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityDungeonMove, shared.CapabilityWorldShout,
		shared.CapabilityPartyDebug,
		shared.CapabilitySkill, shared.CapabilityMarket,
		shared.CapabilityDiagnostics,
		shared.CapabilityMailNotification,
		shared.CapabilitySystemAnnouncement, shared.CapabilityServiceControl,
	} {
		if info.Supports(capability) || info.Capabilities[capability].Reason == "" {
			t.Fatalf("%s must remain disabled with reason: %+v", capability, info.Capabilities[capability])
		}
	}
}

func TestDescriptorOwnsLocalizedSettingMetadata(t *testing.T) {
	settings := Info().Settings
	if len(settings) == 0 {
		t.Fatal("S4A21 settings are missing")
	}
	for _, setting := range settings {
		if setting.Key == "" || setting.Label == "" || setting.LabelZH == "" {
			t.Fatalf("setting metadata is incomplete: %+v", setting)
		}
		if setting.Hint == "" || setting.HintZH == "" {
			t.Fatalf("setting hint is incomplete: %+v", setting)
		}
	}
}
