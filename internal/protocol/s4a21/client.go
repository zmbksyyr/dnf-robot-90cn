package s4a21

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"robot/internal/foundation/lockhub"
	"robot/internal/foundation/network"
)

const DefaultMaxPacketLength = 1024 * 1024

// defaultWriteTimeout bounds a single framed write. Without it a peer that
// stops reading can block the write path (and the shared sendMu) forever.
const defaultWriteTimeout = 15 * time.Second

type Client struct {
	conn            net.Conn
	udpConn         *net.UDPConn
	sendMu          lockhub.Locker
	udpMu           lockhub.Locker
	udpPeers        map[string]*partyUDPPeer
	udpPeerDrops    int
	udpTraceEvents  int
	udpSendCounter  uint32
	partySlots      map[uint16]byte
	partyAppHandler func(PartyAppPosition)
	selfUID         uint16
	selfSlot        byte
	slotKnown       bool
	maxSize         int
	writeTimeout    time.Duration
}

func Dial(ctx context.Context, address string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Keepalive lets the kernel eventually surface a half-open peer (crashed
	// server or black-holed link) instead of blocking reads forever.
	conn, err := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:         conn,
		maxSize:      DefaultMaxPacketLength,
		writeTimeout: defaultWriteTimeout,
		udpPeers:     make(map[string]*partyUDPPeer),
		partySlots:   make(map[uint16]byte),
	}
}

// SetPartyIdentity gives the wire adapter the selected session identity. The
// member slot itself is learned from PARTY_MEMBER_REALTIME_INFO after joining.
func (c *Client) SetPartyIdentity(uid uint16) {
	c.udpMu.Lock()
	c.selfUID = uid
	c.selfSlot = 0
	c.slotKnown = false
	c.udpPeers = make(map[string]*partyUDPPeer)
	c.partySlots = make(map[uint16]byte)
	c.udpPeerDrops = 0
	c.udpMu.Unlock()
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.udpMu.Lock()
	udpConn := c.udpConn
	c.udpConn = nil
	c.udpMu.Unlock()
	if udpConn != nil {
		_ = udpConn.Close()
	}
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// RegisterUDPEndpoint announces a real local UDP port before party packets
// can reference this session. A21 clients expect this endpoint to be present;
// the server's fallback port is not a valid robot endpoint.
func (c *Client) RegisterUDPEndpoint(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("s4a21 client is closed")
	}
	c.udpMu.Lock()
	if c.udpConn != nil {
		c.udpMu.Unlock()
		return nil
	}
	c.udpMu.Unlock()
	localIP := net.IPv4zero
	if addr, ok := c.conn.LocalAddr().(*net.TCPAddr); ok && addr.IP.To4() != nil {
		localIP = addr.IP.To4()
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: localIP, Port: 0})
	if err != nil {
		return fmt.Errorf("s4a21 udp endpoint: %w", err)
	}
	endpoint := udpConn.LocalAddr().(*net.UDPAddr)
	ip := endpoint.IP.To4()
	if ip == nil || ip.IsUnspecified() {
		ip = localIP.To4()
	}
	if ip == nil || ip.IsUnspecified() {
		_ = udpConn.Close()
		return fmt.Errorf("s4a21 udp endpoint has no IPv4 address")
	}
	body, err := SetUDPIPPortBody(ip, uint16(endpoint.Port), 1200)
	if err != nil {
		_ = udpConn.Close()
		return err
	}
	c.udpMu.Lock()
	if c.udpConn != nil {
		// Another caller registered first; keep its socket and discard ours.
		c.udpMu.Unlock()
		_ = udpConn.Close()
		return nil
	}
	c.udpConn = udpConn
	c.udpMu.Unlock()
	go c.servePartyUDP(udpConn)
	if err := c.send(ctx, Encode(1, CmdSetUDPIPPort, body)); err != nil {
		c.udpMu.Lock()
		if c.udpConn == udpConn {
			c.udpConn = nil
		}
		c.udpMu.Unlock()
		_ = udpConn.Close()
		return err
	}
	return nil
}

