package s4a21

import (
	"context"
	"fmt"
	"strings"
	"time"

	"robot/internal/foundation/charset"
	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

type Provisioner struct {
	Address string
	Timeout time.Duration
}

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
	result := shared.ProvisionCharacterResult{Backend: shared.BackendS4A21, CharacterName: request.CharacterName, RobotUID: request.RobotUID}
	if strings.TrimSpace(request.AccountName) == "" {
		return result, fmt.Errorf("account name is required")
	}
	if request.Job < 0 || request.Job > 255 {
		return result, fmt.Errorf("job must be between 0 and 255")
	}
	requestedName := strings.TrimSpace(request.CharacterName)
	fallbackName := fallbackCharacterName(request)
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
		return result, fmt.Errorf("S4A21 login: %w", err)
	}
	if err := client.RequestCharacterRoster(ctx); err != nil {
		return result, fmt.Errorf("S4A21 request character roster: %w", err)
	}
	rosterPacket, err := waitPacket(ctx, client, protocol.NotiCharacterList, 0)
	if err != nil {
		return result, fmt.Errorf("S4A21 character roster: %w", err)
	}
	roster, err := protocol.DecodeCharacterRoster(rosterPacket.Body)
	if err != nil && len(rosterPacket.Body) > 1 {
		return result, fmt.Errorf("S4A21 character roster decode: %w", err)
	}
	for _, candidate := range []string{requestedName, fallbackName} {
		for _, character := range roster {
			if character.Name != candidate {
				continue
			}
			slot := character.Slot
			result.CharacterName = candidate
			result.BackendSlot = &slot
			// Created means the requested protocol identity is ready for local
			// registration. It also covers adoption after a prior partial batch.
			result.Created = true
			return result, nil
		}
	}
	createdName := ""
	for index, candidate := range []string{requestedName, fallbackName} {
		if index == 1 && candidate == requestedName {
			break
		}
		name, err := charset.EncodeGBKString(candidate)
		if err != nil {
			return result, err
		}
		if len(name) < 2 || len(name) > 18 {
			if index == 0 {
				continue
			}
			return result, fmt.Errorf("S4A21 fallback character name must be 2..18 GBK bytes")
		}
		if err := client.CheckCharacterName(ctx, name); err != nil {
			return result, fmt.Errorf("S4A21 check character name: %w", err)
		}
		nameAck, err := waitPacketRaw(ctx, client, protocol.CmdCheckCharacterName, 1)
		if err != nil {
			return result, fmt.Errorf("S4A21 check character name: %w", err)
		}
		if !commandAccepted(nameAck.Body) {
			code := commandErrorCode(nameAck.Body)
			if index == 0 && (code == 24 || code == 159) {
				continue
			}
			return result, fmt.Errorf("S4A21 check character name: command 0x%04X rejected with body %v", protocol.CmdCheckCharacterName, nameAck.Body)
		}
		if err := client.CreateCharacter(ctx, byte(request.Job), name); err != nil {
			return result, err
		}
		ack, err := waitPacketRaw(ctx, client, protocol.CmdCreateCharacter, 1)
		if err != nil {
			return result, fmt.Errorf("S4A21 create character: %w", err)
		}
		if commandAccepted(ack.Body) {
			createdName = candidate
			break
		}
		code := commandErrorCode(ack.Body)
		// Keep the same fallback for the race between availability check and
		// creation, where another session may reserve the requested name.
		if index == 0 && (code == 24 || code == 159) {
			continue
		}
		return result, fmt.Errorf("S4A21 create character: command 0x%04X rejected with body %v", protocol.CmdCreateCharacter, ack.Body)
	}
	if createdName == "" {
		return result, fmt.Errorf("S4A21 create character exhausted name candidates")
	}
	packet, err := waitPacket(ctx, client, protocol.NotiCharacterList, 0)
	if err != nil {
		return result, fmt.Errorf("S4A21 character list refresh: %w", err)
	}
	roster, err = protocol.DecodeCharacterRoster(packet.Body)
	if err != nil {
		// Older A21 builds acknowledge the refresh with an empty marker and
		// expose the roster only on the next login. Creation still succeeded,
		// but no backend slot can be recorded from that response.
		if len(packet.Body) > 1 {
			return result, fmt.Errorf("S4A21 character list decode: %w", err)
		}
	}
	for _, character := range roster {
		if character.Name == createdName {
			slot := character.Slot
			result.BackendSlot = &slot
			break
		}
	}
	result.CharacterName = createdName
	result.Created = true
	return result, nil
}

func fallbackCharacterName(request shared.ProvisionCharacterRequest) string {
	if request.RobotUID > 0 {
		return fmt.Sprintf("rb%d", request.RobotUID)
	}
	value := uint32(2166136261)
	for _, b := range []byte(request.AccountName + "\x00" + request.CharacterName) {
		value ^= uint32(b)
		value *= 16777619
	}
	return fmt.Sprintf("rb%08x", value)
}

func commandAccepted(body []byte) bool {
	return len(body) > 0 && body[0] == 1
}

func commandErrorCode(body []byte) byte {
	if len(body) < 2 || body[0] != 0 {
		return 0
	}
	return body[1]
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
		return protocol.Packet{}, fmt.Errorf("command 0x%04X rejected with body %v", typ, packet.Body)
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
