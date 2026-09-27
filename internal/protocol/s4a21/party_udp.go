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
	"os"
	"strings"

	foundationlog "robot/internal/foundation/log"
)

// partyUDPTraceEnv enables full-datagram tracing when set to 1/true/yes/on.
// The budgets are bounded so a long session cannot flood the robot log; the
// captured bytes are what identifies the client's in-dungeon application
// framing, which the transport layer never parses.
const partyUDPTraceEnv = "S4A21_PARTY_UDP_TRACE"

const (
	partyUDPTraceSessionLimit = 4096
	partyUDPTracePeerLimit    = 256
	partyUDPTraceBytes        = 512
)

func partyUDPTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(partyUDPTraceEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type partyUDPCodec struct {
	key, rotate byte
	extra       [2]byte
}
type partyUDPPeer struct {
	codecRoute       [2]partyUDPCodec
	codecKnown       [2]bool
	nextSeqRoute     [2]uint32
	reliableSeqRoute [2]uint32
	pending          []byte
	pendingSeq       uint32
	pendingRoute     [2][]byte
	pendingSeqRoute  [2]uint32
	diagPackets      byte
	diagDrops        byte
	traceRX          int
	traceTX          int
	epochRoute       [2]bool
}

// partyUDPMaxPeers bounds the per-session peer table. Party UDP endpoints are
// announced by the server, but any host on the network can send a datagram to
// the registered port, so the table must not grow without limit.
const partyUDPMaxPeers = 256

var partyUDPCRCTable = crc32.MakeTable(0x4db89129)

// partyUDPPeerLocked returns the tracked peer for remote, creating one while
// the peer budget allows. Callers must hold c.udpMu.
func (c *Client) partyUDPPeerLocked(remote string) *partyUDPPeer {
	if peer := c.udpPeers[remote]; peer != nil {
		return peer
	}
	if len(c.udpPeers) >= partyUDPMaxPeers {
		return nil
	}
	peer := &partyUDPPeer{}
	c.udpPeers[remote] = peer
	return peer
}

func (c *Client) servePartyUDP(conn *net.UDPConn) {
	buffer := make([]byte, 2048)
	trace := partyUDPTraceEnabled()
	for {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if n == 0 || remote == nil {
			continue
		}
		c.udpMu.Lock()
		peer := c.partyUDPPeerLocked(remote.String())
		if peer == nil {
			if c.udpPeerDrops == 0 {
				c.udpPeerDrops++
				foundationlog.Robotf("S4A21_PARTY_UDP_PEER_LIMIT uid=%d limit=%d remote=%s\n", c.selfUID, partyUDPMaxPeers, remote)
			}
			c.udpMu.Unlock()
			continue
		}
		selfSlot, slotKnown := c.selfSlot, c.slotKnown
		beforePending := peer.pending != nil || peer.pendingRoute[0] != nil || peer.pendingRoute[1] != nil
		beforeSeq := peer.nextSeqRoute[0]
		replies := partyUDPReplies(buffer[:n], peer, selfSlot, slotKnown)
		afterPending := peer.pending != nil || peer.pendingRoute[0] != nil || peer.pendingRoute[1] != nil
		afterSeq := peer.nextSeqRoute[0]
		logPacket := peer.diagPackets < 8
		logDrop := len(replies) == 0 && peer.diagDrops < 8
		if logPacket {
			peer.diagPackets++
		}
		if logDrop {
			peer.diagDrops++
		}
		traceRX := trace && c.udpTraceEvents < partyUDPTraceSessionLimit && peer.traceRX < partyUDPTracePeerLimit
		if traceRX {
			c.udpTraceEvents++
			peer.traceRX++
		}
		traceTX := trace && c.udpTraceEvents < partyUDPTraceSessionLimit && peer.traceTX < partyUDPTracePeerLimit
		if traceTX {
			c.udpTraceEvents++
			peer.traceTX++
		}
		selfUID := c.selfUID
		c.udpMu.Unlock()
		loggedPayload := buffer[:n]
		if len(loggedPayload) > 96 {
			loggedPayload = loggedPayload[:96]
		}
		if logPacket {
			foundationlog.Robotf("S4A21_PARTY_UDP_RX uid=%d slot=%d known=%t remote=%s size=%d bytes=%X replies=%d pending=%t->%t seq=%d->%d\n",
				selfUID, selfSlot, slotKnown, remote, n, loggedPayload, len(replies), beforePending, afterPending, beforeSeq, afterSeq)
		} else if logDrop {
			foundationlog.Robotf("S4A21_PARTY_UDP_DROP uid=%d slot=%d known=%t remote=%s size=%d bytes=%X pending=%t seq=%d\n",
				selfUID, selfSlot, slotKnown, remote, n, loggedPayload, afterPending, afterSeq)
		}
		if traceRX {
			logPartyUDPTraceValue("RX", selfUID, remote, buffer[:n])
			for _, application := range partyUDPApplicationPayloads(buffer[:n]) {
				logPartyUDPTraceValue("APP", selfUID, remote, application)
			}
		}
		for _, reply := range replies {
			_, _ = conn.WriteToUDP(reply, remote)
			if logPacket {
				foundationlog.Robotf("S4A21_PARTY_UDP_TX uid=%d slot=%d remote=%s bytes=%X\n",
					selfUID, selfSlot, remote, reply)
			}
			if traceTX {
				logPartyUDPTraceValue("TX", selfUID, remote, reply)
			}
		}
	}
}

func logPartyUDPTraceValue(direction string, selfUID uint16, remote *net.UDPAddr, payload []byte) {
	logged := payload
	truncated := false
	if len(logged) > partyUDPTraceBytes {
		logged = logged[:partyUDPTraceBytes]
		truncated = true
	}
	foundationlog.Robotf("S4A21_PARTY_UDP_TRACE_%s uid=%d remote=%s size=%d truncated=%t bytes=%X\n",
		direction, selfUID, remote, len(payload), truncated, logged)
}

// partyUDPApplicationPayloads surfaces the application bytes carried by a
// datagram. TQOS negotiation frames (12-byte bodies) and ACKs are skipped;
// frames whose shape does not match a control frame are returned so the
// captured bytes can be decoded offline.
func partyUDPApplicationPayloads(payload []byte) [][]byte {
	frames, ok := splitPartyUDPFrames(payload)
	if !ok {
		return nil
	}
	var result [][]byte
	for _, frame := range frames {
		if application := partyUDPApplicationPayload(frame); application != nil {
			result = append(result, application)
		}
	}
	return result
}

func partyUDPApplicationPayload(frame []byte) []byte {
	if len(frame) < 9 || (frame[0] != 1 && frame[0] != 2) {
		return nil
	}
	bodyLen := int(binary.LittleEndian.Uint16(frame[5:7]))
	if len(frame) != 9+bodyLen {
		return nil
	}
	body := frame[9:]
	if frame[0] == 1 {
		if len(body) < 2 {
			return nil
		}
		inner := int(binary.LittleEndian.Uint16(body[:2]))
		if inner == 12 {
			return nil
		}
		if inner <= 0 || len(body) < 2+inner {
			return append([]byte(nil), frame...)
		}
		return append([]byte(nil), body[2:2+inner]...)
	}
	// Type 2 carries reliable data. The TQOS negotiation body is exactly
	// twelve bytes and starts with three zero bytes; anything else is
	// application data.
	if len(body) == 12 && body[0] == 0 && body[1] == 0 && body[2] == 0 {
		return nil
	}
	return append([]byte(nil), body...)
}

func (c *Client) applyPartyRealtimeInfo(body []byte) {
	if len(body) < 1 || len(body) != 1+int(body[0])*5 {
		return
	}
	c.udpMu.Lock()
	defer c.udpMu.Unlock()
	for offset := 1; offset+5 <= len(body); offset += 5 {
		if binary.LittleEndian.Uint16(body[offset:offset+2]) == c.selfUID {
			previousSlot, previousKnown := c.selfSlot, c.slotKnown
			c.selfSlot = body[offset+4]
			c.slotKnown = c.selfSlot < 4
			if previousSlot != c.selfSlot || previousKnown != c.slotKnown {
				foundationlog.Robotf("S4A21_PARTY_UDP_SLOT uid=%d slot=%d known=%t\n", c.selfUID, c.selfSlot, c.slotKnown)
			}
			return
		}
	}
}

func partyUDPReplies(payload []byte, peer *partyUDPPeer, selfSlot byte, slotKnown bool) [][]byte {
	if !slotKnown {
		return nil
	}
	frames, ok := splitPartyUDPFrames(payload)
	if !ok {
		return nil
	}
	replies := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		replies = append(replies, partyUDPFrameReplies(frame, peer, selfSlot)...)
	}
	return replies
}

