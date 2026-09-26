package s4a21

import (
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

func TestSessionKeepaliveStallDetection(t *testing.T) {
	session := &Session{}
	now := time.Now()
	if session.keepaliveStalled(now) {
		t.Fatal("stall reported before any keepalive ACK was observed")
	}

	session.recordCheckAck(now.Add(-2 * sessionKeepaliveAckTimeout))
	if !session.keepaliveStalled(now) {
		t.Fatal("stall not reported after the ACK timeout elapsed")
	}

	session.recordCheckAck(now)
	if session.keepaliveStalled(now) {
		t.Fatal("fresh ACK reported as stalled")
	}
}

func TestSessionRecordsCheckConnectionAck(t *testing.T) {
	session := &Session{}
	session.dispatchPacket(protocol.Packet{Type: protocol.CmdCheckConnection})
	if !session.checkAckSeen || session.lastCheckAck.IsZero() {
		t.Fatal("check connection ACK was not recorded")
	}
	if session.keepaliveStalled(time.Now().Add(-time.Second)) {
		t.Fatal("stall reported while the ACK is fresh")
	}
}
