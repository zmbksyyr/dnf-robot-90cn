package shared

import (
	"errors"
	"strings"
	"testing"
)

func TestBackendCapabilitiesFailClosed(t *testing.T) {
	b := BackendInfo{ID: BackendNative, Capabilities: map[BackendCapability]CapabilityStatus{
		CapabilityShout: {Enabled: true},
		CapabilityParty: {Reason: "party protocol not implemented"},
	}}
	if err := b.Require(CapabilityShout); err != nil {
		t.Fatalf("supported shout: %v", err)
	}
	for _, operation := range []BackendCapability{CapabilityParty, CapabilitySkill} {
		err := b.Require(operation)
		var unsupported UnsupportedCapabilityError
		if !errors.As(err, &unsupported) || unsupported.Operation != operation {
			t.Fatalf("require %s = %v", operation, err)
		}
		if !strings.Contains(err.Error(), CodeBackendCapabilityUnsupported) {
			t.Fatalf("missing stable code: %v", err)
		}
	}
}

func TestS4A21MetadataReflectsVerifiedCapabilities(t *testing.T) {
	var found BackendInfo
	for _, info := range KnownBackends() {
		if info.ID == BackendS4A21 {
			found = info
			break
		}
	}
	if found.ID == "" || !found.Selectable {
		t.Fatalf("S4A21 metadata = %+v", found)
	}
	for _, capability := range []BackendCapability{CapabilityProvision, CapabilityTownMove, CapabilityShout} {
		if !found.Supports(capability) {
			t.Fatalf("verified capability %s is disabled", capability)
		}
	}
	if found.Supports(CapabilityDungeonMove) || found.Capabilities[CapabilityDungeonMove].Reason == "" {
		t.Fatalf("dungeon movement must remain disabled with reason: %+v", found.Capabilities[CapabilityDungeonMove])
	}
}