func (c *Client) Login(ctx context.Context, mID, passwordHash string) error {
	body, err := LoginBody(mID, passwordHash)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdLogin, body))
}

func (c *Client) CreateCharacter(ctx context.Context, job byte, name []byte) error {
	body, err := CreateCharacterBody(job, name)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdCreateCharacter, body))
}

func (c *Client) CheckCharacterName(ctx context.Context, name []byte) error {
	body, err := CheckCharacterNameBody(name)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdCheckCharacterName, body))
}

func (c *Client) DeleteCharacter(ctx context.Context, slot uint16, name []byte) error {
	body, err := DeleteCharacterBody(slot, name)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdDeleteCharacter, body))
}

func (c *Client) RequestCharacterRoster(ctx context.Context) error {
	return c.send(ctx, Encode(1, CmdGetUserInfo, CharacterRosterRequestBody()))
}

func (c *Client) SelectCharacter(ctx context.Context, slot uint16) error {
	return c.send(ctx, Encode(1, CmdSelectCharacter, SelectCharacterBody(slot)))
}

func (c *Client) CheckConnection(ctx context.Context) error {
	return c.send(ctx, Encode(1, CmdCheckConnection, nil))
}

// Quest methods expose only verified wire primitives. They are intentionally
// not part of shared scheduling until a complete legal task workflow has been
// proven against the target server.
func (c *Client) AcceptQuest(ctx context.Context, questID uint16) error {
	return c.send(ctx, Encode(1, CmdAcceptQuest, AcceptQuestBody(questID)))
}

func (c *Client) SetQuestTrigger(ctx context.Context, questID uint16, triggerType byte, increment bool) error {
	return c.send(ctx, Encode(1, CmdSetQuestTrigger, SetQuestTriggerBody(questID, triggerType, increment)))
}

func (c *Client) FinishQuest(ctx context.Context, questID uint16, rewardSelection int16, completionCount uint16) error {
	return c.send(ctx, Encode(1, CmdFinishQuest, FinishQuestBody(questID, rewardSelection, completionCount)))
}

// The dungeon methods expose only verified wire primitives. The backend
// session deliberately does not call them until the complete dungeon
// workflow, settlement and recovery gates are implemented.
func (c *Client) EnterSelectDungeon(ctx context.Context, dungeonID uint32) error {
	return c.send(ctx, Encode(1, CmdEnterSelectDungeon, EnterSelectDungeonBody(dungeonID)))
}

func (c *Client) SelectDungeon(ctx context.Context, dungeonID uint32, difficulty, flag1, flag2 byte) error {
	return c.send(ctx, Encode(1, CmdSelectDungeon, SelectDungeonBody(dungeonID, difficulty, flag1, flag2)))
}

func (c *Client) ChangeTutorialFlag(ctx context.Context, flagIndex uint32, rewardFlag byte) error {
	return c.send(ctx, Encode(1, CmdChangeTutorialFlag, ChangeTutorialFlagBody(flagIndex, rewardFlag)))
}

func (c *Client) FinishLoading(ctx context.Context) error {
	return c.send(ctx, Encode(1, CmdFinishLoading, FinishLoadingBody()))
}

// ChangePartyHost delegates party leadership to the member in the given roster
// slot. The A21 command carries the target slot, not the user id.
func (c *Client) ChangePartyHost(ctx context.Context, slot byte) error {
	return c.send(ctx, Encode(1, CmdChangePartyHost, []byte{slot}))
}

func (c *Client) SetUserPosition(ctx context.Context, x, y int16, direction byte, motion uint16) error {
	return c.send(ctx, Encode(1, CmdSetUserPosition, SetUserPositionBody(x, y, direction, motion)))
}

func (c *Client) SetUserArea(ctx context.Context, town, area byte, x, y int16) error {
	return c.send(ctx, Encode(1, CmdSetUserArea, SetUserAreaBody(town, area, x, y)))
}

