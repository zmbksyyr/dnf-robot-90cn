package cn90

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// AccountBinder mirrors the DNF90 launcher's local client binding: it posts
// this process id and the game account id to the loopback admin API, which the
// game listener consults (through the TCP owner process id) when a new game
// connection is accepted.
type AccountBinder struct {
	// Endpoint is the admin listener address from instance.json
	// (server.adminListen), for example 127.0.0.1:18111.
	Endpoint string
	// Token is the instance admin token (server.adminToken).
	Token string

	client *http.Client
	// mu serializes registration and the following dial: the server resolves
	// the owner pid at accept time, so the registry entry must not be replaced
	// by the next robot before this connection was accepted.
	mu lockhub.Locker
}

func NewAccountBinder(endpoint, token string) *AccountBinder {
	endpoint = strings.TrimSpace(endpoint)
	token = strings.TrimSpace(token)
	if endpoint == "" || token == "" {
		return nil
	}
	return &AccountBinder{
		Endpoint: endpoint,
		Token:    token,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// dialBoundSession opens one game-channel connection for an account. When a
// binder is configured, registration, dial and the first inbound packet stay
// inside one critical section: the server resolves the TCP owner process id at
// accept time, so a later registration for the next robot must not replace
// this connection's account entry before its accept lookup ran.
func dialBoundSession(ctx context.Context, binder *AccountBinder, address, account string) (*protocol.Client, *protocol.Packet, error) {
	if binder == nil {
		client, err := protocol.Dial(ctx, address)
		if err != nil {
			return nil, nil, err
		}
		packet, err := client.Read(ctx)
		if err != nil {
			_ = client.Close()
			return nil, nil, err
		}
		return client, &packet, nil
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	if err := binder.Register(ctx, account); err != nil {
		foundationlog.Robotf("CN90_ACCOUNT_REGISTER_FAILED account=%s err=%v\n", account, err)
	}
	client, err := protocol.Dial(ctx, address)
	if err != nil {
		return nil, nil, err
	}
	packet, err := client.Read(ctx)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, &packet, nil
}

// Register binds the current process id to the account id.
func (b *AccountBinder) Register(ctx context.Context, accountID string) error {
	if b == nil {
		return nil
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("account id is required")
	}
	pid := os.Getpid()
	if pid <= 0 {
		return fmt.Errorf("process id %d is not usable for account binding", pid)
	}
	payload, err := json.Marshal(struct {
		PID       int    `json:"pid"`
		AccountID string `json:"account_id"`
	}{PID: pid, AccountID: accountID})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+b.Endpoint+"/local/client-account", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Token", b.Token)
	client := b.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("90CN account binding: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("90CN account binding rejected: HTTP %d %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}
