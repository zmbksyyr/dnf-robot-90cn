package s4a21

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	"robot/internal/foundation/charset"
	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
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
	identities, err := c.State.Identities(ctx, shared.BackendS4A21)
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
		if _, err := c.Protocol.DeleteCharacter(ctx, identity); err != nil {
			candidate.Protected, candidate.Reason = true, err.Error()
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

// DeleteCharacter deletes through the public game protocol only. A missing
// roster entry is treated as already deleted so interrupted cleanup can be
// retried without touching the simulator database.
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
	return deleteCharacterFromRoster(ctx, client, identity.CharacterName, rosterPacket.Body)
}

func deleteCharacterFromRoster(ctx context.Context, client *protocol.Client, characterName string, rosterBody []byte) (bool, error) {
	roster, err := protocol.DecodeCharacterRoster(rosterBody)
	if err != nil {
		return false, fmt.Errorf("S4A21 delete character list decode: %w", err)
	}
	var slot uint16
	found := false
	for _, character := range roster {
		if character.Name == characterName {
			slot, found = character.Slot, true
			break
		}
	}
	if !found {
		return false, nil
	}
	name, err := charset.EncodeGBKString(characterName)
	if err != nil {
		return false, err
	}
	if err := client.DeleteCharacter(ctx, slot, name); err != nil {
		return false, err
	}
	ack, err := waitPacket(ctx, client, protocol.CmdDeleteCharacter, 1)
	if err != nil {
		return false, fmt.Errorf("S4A21 delete character: %w", err)
	}
	if len(ack.Body) != 4 || ack.Body[0] != 1 || binary.LittleEndian.Uint16(ack.Body[2:4]) != slot {
		return false, fmt.Errorf("S4A21 delete character returned invalid ACK %v", ack.Body)
	}
	return true, nil
}
