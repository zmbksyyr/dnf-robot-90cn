package cn90

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	"robot/internal/foundation/charset"
	protocol "robot/internal/protocol/cn90"
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
		return robotcap.CleanupResult{}, fmt.Errorf("90CN cleaner dependencies are incomplete")
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
	ambiguousNames := make(map[string]struct{})
	for _, identity := range identities {
		if _, exists := identityByName[identity.CharacterName]; exists {
			// Two accounts can share a character name in the roster. Deleting
			// through a guessed identity could hit the wrong account, so the
			// name is marked ambiguous and its robots are protected.
			ambiguousNames[identity.CharacterName] = struct{}{}
			continue
		}
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
			candidate.Reason = "90CN robot identity is missing"
		} else if _, ambiguous := ambiguousNames[robot.Name]; ambiguous {
			candidate.Protected = true
			candidate.Reason = "90CN robot identity is ambiguous (duplicate character name)"
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
			candidate.Protected, candidate.Reason = true, "90CN account cleanup was not confirmed"
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
	Address string
	Timeout time.Duration
	Binder  *AccountBinder
}

// DeleteCharacter clears the roster of one robot-owned account through the
// public game protocol. One account represents one robot identity; clearing
// stale siblings prevents interrupted cleanup from exhausting character slots.
func (d CharacterDeleter) DeleteCharacter(ctx context.Context, identity robotstate.Identity) (bool, error) {
	if strings.TrimSpace(identity.Account) == "" || strings.TrimSpace(identity.CharacterName) == "" {
		return false, fmt.Errorf("90CN delete requires account and character name")
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
	client, first, err := dialBoundSession(ctx, d.Binder, d.Address, identity.Account)
	if err != nil {
		return false, err
	}
	defer client.Close()
	if _, err := client.CompleteHandshakeFrom(ctx, first); err != nil {
		return false, fmt.Errorf("90CN delete login: %w", err)
	}
	if err := client.RequestRoster(ctx); err != nil {
		return false, err
	}
	rosterPacket, err := waitUpperPacket(ctx, client, protocol.ClassNotice, protocol.NotiCharacterList)
	if err != nil {
		return false, fmt.Errorf("90CN delete character list: %w", err)
	}
	if err := clearRobotAccountRoster(ctx, client, rosterPacket.Body); err != nil {
		return false, err
	}
	return true, nil
}

// clearRobotAccountRoster deletes every character remaining on the account,
// not only the planned robot. Robot accounts are dedicated, so this is the
// intended cleanup boundary; the post-condition below verifies the roster is
// empty before the account is reported as deleted.
func clearRobotAccountRoster(ctx context.Context, client *protocol.Client, rosterBody []byte) error {
	roster, err := protocol.DecodeCharacterRoster(rosterBody)
	if err != nil {
		return fmt.Errorf("90CN delete character list decode: %w", err)
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
	if err := client.RequestRoster(ctx); err != nil {
		return err
	}
	packet, err := waitUpperPacket(ctx, client, protocol.ClassNotice, protocol.NotiCharacterList)
	if err != nil {
		return fmt.Errorf("90CN confirm deleted character list: %w", err)
	}
	remaining, err := protocol.DecodeCharacterRoster(packet.Body)
	if err != nil {
		return fmt.Errorf("90CN confirm deleted character list decode: %w", err)
	}
	if len(remaining) != 0 {
		return fmt.Errorf("90CN account roster still contains %d characters after cleanup", len(remaining))
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
	ack, err := waitUpperPacket(ctx, client, protocol.ClassCommand, protocol.ResponseDelete)
	if err != nil {
		return fmt.Errorf("90CN delete character: %w", err)
	}
	if ok, code := protocol.DeleteResult(ack.Body); !ok {
		return fmt.Errorf("90CN delete character rejected with code 0x%02X", code)
	}
	return nil
}
