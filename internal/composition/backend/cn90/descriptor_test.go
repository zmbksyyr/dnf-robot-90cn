package cn90

import (
	"testing"

	"robot/internal/shared"
)

func TestMetadataReflectsVerifiedCapabilities(t *testing.T) {
	info := Info()
	if info.ID != BackendID || !info.Selectable {
		t.Fatalf("90CN metadata = %+v", info)
	}
	// Only the town-move protocol is implemented at this stage; every other
	// capability must stay disabled with an explicit reason until its
	// protocol/persistence stage lands.
	if !info.Supports(shared.CapabilityTownMove) {
		t.Fatalf("town move capability is disabled: %+v", info.Capabilities[shared.CapabilityTownMove])
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityProvision, shared.CapabilityDungeonFollow, shared.CapabilityParty, shared.CapabilityGuildInvite,
		shared.CapabilityShout, shared.CapabilityDatabase, shared.CapabilityCleanup, shared.CapabilityDangerousDelete,
		shared.CapabilityStore,
	} {
		if info.Supports(capability) || info.Capabilities[capability].Reason == "" {
			t.Fatalf("%s must remain disabled with reason: %+v", capability, info.Capabilities[capability])
		}
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
		t.Fatal("90CN settings are missing")
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
