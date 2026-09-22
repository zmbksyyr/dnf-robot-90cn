package s4a21

import (
	"context"
	"fmt"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

const dungeonPacketWait = 5 * time.Second

// enterSingleDungeon is intentionally private. It is the smallest verified
// S4A21 workflow and is not yet part of the shared RobotSession contract.
// tutorial must be supplied by caller-owned robot state; this method never
// guesses environment or tutorial status from packets.
func (s *Session) enterSingleDungeon(ctx context.Context, dungeonID uint32, tutorial bool) (dungeonRunSnapshot, error) {
	var state dungeonRunState
	if err := state.BeginSelection(dungeonID); err != nil {
		return dungeonRunSnapshot{}, err
	}
	if s == nil || s.client == nil {
		return dungeonRunSnapshot{}, fmt.Errorf("S4A21 session is not ready")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	packets := make(chan protocol.Packet, 16)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		select {
		case packets <- packet:
		case <-ctx.Done():
		}
	})
	defer cleanup()

	if err := s.client.EnterSelectDungeon(ctx, dungeonID); err != nil {
		return dungeonRunSnapshot{}, err
	}
	if err := requireDungeonAck(ctx, packets, protocol.CmdEnterSelectDungeon); err != nil {
		return dungeonRunSnapshot{}, err
	}
	if err := s.client.SelectDungeon(ctx, dungeonID, 0, 0, 0); err != nil {
		return dungeonRunSnapshot{}, err
	}
	if err := state.BeginEntry(); err != nil {
		return dungeonRunSnapshot{}, err
	}
	if tutorial {
		if err := s.client.ChangeTutorialFlag(ctx, 30, 1); err != nil {
			return dungeonRunSnapshot{}, err
		}
	}
	startMap, err := waitDungeonPacket(ctx, packets, protocol.NotiStartMap)
	if err != nil {
		return dungeonRunSnapshot{}, err
	}
	if err := state.AcceptStartMap(startMap.Body); err != nil {
		return dungeonRunSnapshot{}, err
	}
	if err := s.client.FinishLoading(ctx); err != nil {
		return dungeonRunSnapshot{}, err
	}
	finish, err := waitDungeonPacket(ctx, packets, protocol.NotiFinishLoading)
	if err != nil {
		return dungeonRunSnapshot{}, err
	}
	if err := state.AcceptFinishLoading(finish.Body); err != nil {
		return dungeonRunSnapshot{}, err
	}
	return state.Snapshot(), nil
}

func requireDungeonAck(ctx context.Context, packets <-chan protocol.Packet, typ uint16) error {
	packet, err := waitDungeonPacket(ctx, packets, typ)
	if err != nil {
		return err
	}
	if packet.Command != 1 || len(packet.Body) != 1 || packet.Body[0] != 1 {
		return fmt.Errorf("S4A21 command 0x%04X rejected", typ)
	}
	return nil
}

func waitDungeonPacket(ctx context.Context, packets <-chan protocol.Packet, typ uint16) (protocol.Packet, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, dungeonPacketWait)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			return protocol.Packet{}, waitCtx.Err()
		case packet := <-packets:
			if packet.Type == typ {
				return packet, nil
			}
		}
	}
}
