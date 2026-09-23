package tcpapi

import (
	"reflect"
	"strings"
	"testing"

	"robot/internal/scheduler"
	"robot/internal/shared"
)

func TestManualMarketTargetsTreatsEmptyAsBothMarkets(t *testing.T) {
	if got, want := manualMarketTargets("  "), []string{"auction", "cera"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manualMarketTargets(empty)=%v, want %v", got, want)
	}
	if got, want := manualMarketTargets("auction"), []string{"auction"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manualMarketTargets(auction)=%v, want %v", got, want)
	}
}

func TestMarketCommandRejectsUnsupportedBackendBeforeAppLookup(t *testing.T) {
	manager := scheduler.NewRobotManager(nil, nil, nil)
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "unsupported in test backend"})
	manager.SetBackendRobotCreator(shared.BackendInfo{ID: shared.BackendS4A21, Capabilities: capabilities}, nil)
	response, handled := handleMarketCommand("marketStatus", "", manager)
	if !handled || !strings.Contains(response, shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("response=%q handled=%t", response, handled)
	}
	if strings.Contains(response, "market app is not initialized") {
		t.Fatalf("backend boundary was bypassed: %q", response)
	}
}
