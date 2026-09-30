package cn90

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"robot/internal/foundation/charset"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

type Provisioner struct {
	Address string
	Timeout time.Duration
}

type AccountRosterConflictError struct {
	Account string
	Count   int
}

func (e *AccountRosterConflictError) Error() string {
	return fmt.Sprintf("90CN robot account %q contains %d characters; refusing automatic adoption or deletion", e.Account, e.Count)
}

var provisionNameSequence atomic.Uint64

// ProvisionCharacters executes the same verified network workflow for each
// request and returns partial results when the batch is interrupted. Account
// and character state remain entirely server-owned.
func (p Provisioner) ProvisionCharacters(ctx context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	results := make([]shared.ProvisionCharacterResult, 0, len(requests))
	for _, request := range requests {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			default:
			}
		}
		result, err := p.ProvisionCharacter(ctx, request)
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func (p Provisioner) ProvisionCharacter(ctx context.Context, request shared.ProvisionCharacterRequest) (shared.ProvisionCharacterResult, error) {
	result := shared.ProvisionCharacterResult{Backend: BackendID, CharacterName: request.CharacterName, RobotUID: request.RobotUID}
	if strings.TrimSpace(request.AccountName) == "" {
		return result, fmt.Errorf("90CN provision account name is required")
	}
	if request.Job < 0 || request.Job > 255 {
		return result, fmt.Errorf("90CN provision job must be between 0 and 255")
	}
	requestedName := strings.TrimSpace(request.CharacterName)
	if ctx == nil {
		ctx = context.Background()
	}
	if p.Timeout <= 0 {
		p.Timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	client, err := protocol.Dial(ctx, p.Address)
	if err != nil {
		return result, err
	}
	defer client.Close()
	if err := client.Login(ctx, request.AccountName, request.PasswordHash); err != nil {
		return result, err
	}
	if err := waitFor(ctx, client, protocol.CmdLogin, 1); err != nil {
		return result, fmt.Errorf("90CN login: %w", err)
	}
	if err := client.RequestCharacterRoster(ctx); err != nil {
		return result, fmt.Errorf("90CN request character roster: %w", err)
	}
	rosterPacket, err := waitPacket(ctx, client, protocol.NotiCharacterList, 0)
	if err != nil {
		return result, fmt.Errorf("90CN character roster: %w", err)
	}
	roster, err := protocol.DecodeCharacterRoster(rosterPacket.Body)
	if err != nil && len(rosterPacket.Body) > 1 {
		return result, fmt.Errorf("90CN character roster decode: %w", err)
	}
	if len(roster) == 1 {
		character := roster[0]
		slot := character.Slot
		result.CharacterName = character.Name
		result.BackendSlot = &slot
		result.ProfileKnown = true
		result.Job = int(character.Job)
		result.Grow = int(character.Grow)
		result.Level = int(character.Level)
		// Successful login to the deterministic robot account is the durable
		// ownership proof. The character name may change across config resets.
		result.Created = true
		result.Reused = true
		return result, nil
	}
	if len(roster) > 1 {
		return result, &AccountRosterConflictError{Account: request.AccountName, Count: len(roster)}
	}
	createdName := ""
	candidates := provisionCharacterNameCandidates(request, requestedName)
	for index, candidate := range candidates {
		name, err := charset.EncodeGBKString(candidate)
		if err != nil {
			if index < len(candidates)-1 {
				continue
			}
			return result, err
		}
		if len(name) < 2 || len(name) > 18 {
			if index < len(candidates)-1 {
				continue
			}
			return result, fmt.Errorf("90CN character name candidates must be 2..18 GBK bytes")
		}
		if err := client.CheckCharacterName(ctx, name); err != nil {
			return result, fmt.Errorf("90CN check character name: %w", err)
		}
		nameAck, err := waitPacketRaw(ctx, client, protocol.CmdCheckCharacterName, 1)
		if err != nil {
			return result, fmt.Errorf("90CN check character name: %w", err)
		}
		if !commandAccepted(nameAck.Body) {
			code := commandErrorCode(nameAck.Body)
			if (code == 24 || code == 159) && index < len(candidates)-1 {
				continue
			}
			return result, fmt.Errorf("90CN check character name: command 0x%04X rejected with body %v", protocol.CmdCheckCharacterName, nameAck.Body)
		}
		if err := client.CreateCharacter(ctx, byte(request.Job), name); err != nil {
			return result, err
		}
		ack, err := waitPacketRaw(ctx, client, protocol.CmdCreateCharacter, 1)
		if err != nil {
			return result, fmt.Errorf("90CN create character: %w", err)
		}
		if commandAccepted(ack.Body) {
			createdName = candidate
			break
		}
		code := commandErrorCode(ack.Body)
		// Keep the same fallback for the race between availability check and
		// creation, where another session may reserve the requested name.
		// Code 4 is 90CN's generic persistence failure. Soft-deleted names
		// remain under a unique index even though CHECK_NAME reports them as
		// available, so retry with a fresh protocol name.
		if (code == 4 || code == 24 || code == 159) && index < len(candidates)-1 {
			continue
		}
		return result, fmt.Errorf("90CN create character: command 0x%04X rejected with body %v", protocol.CmdCreateCharacter, ack.Body)
	}
	if createdName == "" {
		return result, fmt.Errorf("90CN create character exhausted name candidates")
	}
	packet, err := waitPacket(ctx, client, protocol.NotiCharacterList, 0)
	if err != nil {
		return result, fmt.Errorf("90CN character list refresh: %w", err)
	}
	roster, err = protocol.DecodeCharacterRoster(packet.Body)
	if err != nil {
		// Older 90CN builds acknowledge the refresh with an empty marker and
		// expose the roster only on the next login. Creation still succeeded,
		// but no backend slot can be recorded from that response.
		if len(packet.Body) > 1 {
			return result, fmt.Errorf("90CN character list decode: %w", err)
		}
	}
	for _, character := range roster {
		if character.Name == createdName {
			slot := character.Slot
			result.BackendSlot = &slot
			result.ProfileKnown = true
			result.Job = int(character.Job)
			result.Grow = int(character.Grow)
			result.Level = int(character.Level)
			break
		}
	}
	result.CharacterName = createdName
	result.Created = true
	return result, nil
}

func provisionCharacterNameCandidates(request shared.ProvisionCharacterRequest, requestedName string) []string {
	candidates := make([]string, 0, 13)
	seen := make(map[string]struct{}, 13)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		candidates = append(candidates, name)
	}
	add(requestedName)
	seed := uint64(time.Now().UnixNano()) ^ provisionNameSequence.Add(1)*0x9e3779b97f4a7c15
	for _, value := range []byte(request.AccountName) {
		seed ^= uint64(value)
		seed *= 1099511628211
	}
	for attempt := 0; attempt < 12; attempt++ {
		add(freshCharacterName(requestedName, seed+uint64(attempt)*0x9e3779b97f4a7c15))
	}
	return candidates
}