func splitPartyUDPFrames(payload []byte) ([][]byte, bool) {
	frames := make([][]byte, 0, 2)
	for len(payload) > 0 {
		frameSize := 0
		switch payload[0] {
		case 0:
			frameSize = 8
		case 1, 2:
			if len(payload) < 9 {
				return nil, false
			}
			frameSize = 9 + int(binary.LittleEndian.Uint16(payload[5:7]))
		default:
			return nil, false
		}
		if frameSize > len(payload) {
			return nil, false
		}
		frames = append(frames, payload[:frameSize])
		payload = payload[frameSize:]
	}
	return frames, len(frames) > 0
}

func partyUDPFrameReplies(payload []byte, peer *partyUDPPeer, selfSlot byte) [][]byte {
	if len(payload) == 8 && payload[0] == 0 {
		for route := 0; route < 2; route++ {
			if peer.pendingRoute[route] != nil && binary.LittleEndian.Uint32(payload[2:6]) == peer.pendingSeqRoute[route]+1 {
				peer.pendingRoute[route] = nil
				if route == 0 || peer.pending == nil || peer.pendingSeq == peer.pendingSeqRoute[route] {
					peer.pending = nil
				}
			}
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
	sequence := binary.LittleEndian.Uint32(payload[1:5])
	replies := make([][]byte, 0, 2)
	if payload[0] == 1 {
		replies = append(replies, partyUDPAck(selfSlot, sequence))
		if len(body) < 2 {
			return replies
		}
		inner := int(binary.LittleEndian.Uint16(body[:2]))
		if inner != 12 || len(body) < 2+inner {
			return replies
		}
		body = body[2 : 2+inner]
	}
	if len(body) != 12 || body[0] != 0 || body[1] != 0 || body[2] != 0 {
		return replies
	}
	sender := payload[7]
	state, codec, wireRoute, ok := decodePartyUDPBody(body, sender, 1, peer)
	if !ok {
		return replies
	}
	routeIndex := int(wireRoute)
	if routeIndex > 1 {
		routeIndex = 1
	}
	peer.codecRoute[routeIndex], peer.codecKnown[routeIndex] = codec, true
	switch state {
	case 3:
		routeIndex := int(wireRoute)
		if routeIndex > 1 {
			routeIndex = 1
		}
		if !peer.epochRoute[routeIndex] {
			peer.nextSeqRoute[routeIndex] = 0
			peer.epochRoute[routeIndex] = true
		}
		sequence := peer.nextSeqRoute[routeIndex]
		replies = append(replies, partyUDPReplyCandidates(sequence, selfSlot, 0, wireRoute, codec)...)
		peer.nextSeqRoute[routeIndex]++
	case 0:
		routeIndex := int(wireRoute)
		if routeIndex > 1 {
			routeIndex = 1
		}
		replies = append(replies, partyUDPReplyCandidates(peer.nextSeqRoute[routeIndex], selfSlot, 1, wireRoute, codec)...)
		peer.nextSeqRoute[routeIndex]++
	case 1:
		if peer.pendingRoute[routeIndex] == nil {
			peer.pendingSeqRoute[routeIndex] = peer.reliableSeqRoute[routeIndex]
			peer.reliableSeqRoute[routeIndex]++
			peer.pendingRoute[routeIndex] = buildPartyUDP(peer.pendingSeqRoute[routeIndex], selfSlot, 2, wireRoute, codec)
			peer.pending, peer.pendingSeq = peer.pendingRoute[routeIndex], peer.pendingSeqRoute[routeIndex]
		}
		replies = append(replies, peer.pendingRoute[routeIndex])
		// state2 uses the same directional tail as the unreliable states.
	}
	return replies
}

// A21 keeps a 4-byte session value in the request. Some clients expect that
// value echoed, while patched clients validate the state checksum. Emit both
// candidates with the same transport sequence; an invalid candidate is ignored
// before it can advance the receive window.
func partyUDPReplyCandidates(sequence uint32, sender, state, route byte, codec partyUDPCodec) [][]byte {
	return [][]byte{buildPartyUDP(sequence, sender, state, route, codec)}
}

func decodePartyUDPBody(body []byte, sender, route byte, peer *partyUDPPeer) (byte, partyUDPCodec, byte, bool) {
	routes := []byte{route}
	if route != 0 {
		routes = append(routes, 0)
	}
	for _, expectedRoute := range routes {
		routeIndex := int(expectedRoute)
		if routeIndex > 1 {
			routeIndex = 1
		}
		if peer.codecKnown[routeIndex] {
			codec := peer.codecRoute[routeIndex]
			if state, ok := decodePartyUDPBodyWithCodec(body, sender, expectedRoute, codec); ok {
				codec.extra = decodePartyUDPExtra(body, codec)
				return state, codec, expectedRoute, true
			}
		}
	}
	for rotate := 0; rotate < 8; rotate++ {
		codec := partyUDPCodec{key: bits.RotateLeft8(body[7], -rotate) ^ sender, rotate: byte(rotate)}
		for _, expectedRoute := range routes {
			if state, ok := decodePartyUDPBodyWithCodec(body, sender, expectedRoute, codec); ok {
				codec.extra = decodePartyUDPExtra(body, codec)
				return state, codec, expectedRoute, true
			}
		}
	}
	return 0, partyUDPCodec{}, 0, false
}

func decodePartyUDPBodyWithCodec(body []byte, sender, route byte, codec partyUDPCodec) (byte, bool) {
	decoded := bits.RotateLeft8(body[7], -int(codec.rotate)) ^ codec.key
	state := bits.RotateLeft8(body[8], -int(codec.rotate)) ^ codec.key
	gotRoute := bits.RotateLeft8(body[9], -int(codec.rotate)) ^ codec.key
	if decoded != sender || state > 3 || gotRoute != route {
		return 0, false
	}
	extra := decodePartyUDPExtra(body, codec)
	checksum := partyUDPChecksum(sender, state, route, extra)
	return state, bytes.Equal(body[3:7], checksum[:])
}

func decodePartyUDPExtra(body []byte, codec partyUDPCodec) [2]byte {
	return [2]byte{
		bits.RotateLeft8(body[10], -int(codec.rotate)) ^ codec.key,
		bits.RotateLeft8(body[11], -int(codec.rotate)) ^ codec.key,
	}
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
	checksum := partyUDPChecksum(sender, state, route, codec.extra)
	copy(body[3:7], checksum[:])
	for i, value := range []byte{sender, state, route, codec.extra[0], codec.extra[1]} {
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

func partyUDPChecksum(sender, state, route byte, extra [2]byte) [4]byte {
	value := crc32.Checksum([]byte{sender, state, route, extra[0], extra[1]}, partyUDPCRCTable)
	var out [4]byte
	binary.LittleEndian.PutUint32(out[:], value)
	return out
}
