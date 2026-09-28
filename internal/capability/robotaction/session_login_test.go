package robotaction

import (
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

func TestOnlinePayloadKeepsDatabaseCIDSeparateFromCharacterSlot(t *testing.T) {
	service := SessionService{Env: &directLogoutEnv{}}
	got := service.onlinePayload(robotcap.Info{
		UID: 17000001,
		CID: 900001,
	}, robotconfig.RuntimeConfig{})

	if got.CID != 900001 {
		t.Fatalf("online cid = %d, want database charac_no 900001", got.CID)
	}
	if got.CharacterSlot != 0 {
		t.Fatalf("online character slot = %d, want first slot 0", got.CharacterSlot)
	}
}

func TestOnlinePayloadCarriesPersistentGuildMembership(t *testing.T) {
	service := SessionService{Env: &directLogoutEnv{}}
	got := service.onlinePayload(robotcap.Info{UID: 17000001, CID: 900001, GuildID: 2}, robotconfig.RuntimeConfig{})
	if got.GuildID != 2 {
		t.Fatalf("online guild id = %d, want 2", got.GuildID)
	}
}

func TestOnlinePayloadUsesConfiguredGamePortInsteadOfStoredRobotPort(t *testing.T) {
	service := SessionService{Env: &directLogoutEnv{}}
	got := service.onlinePayload(robotcap.Info{
		UID:  17000001,
		CID:  900001,
		Port: 10011,
	}, robotconfig.RuntimeConfig{})

	if got.Port != 20011 {
		t.Fatalf("online port = %d, want configured game port 20011", got.Port)
	}
}
