package s4a21

import (
	"encoding/binary"
	"testing"
)

func TestPartyUDPHandshakeReplies(t *testing.T) {
	peer := &partyUDPPeer{}
	codec := partyUDPCodec{key: 0x7e}
	state3 := buildPartyUDP(9, 1, 3, 1, codec)
	replies := partyUDPReplies(state3, peer, 1, true)
	if len(replies) != 1 || replies[0][0] != 2 || replies[0][7] != 1 {
		t.Fatalf("state3 replies=%X", replies)
	}
	state0 := buildPartyUDP(10, 1, 0, 1, codec)
	replies = partyUDPReplies(state0, peer, 1, true)
	if len(replies) != 1 {
		t.Fatalf("state0 replies=%X", replies)
	}
	state1 := buildPartyUDP(11, 1, 1, 1, codec)
	replies = partyUDPReplies(state1, peer, 1, true)
	if len(replies) != 1 || replies[0][0] != 1 {
		t.Fatalf("state1 replies=%X", replies)
	}
	sequence := binary.LittleEndian.Uint32(replies[0][1:5])
	if len(peer.pending) == 0 || peer.pendingSeq != sequence {
		t.Fatalf("pending state2 sequence=%d peer=%+v", sequence, peer)
	}
	partyUDPReplies(partyUDPAck(1, sequence), peer, 1, true)
	if peer.pending != nil {
		t.Fatal("state2 ACK did not clear pending reply")
	}
}

func TestPartyUDPEchoIsNotReturned(t *testing.T) {
	peer := &partyUDPPeer{}
	if got := partyUDPReplies([]byte("not-a-tqos-frame"), peer, 1, true); len(got) != 0 {
		t.Fatalf("invalid datagram produced replies=%X", got)
	}
}

func TestPartyRealtimeInfoSelectsSelfSlot(t *testing.T) {
	client := &Client{selfUID: 5784}
	client.applyPartyRealtimeInfo([]byte{
		2,
		1, 0, 100, 0, 0,
		0x98, 0x16, 100, 0, 1,
	})
	if !client.slotKnown || client.selfSlot != 1 {
		t.Fatalf("party identity slot=%d known=%t", client.selfSlot, client.slotKnown)
	}
}

func TestPartyUDPWaitsForSelfSlot(t *testing.T) {
	peer := &partyUDPPeer{}
	payload := buildPartyUDP(9, 0, 3, 1, partyUDPCodec{key: 0x7e})
	if replies := partyUDPReplies(payload, peer, 0, false); len(replies) != 0 {
		t.Fatalf("unknown self slot produced replies=%X", replies)
	}
}

func TestPartyUDPA21TwelveByteFrame(t *testing.T) {
	peer := &partyUDPPeer{}
	frame := []byte{0x02, 0x01, 0x00, 0x00, 0x00, 0x0c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x3a, 0xcf, 0xe7, 0xef, 0x59, 0x5a, 0x58, 0x58, 0x59}
	replies := partyUDPReplies(frame, peer, 1, true)
	if len(replies) != 1 || replies[0][0] != 2 {
		t.Fatalf("A21 12-byte frame replies=%X", replies)
	}
	body := replies[0][9:]
	state, _, _, ok := decodePartyUDPBody(body, 1, 1, &partyUDPPeer{})
	if !ok || state != 0 {
		t.Fatalf("A21 response checksum/codec invalid state=%d ok=%t body=%X", state, ok, body)
	}
}

func TestPartyUDPA21RouteZeroChecksum(t *testing.T) {
	peer := &partyUDPPeer{}
	frame := []byte{0x02, 0x03, 0x00, 0x00, 0x00, 0x0c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xaf, 0x92, 0x1a, 0xb4, 0x59, 0x5a, 0x59, 0x58, 0x59}
	if replies := partyUDPReplies(frame, peer, 1, true); len(replies) != 1 {
		t.Fatalf("A21 route-zero frame replies=%X", replies)
	}
}

func TestPartyUDPKeepsReliableStatePerRoute(t *testing.T) {
	peer := &partyUDPPeer{}
	codec := partyUDPCodec{key: 0x7e}
	for _, route := range []byte{0, 1} {
		partyUDPReplies(buildPartyUDP(1, 1, 3, route, codec), peer, 1, true)
		partyUDPReplies(buildPartyUDP(2, 1, 0, route, codec), peer, 1, true)
	}
	route0 := partyUDPReplies(buildPartyUDP(3, 1, 1, 0, codec), peer, 1, true)
	route1 := partyUDPReplies(buildPartyUDP(3, 1, 1, 1, codec), peer, 1, true)
	if len(route0) != 1 || len(route1) != 1 || route0[0][0] != 1 || route1[0][0] != 1 {
		t.Fatalf("route replies=%X/%X", route0, route1)
	}
	if binary.LittleEndian.Uint32(route0[0][1:5]) != binary.LittleEndian.Uint32(route1[0][1:5]) {
		t.Fatalf("routes should have independent reliable sequences: %X/%X", route0[0], route1[0])
	}
}

func TestPartyUDPAcksReliableFramesInCombinedDatagram(t *testing.T) {
	peer := &partyUDPPeer{}
	ack := partyUDPAck(0, 7)
	reliable := make([]byte, 9+5)
	reliable[0] = 1
	binary.LittleEndian.PutUint32(reliable[1:5], 12)
	binary.LittleEndian.PutUint16(reliable[5:7], 5)
	reliable[7] = 0
	copy(reliable[9:], []byte{3, 0, 1, 2, 3})
	payload := append(append([]byte{}, ack...), reliable...)
	replies := partyUDPReplies(payload, peer, 1, true)
	if len(replies) != 1 || len(replies[0]) != 8 || replies[0][0] != 0 {
		t.Fatalf("combined reliable replies=%X", replies)
	}
	if got := binary.LittleEndian.Uint32(replies[0][2:6]); got != 13 {
		t.Fatalf("combined reliable ack=%d want=13", got)
	}
}
