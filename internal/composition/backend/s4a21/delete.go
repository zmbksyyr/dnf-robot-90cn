package s4a21

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	"robot/internal/foundation/charset"
	protocol "robot/internal/protocol/s4a21"
)

type deleteProtocol interface {
	DeleteCharacter(context.Context, robotstate.Identity) (bool, error)
}

type cleanupState interface {
	robotstate.Directory
	robotstate.IdentityDirectory
	robotstate.RobotRemover
}

type sessionCloser interface {
	Close(int) error
}

type RobotCleaner struct {
	Protocol deleteProtocol
	State    cleanupState
	Sessions sessionCloser
}

func (c RobotCleaner) CleanupRobots(ctx context.Context, request robotcap.CleanupRequest) (robotcap.CleanupResult, error) {
	if c.Protocol == nil || c.State == nil {
		return robotcap.CleanupResult{}, fmt.Errorf("S4A21 cleaner dependencies are incomplete")
	}
	robots, err := c.State.SelectRobots(ctx, robotcap.CommandRequest{Count: int(^uint(0) >> 1)})
	if err != nil {
		return robotcap.CleanupResult{}, err
	}
	identities, err := c.State.Identities(ctx, BackendID)
	if err != nil {
		return robotcap.CleanupResult{}, err
	}
	identityByName := make(map[string]robotstate.Identity, len(identities))
	for _, identity := range identities {
		identityByName[identity.CharacterName] = identity
	}
	wanted := make(map[int]struct{}, len(request.UIDs))
	for _, uid := range request.UIDs {
		wanted[uid] = struct{}{}
	}
	result := robotcap.CleanupResult{DryRun: !request.Force}
	for _, robot := range robots {
		if !cleanupUIDSelected(robot.UID, request, wanted) {
			continue
		}
		identity, ok := identityByName[robot.Name]
		candidate := robotcap.CleanupCandidate{UID: robot.UID, CID: robot.CID, Name: robot.Name, Account: identity.Account}
		if !ok {
			candidate.Protected = true
			candidate.Reason = "S4A21 robot identity is missing"
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	result.Requested = len(result.Candidates)
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		if candidate.Protected {
			result.Skipped++
			continue
		}
		if !request.Force {
			continue
		}
		if c.Sessions != nil {
			if err := c.Sessions.Close(candidate.UID); err != nil {
				candidate.Protected, candidate.Reason = true, err.Error()
				result.Skipped++
				continue
			}
		}
		identity := identityByName[candidate.Name]
		confirmed, err := c.Protocol.DeleteCharacter(ctx, identity)
		if err != nil {
			candidate.Protected, candidate.Reason = true, err.Error()
			result.Skipped++
			continue
		}
		if !confirmed {
			candidate.Protected, candidate.Reason = true, "S4A21 account cleanup was not confirmed"
			result.Skipped++
			continue
		}
		if err := c.State.RemoveRobots(ctx, []int{candidate.UID}); err != nil {
			candidate.Protected, candidate.Reason = true, err.Error()
			result.Skipped++
			continue
		}
		candidate.Deleted = true
		result.Deleted++
	}
	return result, nil
}

func cleanupUIDSelected(uid int, request robotcap.CleanupRequest, wanted map[int]struct{}) bool {
	if len(wanted) > 0 {
		_, ok := wanted[uid]
		return ok
	}
	if request.MinUID > 0 && uid < request.MinUID {
		return false
	}
	if request.MaxUID > 0 && uid > request.MaxUID {
		return false
	}
	return true
}

type CharacterDeleter struct {
	Address      string
	Timeout      time.Duration
	PasswordHash string
}

// DeleteCharacter clears the roster of one robot-owned account through the
// public game protocol. One account represents one robot identity; clearing
// stale siblings prevents interrupted cleanup from exhausting character slots.
func (d CharacterDeleter) DeleteCharacter(ctx context.Context, identity robotstate.Identity) (bool, error) {
	if strings.TrimSpace(identity.Account) == "" || strings.TrimSpace(identity.CharacterName) == "" {
		return false, fmt.Errorf("S4A21 delete requires account and character name")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := protocol.Dial(ctx, d.Address)
	if err != nil {
		return false, err
	}
	defer client.Close()
	if err := client.Login(ctx, identity.Account, d.PasswordHash); err != nil {
		return false, err
	}
	if err := waitFor(ctx, client, protocol.CmdLogin, 1); err != nil {
		return false, fmt.Errorf("S4A21 delete login: %w", err)
	}
	if err := client.RequestCharacterRoster(ctx); err != nil {
		return false, err
	}
	rosterPacket, err := waitPacket(ctx, client, protocol.NotiCharacterList, 0)
	if err != nil {
		return false, fmt.Errorf("S4A21 delete character list: %w", err)
	}
	if err := clearRobotAccountRoster(ctx, client, rosterPacket.Body); err != nil {
		return false, err
	}
	return true, nil
}

func clearRobotAccountRoster(ctx context.Context, client *protocol.Client, rosterBody []byte) error {
	roster, err := protocol.DecodeCharacterRoster(rosterBody)
	if err != nil {
		return fmt.Errorf("S4A21 delete character list decode: %w", err)
	}
	if len(roster) == 0 {
		return nil
	}
	// Delete from the highest slot so lower indices remain valid while the
	// server compacts the account roster after every successful deletion.
	sort.Slice(roster, func(i, j int) bool { return roster[i].Slot > roster[j].Slot })
	for _, character := range roster {
		if err := deleteRosterEntry(ctx, client, character); err != nil {
			return err
		}
	}
	if err := client.RequestCharacterRoster(ctx); err != nil {
		return err
	}
	packet, err := waitPacket(ctx, client, protocol.NotiCharacterList, 0)
	if err != nil {
		return fmt.Errorf("S4A21 confirm deleted character list: %w", err)
	}
	remaining, err := protocol.DecodeCharacterRoster(packet.Body)
	if err != nil {
		return fmt.Errorf("S4A21 confirm deleted character list decode: %w", err)
	}
	if len(remaining) != 0 {
		return fmt.Errorf("S4A21 account roster still contains %d characters after cleanup", len(remaining))
	}
	return nil
}

func deleteRosterEntry(ctx context.Context, client *protocol.Client, character protocol.CharacterRosterEntry) error {
	name := character.NameRaw
	if len(name) == 0 {
		var err error
		name, err = charset.EncodeGBKString(character.Name)
		if err != nil {
			return err
		}
	}
	if err := client.DeleteCharacter(ctx, character.Slot, name); err != nil {
		return err
	}
	ack, err := waitPacket(ctx, client, protocol.CmdDeleteCharacter, 1)
	if err != nil {
		return fmt.Errorf("S4A21 delete character: %w", err)
	}
	if len(ack.Body) != 4 || ack.Body[0] != 1 || binary.LittleEndian.Uint16(ack.Body[2:4]) != character.Slot {
		return fmt.Errorf("S4A21 delete character returned invalid ACK %v", ack.Body)
	}
	return nil
}
