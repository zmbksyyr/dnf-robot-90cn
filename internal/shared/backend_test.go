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
