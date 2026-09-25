package monitor

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"robot/internal/foundation/charset"
	"robot/internal/foundation/lockhub"
	foundationnetwork "robot/internal/foundation/network"
)

const (
	defaultAddress = "127.0.0.1:30303"

	KindMegaphone       = "megaphone"
	KindWebNoticeSingle = "web_notice_single"

	opMegaphone       = 0x0546
	opNotifyNewMail   = 0x0514
	opWebNoticeSingle = 0x09e0
)

type Client struct {
	Address string

	mu       lockhub.Locker
	failures int
	retryAt  time.Time
	dial     func(network, address string, timeout time.Duration) (net.Conn, error)
	now      func() time.Time
}

const (
	monitorRetryMin = time.Second
	monitorRetryMax = 30 * time.Second
)

func (c *Client) SendWorldShout(msg, name string, senderID uint16) error {
	return c.SendMonitorAnnouncement(KindMegaphone, msg, name, senderID)
}

func (c *Client) NotifyNewMail(characNo uint32) error {
	if characNo == 0 {
		return fmt.Errorf("invalid mail character number 0")
	}
	return c.send(BuildNotifyNewMailPacket(characNo), "new mail")
}

func (c *Client) SendMonitorAnnouncement(kind, msg, name string, senderID uint16) error {
	packet, err := BuildAnnouncementPacket(kind, msg, name, senderID)
	if err != nil {
		return err
	}
	return c.send(packet, kind)
}

func (c *Client) send(packet []byte, kind string) error {
	if c == nil {
		return fmt.Errorf("monitor client is nil")
	}
	now := c.currentTime()
	if err := c.backoffError(kind, now); err != nil {
		return err
	}

	addr := c.Address
	if addr == "" {
		addr = defaultAddress
	}
	dial := c.dial
	if dial == nil {
		dial = net.DialTimeout
	}
	conn, err := dial("tcp", addr, 3*time.Second)
	if err != nil {
		c.recordFailure(c.currentTime())
		return fmt.Errorf("connect monitor %s: %w", kind, err)
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		c.recordFailure(c.currentTime())
		return fmt.Errorf("set monitor %s write deadline: %w", kind, err)
	}
	if err := foundationnetwork.WriteFull(conn, packet); err != nil {
		c.recordFailure(c.currentTime())
		return fmt.Errorf("send monitor %s: %w", kind, err)
	}
	c.recordSuccess()
	return nil
}

func (c *Client) backoffError(kind string, now time.Time) error {
	c.mu.Lock()
	retryAt := c.retryAt
	c.mu.Unlock()
	if now.Before(retryAt) {
		return fmt.Errorf("monitor %s retry is backed off for %s", kind, retryAt.Sub(now).Round(time.Millisecond))
	}
	return nil
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	c.failures = 0
	c.retryAt = time.Time{}
	c.mu.Unlock()
}

func (c *Client) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) recordFailure(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	delay := monitorRetryMin
	for i := 1; i < c.failures && delay < monitorRetryMax; i++ {
		delay *= 2
	}
	if delay > monitorRetryMax {
		delay = monitorRetryMax
	}
	c.retryAt = now.Add(delay)
}

func BuildAnnouncementPacket(kind, msg, name string, senderID uint16) ([]byte, error) {
	switch kind {
	case "", KindMegaphone:
		return buildMegaphoneLikePacket(opMegaphone, msg, name, senderID), nil
	case KindWebNoticeSingle:
		return buildWebNoticeSinglePacket(msg), nil
	default:
		return nil, fmt.Errorf("unknown monitor announcement kind %q", kind)
	}
}

// BuildNotifyNewMailPacket matches Packet_Monitor_Notify_New_Mail. Monitor
// resolves the online character, fills the channel id at +0x0e, and forwards
// the packet to df_game_r. The game then raises its mailbox alarm.
func BuildNotifyNewMailPacket(characNo uint32) []byte {
	const size = 0x12
	packet := make([]byte, size)
	putHeader(packet, opNotifyNewMail, size)
	binary.LittleEndian.PutUint32(packet[0x0a:0x0e], characNo)
	return packet
}

// buildMegaphoneLikePacket builds the verified monitor packet that the game
// renders exactly like a normal world megaphone. Keep it as the reference
// implementation for world-shout behavior; system announcements use 0x09e0.
func buildMegaphoneLikePacket(op uint16, msg, name string, senderID uint16) []byte {
	msgBytes := truncateBytes([]byte(msg), 255)
	nameBytes := charset.Windows1252StringBytes(name)
	if len(nameBytes) == 0 {
		nameBytes = []byte("Robot")
	}
	nameBytes = truncateBytes(nameBytes, 0x1e)

	size := 0x2e + len(msgBytes)
	packet := make([]byte, size)
	putHeader(packet, op, size)
	packet[0x0b] = 11
	binary.LittleEndian.PutUint16(packet[0x0c:0x0e], senderID)
	packet[0x0e] = 15
	copy(packet[0x0f:0x2d], nameBytes)
	packet[0x2d] = byte(len(msgBytes))
	copy(packet[0x2e:], msgBytes)
	return packet
}

// buildWebNoticeSinglePacket matches CPacketTranslater::OnWebNoticeSingle:
// monitor forwards the packet header size and logs len at +0x0a, text at +0x0b.
func buildWebNoticeSinglePacket(msg string) []byte {
	msgBytes := truncateBytes([]byte(msg), 255)
	size := 0x0c + len(msgBytes)
	packet := make([]byte, size)
	putHeader(packet, opWebNoticeSingle, size)
	packet[0x0a] = byte(len(msgBytes))
	copy(packet[0x0b:], msgBytes)
	return packet
}

func putHeader(packet []byte, op uint16, size int) {
	binary.LittleEndian.PutUint16(packet[0:2], op)
	binary.LittleEndian.PutUint16(packet[2:4], uint16(size))
}

func truncateBytes(b []byte, max int) []byte {
	if len(b) > max {
		return b[:max]
	}
	return b
}