func (c *Client) SendMessage(ctx context.Context, mode byte, targetUID uint16, targetCharacterID uint32, message []byte) error {
	body, err := SendMessageBody(mode, targetUID, targetCharacterID, message)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdSendMessage, body))
}

// Party methods are protocol-probe primitives only. They are intentionally
// not exposed through the backend session until the complete party lifecycle
// and dungeon-selection gates are verified.
func (c *Client) SetPartyInfo(ctx context.Context, settings []byte) error {
	body, err := SetPartyInfoBody(settings)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdSetPartyInfo, body))
}

func (c *Client) RequestPeer(ctx context.Context, targetUID uint16, requestType byte, peerValue int32) error {
	return c.send(ctx, Encode(1, CmdRequestPeer, RequestPeerBody(targetUID, requestType, peerValue)))
}

func (c *Client) AcceptPartyInvite(ctx context.Context, inviterUID uint16, peerValue int32) error {
	return c.send(ctx, Encode(1, CmdResponsePeer, ResponsePeerBody(inviterUID, peerValue)))
}

func (c *Client) AcceptGuildInvite(ctx context.Context) error {
	return c.send(ctx, Encode(1, CmdReplyGuildInvite, GuildInviteReplyBody(true)))
}

func (c *Client) LeaveParty(ctx context.Context) error {
	return c.send(ctx, Encode(1, CmdLeaveParty, LeavePartyBody()))
}

func (c *Client) WalkoutPartyMember(ctx context.Context, slot byte) error {
	return c.send(ctx, Encode(1, CmdWalkoutPartyMember, WalkoutPartyMemberBody(slot)))
}

func (c *Client) MoveMap(ctx context.Context, request MoveMapRequest) error {
	return c.send(ctx, Encode(1, CmdMoveMap, MoveMapBody(request)))
}

func (c *Client) Read(ctx context.Context) (Packet, error) {
	if c == nil || c.conn == nil {
		return Packet{}, fmt.Errorf("s4a21 client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resetDeadline := interruptOnCancel(ctx, c.conn.SetReadDeadline)
	packet, err := c.readFrame()
	resetDeadline()
	if err != nil && ctx.Err() != nil {
		return Packet{}, ctx.Err()
	}
	return packet, err
}

func (c *Client) readFrame() (Packet, error) {
	packet, err := ReadFrame(c.conn, c.maxSize)
	if err == nil {
		switch packet.Type {
		case NotiPartyRealtimeInfo:
			c.applyPartyRealtimeInfo(packet.Body)
		case NotiUserUDPIPPort:
			c.StartPartyUDPExchange(packet.Body)
		}
	}
	return packet, err
}

func (c *Client) Run(ctx context.Context, onPacket func(Packet) error) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("s4a21 client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resetDeadline := interruptOnCancel(ctx, c.conn.SetReadDeadline)
	defer resetDeadline()
	for {
		packet, err := c.readFrame()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if onPacket != nil {
			if err := onPacket(packet); err != nil {
				return err
			}
		}
	}
}

func (c *Client) send(ctx context.Context, frame []byte) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("s4a21 client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	writeTimeout := c.writeTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteTimeout
	}
	deadline := time.Now().Add(writeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	resetDeadline := interruptOnCancel(ctx, c.conn.SetWriteDeadline)
	defer resetDeadline()
	err := network.WriteFull(c.conn, frame)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The caller canceled (interruptOnCancel unblocks the socket but
			// the connection stays usable for the next write).
			return ctxErr
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			// Our own write timeout fired: a partial frame may have been
			// delivered, so close the connection to force a reconnect rather
			// than continuing on a desynchronized stream.
			_ = c.conn.Close()
		}
	}
	return err
}

// interruptOnCancel unblocks a pending socket operation and waits for the
// cancellation callback before clearing the deadline. Waiting prevents an old
// operation from installing an expired deadline after the next one begins.
func interruptOnCancel(ctx context.Context, setDeadline func(time.Time) error) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
		_ = setDeadline(time.Time{})
	}
}
