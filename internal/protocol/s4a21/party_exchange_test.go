package s4a21

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// buildPartyIpInfoBody mirrors the server's PARTY IP INFO (0x0B) roster shape:
// member count plus 22 bytes per member.
func buildPartyIpInfoBody(members []struct {
	uid  uint16
	ip   net.IP
	port int
}) []byte {
	body := make([]byte, 1+len(members)*22)
	body[0] = byte(len(members))
	for index, member := range members {
		offset := 1 + index*22
		binary.LittleEndian.PutUint16(body[offset:offset+2], member.uid)
		copy(body[offset+2:offset+6], member.ip.To4())
		copy(body[offset+6:offset+10], member.ip.To4())
		body[offset+10] = byte(member.port >> 8)
		body[offset+11] = byte(member.port & 0xFF)
	}
	return body
}

func TestStartPartyUDPExchangeBootstrapsCodec(t *testing.T) {
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	peerPort := peer.LocalAddr().(*net.UDPAddr).Port

	client := NewClient(nil)
	client.udpMu.Lock()
	client.udpConn = local
	client.selfUID = 3
	client.selfSlot = 0
	client.slotKnown = true
	client.udpMu.Unlock()

	body := buildPartyIpInfoBody([]struct {
		uid  uint16
		ip   net.IP
		port int
	}{
		{uid: 3, ip: net.IPv4(127, 0, 0, 1), port: local.LocalAddr().(*net.UDPAddr).Port},
		{uid: 1, ip: net.IPv4(127, 0, 0, 1), port: peerPort},
	})
	client.StartPartyUDPExchange(body)

	buffer := make([]byte, 256)
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _, err := peer.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("peer did not receive the exchange frame: %v", err)
	}
	frame := buffer[:n]
	if frame[0] != 2 {
		t.Fatalf("exchange frame type = %d", frame[0])
	}
	if binary.LittleEndian.Uint16(frame[5:7]) != 12 {
		t.Fatalf("exchange frame body length = %d", binary.LittleEndian.Uint16(frame[5:7]))
	}
	if frame[7] != 0 {
		t.Fatalf("exchange frame sender slot = %d", frame[7])
	}

	// The peer derives the codec from the frame and answers with a state-0
	// control reply, which is what makes the codec known on both sides.
	receiver := &partyUDPPeer{}
	replies := partyUDPReplies(frame, receiver, 1, true)
	if len(replies) == 0 {
		t.Fatal("peer produced no reply to the exchange frame")
	}
	if !receiver.codecKnown[partyUDPSelfRoute] {
		t.Fatal("peer did not learn the initiator codec")
	}
	if receiver.codecRoute[partyUDPSelfRoute] != partyUDPDefaultCodec {
		t.Fatalf("derived codec = %+v", receiver.codecRoute[partyUDPSelfRoute])
	}

	// The initiator must accept the reply and learn the peer codec too.
	sender := &partyUDPPeer{}
	partyUDPReplies(replies[0], sender, 0, true)
	if !sender.codecKnown[partyUDPSelfRoute] {
		t.Fatal("initiator did not learn the peer codec from the reply")
	}
}

func TestStartPartyUDPExchangeIgnoresMalformedRoster(t *testing.T) {
	client := NewClient(nil)
	client.StartPartyUDPExchange([]byte{2, 1, 2})
	client.StartPartyUDPExchange([]byte{0})
}
