package tcpapi

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/scheduler"
	"robot/internal/shared"
)

type recordingGameGate struct{ called *bool }

func (g recordingGameGate) Check() error {
	*g.called = true
	return errors.New("game runtime unavailable")
}

func TestRequiresGameRuntime(t *testing.T) {
	if !RequiresGameRuntime("robotsOnline") {
		t.Fatal("robotsOnline should require the backend game runtime")
	}
	if RequiresGameRuntime("sys") {
		t.Fatal("sys should not require the backend game runtime")
	}
}

func TestParseRequestPacketIsAuthoritative(t *testing.T) {
	pkt := `<tw><c>sys</c><json>{"ok":true}</json></tw>`
	fields, err := parseRequestPacket(pkt)
	if err != nil {
		t.Fatalf("parseRequestPacket(%s) error: %v", pkt, err)
	}
	if got := fields["c"]; got != "sys" {
		t.Fatalf("command=%q, want sys", got)
	}
	if got := fields["json"]; got != `{"ok":true}` {
		t.Fatalf("payload=%q", got)
	}
}

func TestParseRequestPacketRejectsCommentInjection(t *testing.T) {
	tests := []string{
		`<tw><!-- <c>cleanupRobotsAsync</c> --><c>sys</c></tw>`,
		`<tw><c>sys<!-- <c>cleanupRobotsAsync</c> --></c></tw>`,
		`<tw><?target cleanupRobotsAsync?><c>sys</c></tw>`,
		`<tw><!DOCTYPE x><c>sys</c></tw>`,
	}
	for _, packet := range tests {
		if _, err := parseRequestPacket(packet); err == nil {
			t.Fatalf("parseRequestPacket accepted comment/injection packet %s", packet)
		}
	}
}

func TestDecodePayloadIgnoresCommentPayload(t *testing.T) {
	var target struct {
		Count int `json:"count"`
	}
	packet := `<tw><c>sys</c><json>{"count":1}</json><!-- <json>{"count":9}</json> --></tw>`
	if err := decodePayload(packet, &target); err == nil {
		t.Fatal("decodePayload accepted a comment-carrying packet")
	}
}

func TestCleanupRequiresAsync(t *testing.T) {
	if !cleanupRequiresAsync(robotcap.CleanupRequest{Force: true}) {
		t.Fatal("forced full cleanup should require async")
	}
	if cleanupRequiresAsync(robotcap.CleanupRequest{Force: false}) {
		t.Fatal("dry-run full cleanup should remain synchronous")
	}
	if cleanupRequiresAsync(robotcap.CleanupRequest{Force: true, UIDs: []int{1}}) {
		t.Fatal("scoped uid cleanup should remain synchronous")
	}
	if cleanupRequiresAsync(robotcap.CleanupRequest{Force: true, MinUID: 1, MaxUID: 10}) {
		t.Fatal("scoped range cleanup should remain synchronous")
	}
}

func TestDecodePayloadRejectsUnknownDuplicateAndTrailingJSON(t *testing.T) {
	tests := []string{
		`<tw><json>{"count":1,"unknown":2}</json></tw>`,
		`<tw><json>{"count":1,"count":2}</json></tw>`,
		`<tw><json>{"count":1}{"count":2}</json></tw>`,
	}
	for _, packet := range tests {
		var target struct {
			Count int `json:"count"`
		}
		if err := decodePayload(packet, &target); err == nil {
			t.Fatalf("decodePayload accepted %s", packet)
		}
	}
}

func TestHandlePacketReturnsErrorAfterPanic(t *testing.T) {
	response := HandlePacket("test", `<tw><c>autoStatus</c></tw>`, (*scheduler.RobotManager)(nil))
	if !strings.Contains(response, "internal robot command failure") {
		t.Fatalf("panic response = %q", response)
	}
}

func TestAsyncCommandRejectsUnsupportedCapabilityBeforeQueue(t *testing.T) {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "unsupported in test backend"})
	manager := scheduler.NewRobotManager(nil, nil, nil)
	manager.SetBackendRobotCreator(shared.BackendInfo{ID: shared.BackendID("test"), Capabilities: capabilities}, nil)
	gateCalled := false
	manager.SetGameCommandGate(recordingGameGate{called: &gateCalled})
	for _, command := range []string{"robotsStoreAsync", "cleanupRobotsAsync"} {
		packet := `<tw><c>` + command + `</c><json>{}</json></tw>`
		response := HandlePacket("127.0.0.1:1234", packet, manager)
		if !strings.Contains(response, shared.CodeBackendCapabilityUnsupported) ||
			strings.Contains(response, `"state":"queued"`) || strings.Contains(response, `"state":"running"`) {
			t.Fatalf("%s response=%q", command, response)
		}
	}
	if gateCalled {
		t.Fatal("game runtime gate ran before unsupported capability rejection")
	}
}

func TestCommandCapabilitiesCoverBackendSpecificActions(t *testing.T) {
	tests := map[string][]shared.BackendCapability{
		"createRobots":          {shared.CapabilityProvision},
		"robotsMove":            {shared.CapabilityTownMove},
		"robotsShout":           {shared.CapabilityWorldShout, shared.CapabilityShout},
		"robotsShoutLocal":      {shared.CapabilityShout},
		"robotsShoutWorld":      {shared.CapabilityWorldShout},
		"robotsStore":           {shared.CapabilityStore},
		"robotsStoreAsync":      {shared.CapabilityStore},
		"cleanupRobots":         {shared.CapabilityCleanup},
		"cleanupRobotsAsync":    {shared.CapabilityCleanup},
		"partySkillReload":      {shared.CapabilitySkill},
		"partyDebugStart":       {shared.CapabilityPartyDebug},
		"partyDebugStop":        {shared.CapabilityPartyDebug},
		"partyDebugStatus":      {shared.CapabilityPartyDebug},
		"systemAnnouncement":    {shared.CapabilitySystemAnnouncement},
		"dangerousDeleteUnlock": {shared.CapabilityDangerousDelete},
		"dangerousDeleteAsync":  {shared.CapabilityDangerousDelete},
	}
	for command, want := range tests {
		if got := commandCapabilities(command); !reflect.DeepEqual(got, want) {
			t.Fatalf("commandCapabilities(%q) = %q, want %q", command, got, want)
		}
	}
	if capabilities := commandCapabilities("dashboardStatus"); len(capabilities) != 0 {
		t.Fatalf("dashboardStatus unexpectedly requires %q", capabilities)
	}
}