const cn90NameSuffixRunes = "风云星月山海天涯剑影霜雪龙吟夜雨晨光流火青岚苍穹逐梦无双凌墨羽寒江孤城长歌惊鸿逍遥清欢归舟听潮踏歌流萤锦书朝暮浮生"

func freshCharacterName(base string, seed uint64) string {
	alphabet := []rune(cn90NameSuffixRunes)
	suffixRunes := make([]rune, 4)
	value := seed
	for index := range suffixRunes {
		suffixRunes[index] = alphabet[value%uint64(len(alphabet))]
		value = value/uint64(len(alphabet)) + 0x9e3779b97f4a7c15
	}
	suffix := string(suffixRunes)
	suffixBytes, _ := charset.EncodeGBKString(suffix)
	remaining := 18 - len(suffixBytes)
	var prefix strings.Builder
	used := 0
	for _, r := range strings.TrimSpace(base) {
		if !unicode.Is(unicode.Han, r) {
			continue
		}
		encoded, err := charset.EncodeGBKString(string(r))
		if err != nil || used+len(encoded) > remaining {
			continue
		}
		prefix.WriteRune(r)
		used += len(encoded)
	}
	if prefix.Len() == 0 {
		prefix.WriteString("旅人")
	}
	return prefix.String() + suffix
}

func commandAccepted(body []byte) bool {
	return len(body) > 0 && body[0] == 1
}

func commandErrorCode(body []byte) byte {
	if len(body) == 1 {
		if body[0] != 1 {
			return body[0]
		}
		return 0
	}
	if len(body) >= 2 && body[0] == 0 {
		return body[1]
	}
	return 0
}

func waitFor(ctx context.Context, client *protocol.Client, typ uint16, command byte) error {
	_, err := waitPacket(ctx, client, typ, command)
	return err
}

func waitPacket(ctx context.Context, client *protocol.Client, typ uint16, command byte) (protocol.Packet, error) {
	packet, err := waitPacketRaw(ctx, client, typ, command)
	if err != nil {
		return protocol.Packet{}, err
	}
	if command == 1 && !commandAccepted(packet.Body) {
		return protocol.Packet{}, fmt.Errorf("90CN command 0x%04X rejected with body %v", typ, packet.Body)
	}
	return packet, nil
}

func waitPacketRaw(ctx context.Context, client *protocol.Client, typ uint16, command byte) (protocol.Packet, error) {
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return protocol.Packet{}, err
		}
		if packet.Type != typ || packet.Command != command {
			continue
		}
		return packet, nil
	}
}

var _ shared.CharacterProvisioner = Provisioner{}
var _ shared.BatchCharacterProvisioner = Provisioner{}
