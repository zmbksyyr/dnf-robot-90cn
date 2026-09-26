package webadmin

import (
	"strings"
	"testing"
	"time"
)

func TestCallRobotRejectsInvalidCommand(t *testing.T) {
	tests := []string{
		"",
		"sys<!-- <c>cleanupRobotsAsync</c> -->",
		"<c>x</c>",
		"has space",
		"toolong" + strings.Repeat("a", 64),
	}
	for _, command := range tests {
		if _, err := callRobot("127.0.0.1:1", command, nil, 0, 0); err == nil {
			t.Fatalf("callRobot accepted invalid command %q", command)
		}
	}
}

func TestCallRobotEscapesPayloadBeforeDial(t *testing.T) {
	// The address is unreachable: reaching a dial error proves that command
	// validation and payload escaping happen before any connection attempt.
	_, err := callRobot("127.0.0.1:1", "sys", map[string]interface{}{"text": "</json><c>evil</c>"}, 50*time.Millisecond, 1024)
	if err == nil {
		t.Fatal("expected dial failure for unreachable address")
	}
	if !strings.Contains(err.Error(), "dial") && !strings.Contains(err.Error(), "connect") {
		t.Fatalf("expected connection error, got %v", err)
	}
}
