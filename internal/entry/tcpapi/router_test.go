package tcpapi

import (
	"strings"

	robotcap "robot/internal/capability/robot"
	"robot/internal/scheduler"
	"testing"
)

func TestRequiresGameRuntime(t *testing.T) {
	if !RequiresGameRuntime("robotsOnline") {
		t.Fatal("robotsOnline should require the backend game runtime")
	}
	if RequiresGameRuntime("sys") {
		t.Fatal("sys should not require the backend game runtime")
	}
}

func TestExtractTagContent(t *testing.T) {
	pkt := `<tw><c>sys</c><json>{"ok":true}</json></tw>`
	if got := extractTagContent(pkt, "c"); got != "sys" {
		t.Fatalf("command=%q, want sys", got)
	}
	if got := extractPayload(pkt); got != `{"ok":true}` {
		t.Fatalf("payload=%q", got)
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
