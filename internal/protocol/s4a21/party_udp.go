package s4a21

// A21 party UDP uses the same small TQOS negotiation carried by the game
// client. It is deliberately kept in this adapter: the shared scheduler only
// needs a connected party session and must not know wire details.

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math/bits"
	"net"
)

type partyUDPCodec struct{ key, rotate byte }
type partyUDPPeer struct {
	codec       partyUDPCodec
	codecKnown  bool
	nextSeq     uint32
	reliableSeq uint32
	pending     []byte
	pendingSeq  uint32
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
		replies := partyUDPReplies(buffer[:n], peer)
		c.udpMu.Unlock()
		for _, reply := range replies {
			_, _ = conn.WriteToUDP(reply, remote)
		}
	}
}

func partyUDPReplies(payload []byte, peer *partyUDPPeer) [][]byte {
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
		if inner != 10 || len(body) < 2+inner {
			return nil
		}
		body = body[2 : 2+inner]
	}
	if len(body) != 10 || body[0] != 0 || body[1] != 0 || body[2] != 0 {
		return nil
	}
	sender := payload[7]
	state, codec, ok := decodePartyUDPBody(body, sender, 1, peer)
	if !ok {
		return nil
	}
	peer.codec, peer.codecKnown = codec, true
	sequence := binary.LittleEndian.Uint32(payload[1:5])
	replies := make([][]byte, 0, 2)
	if payload[0] == 1 {
		replies = append(replies, partyUDPAck(sender, sequence))
	}
	switch state {
	case 3:
		peer.nextSeq, peer.reliableSeq, peer.pending = 0, 0, nil
		replies = append(replies, buildPartyUDP(0, 0, 0, 1, codec))
	case 0:
		replies = append(replies, buildPartyUDP(peer.nextSeq, 0, 1, 1, codec))
		peer.nextSeq++
	case 1:
		if peer.pending == nil {
			peer.pendingSeq = peer.reliableSeq
			peer.reliableSeq++
			peer.pending = buildPartyUDP(peer.pendingSeq, 0, 2, 1, codec)
		}
		replies = append(replies, peer.pending)
	}
	return replies
}

func decodePartyUDPBody(body []byte, sender, route byte, peer *partyUDPPeer) (byte, partyUDPCodec, bool) {
	if peer.codecKnown {
		if state, ok := decodePartyUDPBodyWithCodec(body, sender, route, peer.codec); ok {
			return state, peer.codec, true
		}
	}
	for rotate := 0; rotate < 8; rotate++ {
		codec := partyUDPCodec{key: bits.RotateLeft8(body[7], -rotate) ^ sender, rotate: byte(rotate)}
		if state, ok := decodePartyUDPBodyWithCodec(body, sender, route, codec); ok {
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
	checksum := partyUDPChecksum(sender, state, route)
	return state, bytes.Equal(body[3:7], checksum[:])
}

func buildPartyUDP(sequence uint32, sender, state, route byte, codec partyUDPCodec) []byte {
	typ, bodyLen, offset := byte(2), 10, 9
	if state == 2 {
		typ, bodyLen, offset = 1, 12, 11
	}
	out := make([]byte, 9+bodyLen)
	out[0] = typ
	binary.LittleEndian.PutUint32(out[1:5], sequence)
	binary.LittleEndian.PutUint16(out[5:7], uint16(bodyLen))
	out[7] = sender
	if typ == 1 {
		binary.LittleEndian.PutUint16(out[9:11], 10)
	}
	body := out[offset:]
	checksum := partyUDPChecksum(sender, state, route)
	copy(body[3:7], checksum[:])
	for i, value := range []byte{sender, state, route} {
		body[7+i] = bits.RotateLeft8(value^codec.key, int(codec.rotate))
	}
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
