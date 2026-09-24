package s4a21

import (
	"encoding/binary"
	"testing"
)

func TestPartyUDPHandshakeReplies(t *testing.T) {
	peer := &partyUDPPeer{}
	codec := partyUDPCodec{key: 0x7e}
	state3 := buildPartyUDP(9, 1, 3, 1, codec)
	replies := partyUDPReplies(state3, peer)
	if len(replies) != 1 || replies[0][0] != 2 {
		t.Fatalf("state3 replies=%X", replies)
	}
	state0 := buildPartyUDP(10, 1, 0, 1, codec)
	replies = partyUDPReplies(state0, peer)
	if len(replies) != 1 {
		t.Fatalf("state0 replies=%X", replies)
	}
	state1 := buildPartyUDP(11, 1, 1, 1, codec)
	replies = partyUDPReplies(state1, peer)
	if len(replies) != 1 || replies[0][0] != 1 {
		t.Fatalf("state1 replies=%X", replies)
	}
	sequence := binary.LittleEndian.Uint32(replies[0][1:5])
	if len(peer.pending) == 0 || peer.pendingSeq != sequence {
		t.Fatalf("pending state2 sequence=%d peer=%+v", sequence, peer)
	}
	partyUDPReplies(partyUDPAck(1, sequence), peer)
	if peer.pending != nil {
		t.Fatal("state2 ACK did not clear pending reply")
	}
}

func TestPartyUDPEchoIsNotReturned(t *testing.T) {
	peer := &partyUDPPeer{}
	if got := partyUDPReplies([]byte("not-a-tqos-frame"), peer); len(got) != 0 {
		t.Fatalf("invalid datagram produced replies=%X", got)
	}
}
