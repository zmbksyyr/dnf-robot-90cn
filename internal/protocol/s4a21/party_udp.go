package s4a21

// A21 party UDP uses the same small TQOS negotiation carried by the game
// client. It is deliberately kept in this adapter: the shared scheduler only
// needs a connected party session and must not know wire details.

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
	"net"

	foundationlog "robot/internal/foundation/log"
)

type partyUDPCodec struct {
	key, rotate byte
	extra       [2]byte
	checksum    [4]byte
}
type partyUDPPeer struct {
	codec       partyUDPCodec
	codecKnown  bool
	nextSeq     uint32
	reliableSeq uint32
	pending     []byte
	pendingSeq  uint32
	diagPackets byte
	diagDrops   byte
}

var partyUDPCRCTable = crc32.MakeTable(0x4db89129)

func (c *Client) servePartyUDP(conn *net.UDPConn) {
	buffer := make([]byte, 2048)
	for {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if n == 0 || remote == nil {
			continue
		}
		c.udpMu.Lock()
		peer := c.udpPeers[remote.String()]
		if peer == nil {
			peer = &partyUDPPeer{}
			c.udpPeers[remote.String()] = peer
		}
		selfSlot, slotKnown := c.selfSlot, c.slotKnown
		beforePending := peer.pending != nil
		beforeSeq := peer.nextSeq
		replies := partyUDPReplies(buffer[:n], peer, selfSlot, slotKnown)
		afterPending := peer.pending != nil
		afterSeq := peer.nextSeq
		logPacket := peer.diagPackets < 32
		logDrop := len(replies) == 0 && peer.diagDrops < 32
		if logPacket {
			peer.diagPackets++
		}
		if logDrop {
			peer.diagDrops++
		}
		selfUID := c.selfUID
		c.udpMu.Unlock()
		if logPacket {
			foundationlog.Robotf("S4A21_PARTY_UDP_RX uid=%d slot=%d known=%t remote=%s bytes=%X replies=%d pending=%t->%t seq=%d->%d\n",
				selfUID, selfSlot, slotKnown, remote, buffer[:n], len(replies), beforePending, afterPending, beforeSeq, afterSeq)
		} else if logDrop {
			foundationlog.Robotf("S4A21_PARTY_UDP_DROP uid=%d slot=%d known=%t remote=%s bytes=%X pending=%t seq=%d\n",
				selfUID, selfSlot, slotKnown, remote, buffer[:n], afterPending, afterSeq)
		}
		for _, reply := range replies {
			_, _ = conn.WriteToUDP(reply, remote)
			if logPacket {
				foundationlog.Robotf("S4A21_PARTY_UDP_TX uid=%d slot=%d remote=%s bytes=%X\n",
					selfUID, selfSlot, remote, reply)
			}
		}
	}
}

func (c *Client) applyPartyRealtimeInfo(body []byte) {
	if len(body) < 1 || len(body) != 1+int(body[0])*5 {
		return
	}
	c.udpMu.Lock()
	defer c.udpMu.Unlock()
	for offset := 1; offset+5 <= len(body); offset += 5 {
		if binary.LittleEndian.Uint16(body[offset:offset+2]) == c.selfUID {
			c.selfSlot = body[offset+4]
			c.slotKnown = c.selfSlot < 4
			foundationlog.Robotf("S4A21_PARTY_UDP_SLOT uid=%d slot=%d known=%t\n", c.selfUID, c.selfSlot, c.slotKnown)
			return
		}
	}
}

