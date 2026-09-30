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
	ConnectHost string
	Channels    channelCatalog
	Timeout     time.Duration
	Binder      *AccountBinder
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
	address, err := channelAddress(p.ConnectHost, p.Channels, request.AccountName)
	if err != nil {
		return result, err
	}
	client, first, err := dialBoundSession(ctx, p.Binder, address, request.AccountName)
	if err != nil {
		return result, err
	}
	defer func() {
		// Announce an orderly channel exit so the server does not log a read
		// failure when this short-lived provisioning connection closes.
		exitCtx, cancelExit := context.WithTimeout(context.Background(), 2*time.Second)
		_ = client.Exit(exitCtx)
		cancelExit()
		_ = client.Close()
	}()
	if _, err := client.CompleteHandshakeFrom(ctx, first); err != nil {
		return result, fmt.Errorf("90CN login: %w", err)
	}
	if err := client.RequestRoster(ctx); err != nil {
		return result, fmt.Errorf("90CN request character roster: %w", err)
	}
	rosterPacket, err := waitUpperPacket(ctx, client, protocol.ClassNotice, protocol.NotiCharacterList)
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
		if len(name) < 2 || len(name) > 30 {
			if index < len(candidates)-1 {
				continue
			}
			return result, fmt.Errorf("90CN character name candidates must be 2..30 GBK bytes")
		}
		if err := client.CheckCharacterName(ctx, name); err != nil {
			return result, fmt.Errorf("90CN check character name: %w", err)
		}
		nameAck, err := waitUpperPacket(ctx, client, protocol.ClassCommand, protocol.ResponseCheckName)
		if err != nil {
			return result, fmt.Errorf("90CN check character name: %w", err)
		}
		if ok, code := protocol.CheckNameResult(nameAck.Body); !ok {
			// 0x00 marks a duplicate, 0x14 a parse/query failure.
			if (code == 0x00 || code == 0x14) && index < len(candidates)-1 {
				continue
			}
			return result, fmt.Errorf("90CN check character name rejected with code 0x%02X", code)
		}
		if err := client.CreateCharacter(ctx, byte(request.Job), name); err != nil {
			return result, err
		}
		ack, err := waitUpperPacket(ctx, client, protocol.ClassCommand, protocol.ResponseCreate)
		if err != nil {
			return result, fmt.Errorf("90CN create character: %w", err)
		}
		if _, code, err := protocol.CreateResultCharacterID(ack.Body); err != nil {
			return result, fmt.Errorf("90CN create character: %w", err)
		} else if code != 0 {
			// 0x04 is the generic create rejection; keep the same fallback for
			// the race between the availability check and creation.
			if code == 0x04 && index < len(candidates)-1 {
				continue
			}
			return result, fmt.Errorf("90CN create character rejected with code 0x%02X", code)
		}
		createdName = candidate
		break
	}
	if createdName == "" {
		return result, fmt.Errorf("90CN create character exhausted name candidates")
	}
	packet, err := waitUpperPacket(ctx, client, protocol.ClassNotice, protocol.NotiCharacterList)
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

var _ shared.CharacterProvisioner = Provisioner{}
var _ shared.BatchCharacterProvisioner = Provisioner{}
