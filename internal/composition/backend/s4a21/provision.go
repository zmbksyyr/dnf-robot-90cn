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

func (p Provisioner) ProvisionCharacter(ctx context.Context, request shared.ProvisionCharacterRequest) (shared.ProvisionCharacterResult, error) {
	result := shared.ProvisionCharacterResult{Backend: shared.BackendS4A21, CharacterName: request.CharacterName}
	if strings.TrimSpace(request.AccountName) == "" {
		return result, fmt.Errorf("account name is required")
	}
	if request.Job < 0 || request.Job > 255 {
		return result, fmt.Errorf("job must be between 0 and 255")
	}
	name, err := charset.EncodeGBKString(strings.TrimSpace(request.CharacterName))
	if err != nil {
		return result, err
	}
	if len(name) < 2 || len(name) > 18 {
		return result, fmt.Errorf("S4A21 character name must be 2..18 GBK bytes")
	}
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
	if err := client.CreateCharacter(ctx, byte(request.Job), name); err != nil {
		return result, err
	}
	if err := waitFor(ctx, client, protocol.CmdCreateCharacter, 1); err != nil {
		return result, fmt.Errorf("S4A21 create character: %w", err)
	}
	if err := waitFor(ctx, client, protocol.NotiCharacterList, 0); err != nil {
		return result, fmt.Errorf("S4A21 character list refresh: %w", err)
	}
	result.Created = true
	return result, nil
}

func waitFor(ctx context.Context, client *protocol.Client, typ uint16, command byte) error {
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return err
		}
		if packet.Type != typ || packet.Command != command {
			continue
		}
		if command == 1 && (len(packet.Body) == 0 || packet.Body[0] != 1) {
			return fmt.Errorf("command 0x%04X rejected with body %v", typ, packet.Body)
		}
		return nil
	}
}

var _ shared.CharacterProvisioner = Provisioner{}
