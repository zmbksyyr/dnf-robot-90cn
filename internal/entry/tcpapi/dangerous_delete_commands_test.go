package tcpapi

import (
	"strings"
	"testing"
	"time"

	"robot/internal/scheduler"
	"robot/internal/shared"
)

func TestDangerousDeleteCommandsRejectNonLoopbackClients(t *testing.T) {
	manager := scheduler.NewRobotManager(nil, nil, nil)
	for _, cmd := range []string{"dangerousDeleteUnlock", "dangerousDeleteAsync"} {
		response, handled := handleDangerousDeleteCommand("192.168.200.10:12345", cmd, "", manager)
		if !handled {
			t.Fatalf("%s was not handled", cmd)
		}
		if !strings.Contains(response, "only available through the local web admin") {
			t.Fatalf("%s response=%q", cmd, response)
		}
	}
}

func TestDangerousDeleteCommandsRejectUnsupportedBackendBeforeUnlock(t *testing.T) {
	manager := scheduler.NewRobotManager(nil, nil, nil)
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "unsupported in test backend"})
	manager.SetBackendRobotCreator(shared.BackendInfo{ID: shared.BackendS4A21, Capabilities: capabilities}, nil)
	response := HandlePacket("127.0.0.1:12345", "<tw><c>dangerousDeleteUnlock</c><json>{\"code\":\"123\"}</json></tw>", manager)
	if !strings.Contains(response, shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("response=%q", response)
	}
}

func TestDangerousDeleteTokenIsSingleUse(t *testing.T) {
	token, err := issueDangerousDeleteToken("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !consumeDangerousDeleteToken(token, "127.0.0.1", time.Now()) {
		t.Fatal("fresh token should be accepted")
	}
	if consumeDangerousDeleteToken(token, "127.0.0.1", time.Now()) {
		t.Fatal("token must be single use")
	}
}

func TestDangerousDeleteTokenRejectsExpiredValue(t *testing.T) {
	token := "expired-test-token"
	dangerousDeleteTokens.access.Lock()
	dangerousDeleteTokens.values[token] = dangerousDeleteToken{Expires: time.Now().Add(-time.Second), ClientIP: "127.0.0.1"}
	dangerousDeleteTokens.access.Unlock()
	if consumeDangerousDeleteToken(token, "127.0.0.1", time.Now()) {
		t.Fatal("expired token should be rejected")
	}
}

func TestDangerousDeleteTokenIsBoundToClientIP(t *testing.T) {
	token, err := issueDangerousDeleteToken("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if consumeDangerousDeleteToken(token, "::1", time.Now()) {
		t.Fatal("token must not be accepted from another client IP")
	}
}

func TestLoopbackClientIP(t *testing.T) {
	tests := []struct {
		clientID string
		wantIP   string
		wantOK   bool
	}{
		{clientID: "127.0.0.1:12345", wantIP: "127.0.0.1", wantOK: true},
		{clientID: "[::1]:12345", wantIP: "::1", wantOK: true},
		{clientID: "192.168.200.10:12345", wantOK: false},
		{clientID: "invalid", wantOK: false},
	}
	for _, tt := range tests {
		gotIP, gotOK := loopbackClientIP(tt.clientID)
		if gotIP != tt.wantIP || gotOK != tt.wantOK {
			t.Fatalf("loopbackClientIP(%q) = (%q, %v), want (%q, %v)", tt.clientID, gotIP, gotOK, tt.wantIP, tt.wantOK)
		}
	}
}

func TestDangerousDeleteTokensStayBounded(t *testing.T) {
	dangerousDeleteTokens.access.Lock()
	clear(dangerousDeleteTokens.values)
	dangerousDeleteTokens.access.Unlock()
	t.Cleanup(func() {
		dangerousDeleteTokens.access.Lock()
		clear(dangerousDeleteTokens.values)
		dangerousDeleteTokens.access.Unlock()
	})

	for i := 0; i < maxDangerousDeleteTokens+10; i++ {
		if _, err := issueDangerousDeleteToken("127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	dangerousDeleteTokens.access.Lock()
	count := len(dangerousDeleteTokens.values)
	dangerousDeleteTokens.access.Unlock()
	if count != maxDangerousDeleteTokens {
		t.Fatalf("dangerous token count got %d want %d", count, maxDangerousDeleteTokens)
	}
}
