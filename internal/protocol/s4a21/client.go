package s4a21

import (
	"context"
	"fmt"
	"net"
	"time"

	"robot/internal/foundation/lockhub"
)

const DefaultMaxPacketLength = 1024 * 1024

type Client struct {
	conn    net.Conn
	sendMu  lockhub.Locker
	maxSize int
}

func Dial(ctx context.Context, address string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

func NewClient(conn net.Conn) *Client { return &Client{conn: conn, maxSize: DefaultMaxPacketLength} }

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
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

func (c *Client) DeleteCharacter(ctx context.Context, slot uint16, name []byte) error {
	body, err := DeleteCharacterBody(slot, name)
	if err != nil {
		return err
	}
	return c.send(ctx, Encode(1, CmdDeleteCharacter, body))
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

func (c *Client) AcceptPartyInvite(ctx context.Context, inviterUID uint16) error {
	return c.send(ctx, Encode(1, CmdResponsePeer, ResponsePeerBody(inviterUID, 0)))
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
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.conn.SetReadDeadline(time.Now())
		case <-stop:
		}
	}()
	packet, err := ReadFrame(c.conn, c.maxSize)
	close(stop)
	_ = c.conn.SetReadDeadline(time.Time{})
	if err != nil && ctx.Err() != nil {
		return Packet{}, ctx.Err()
	}
	return packet, err
}

func (c *Client) Run(ctx context.Context, onPacket func(Packet) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		packet, err := c.Read(ctx)
		if err != nil {
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
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
		defer c.conn.SetWriteDeadline(time.Time{})
	}
	_, err := c.conn.Write(frame)
	return err
}