func partyUDPReplies(payload []byte, peer *partyUDPPeer, selfSlot byte, slotKnown bool) [][]byte {
	if !slotKnown {
		return nil
	}
	if len(payload) == 8 && payload[0] == 0 {
		if peer.pending != nil && binary.LittleEndian.Uint32(payload[2:6]) == peer.pendingSeq+1 {
			peer.pending = nil
		}
		return nil
	}
	if len(payload) < 9 || (payload[0] != 1 && payload[0] != 2) {
		return nil
	}
	bodyLen := int(binary.LittleEndian.Uint16(payload[5:7]))
	if len(payload) != 9+bodyLen {
		return nil
	}
	body := payload[9:]
	if payload[0] == 1 {
		if len(body) < 2 {
			return nil
		}
		inner := int(binary.LittleEndian.Uint16(body[:2]))
		if inner != 12 || len(body) < 2+inner {
			return nil
		}
		body = body[2 : 2+inner]
	}
	if len(body) != 12 || body[0] != 0 || body[1] != 0 || body[2] != 0 {
		return nil
	}
	sender := payload[7]
	state, codec, ok := decodePartyUDPBody(body, sender, 1, peer)
	if !ok {
		return nil
	}
	peer.codec, peer.codecKnown = codec, true
	copy(codec.extra[:], body[10:12])
	peer.codec = codec
	sequence := binary.LittleEndian.Uint32(payload[1:5])
	replies := make([][]byte, 0, 2)
	if payload[0] == 1 {
		replies = append(replies, partyUDPAck(selfSlot, sequence))
	}
	switch state {
	case 3:
		peer.nextSeq, peer.reliableSeq, peer.pending = 0, 0, nil
		replies = append(replies, buildPartyUDP(0, selfSlot, 0, 1, codec))
	case 0:
		replies = append(replies, buildPartyUDP(peer.nextSeq, selfSlot, 1, 1, codec))
		peer.nextSeq++
	case 1:
		if peer.pending == nil {
			peer.pendingSeq = peer.reliableSeq
			peer.reliableSeq++
			peer.pending = buildPartyUDP(peer.pendingSeq, selfSlot, 2, 1, codec)
		}
		replies = append(replies, peer.pending)
	}
	return replies
}

func decodePartyUDPBody(body []byte, sender, route byte, peer *partyUDPPeer) (byte, partyUDPCodec, bool) {
	if peer.codecKnown {
		if state, ok := decodePartyUDPBodyWithCodec(body, sender, route, peer.codec); ok {
			peer.codec.extra = [2]byte{body[10], body[11]}
			copy(peer.codec.checksum[:], body[3:7])
			return state, peer.codec, true
		}
	}
	for rotate := 0; rotate < 8; rotate++ {
		codec := partyUDPCodec{key: bits.RotateLeft8(body[7], -rotate) ^ sender, rotate: byte(rotate)}
		if state, ok := decodePartyUDPBodyWithCodec(body, sender, route, codec); ok {
			codec.extra = [2]byte{body[10], body[11]}
			copy(codec.checksum[:], body[3:7])
			return state, codec, true
		}
	}
	return 0, partyUDPCodec{}, false
}

func decodePartyUDPBodyWithCodec(body []byte, sender, route byte, codec partyUDPCodec) (byte, bool) {
	decoded := bits.RotateLeft8(body[7], -int(codec.rotate)) ^ codec.key
	state := bits.RotateLeft8(body[8], -int(codec.rotate)) ^ codec.key
	gotRoute := bits.RotateLeft8(body[9], -int(codec.rotate)) ^ codec.key
	if decoded != sender || state > 3 || gotRoute != route {
		return 0, false
	}
	return state, true
}

func buildPartyUDP(sequence uint32, sender, state, route byte, codec partyUDPCodec) []byte {
	typ, bodyLen, offset := byte(2), 12, 9
	if state == 2 {
		typ, bodyLen, offset = 1, 14, 11
	}
	out := make([]byte, 9+bodyLen)
	out[0] = typ
	binary.LittleEndian.PutUint32(out[1:5], sequence)
	binary.LittleEndian.PutUint16(out[5:7], uint16(bodyLen))
	out[7] = sender
	if typ == 1 {
		binary.LittleEndian.PutUint16(out[9:11], 12)
	}
	body := out[offset:]
	copy(body[3:7], codec.checksum[:])
	for i, value := range []byte{sender, state, route} {
		body[7+i] = bits.RotateLeft8(value^codec.key, int(codec.rotate))
	}
	body[10], body[11] = codec.extra[0], codec.extra[1]
	return out
}

func partyUDPAck(sender byte, sequence uint32) []byte {
	out := make([]byte, 8)
	out[1] = sender
	binary.LittleEndian.PutUint32(out[2:6], sequence+1)
	return out
}

func partyUDPChecksum(sender, state, route byte) [4]byte {
	value := crc32.Checksum([]byte{sender, state, route}, partyUDPCRCTable)
	var out [4]byte
	binary.LittleEndian.PutUint32(out[:], value)
	out[0] ^= out[1] ^ out[2] ^ out[3] ^ 0x18
	return out
}
