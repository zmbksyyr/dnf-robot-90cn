package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"robot/internal/foundation/serviceinit"
)

func TestInitialConfigUsesDiscoveredExternalPorts(t *testing.T) {
	original := discoverInitialExternalPorts
	discoverInitialExternalPorts = func(string, string) serviceinit.ExternalPorts {
		return serviceinit.ExternalPorts{
			Game:    serviceinit.PortValue{Port: 20011, Source: "/srv/game/cfg/game.cfg"},
			Monitor: serviceinit.PortValue{Port: 31303, Source: "/srv/monitor/cfg/monitor.cfg"},
			Auction: serviceinit.PortValue{Port: 31803, Source: "/srv/auction/cfg/auction.cfg"},
			Point:   serviceinit.PortValue{Port: 31603, Source: "/srv/point/cfg/point.cfg"},
			Relay:   serviceinit.PortValue{Port: 17200, Source: "/srv/relay/cfg/relay.cfg"},
		}
	}
	t.Cleanup(func() { discoverInitialExternalPorts = original })

	path := filepath.Join(t.TempDir(), "conf", "config.ini")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotGamePort != 20011 {
		t.Fatalf("discovered ports not loaded: %+v", cfg)
	}
	if cfg.PartyRoute0Port != 5063 {
		t.Fatalf("party route0 port = %d, want Robot-owned default", cfg.PartyRoute0Port)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# discovered from /srv/game/cfg/game.cfg", "Game = 20011", "PartyRoute0 = 5063", "RobotConnectIp = auto"} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated config missing %q:\n%s", want, text)
		}
	}
}

func TestExistingConfigIsNotOverwrittenByDiscovery(t *testing.T) {
	original := discoverInitialExternalPorts
	discoverInitialExternalPorts = func(string, string) serviceinit.ExternalPorts {
		t.Fatal("discovery must not run for an existing config")
		return serviceinit.ExternalPorts{}
	}
	t.Cleanup(func() { discoverInitialExternalPorts = original })

	path := filepath.Join(t.TempDir(), "config.ini")
	text := strings.Replace(defaultConfigForTest(), "Game = 10011", "Game = 21011", 1)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotGamePort != 21011 {
		t.Fatalf("existing game port = %d", cfg.RobotGamePort)
	}
}

func TestInitialConfigFallsBackPerExternalPort(t *testing.T) {
	original := discoverInitialExternalPorts
	discoverInitialExternalPorts = func(string, string) serviceinit.ExternalPorts {
		return serviceinit.ExternalPorts{
			Game:    serviceinit.PortValue{Port: 65000, Source: "/srv/game/cfg/game.cfg"},
			Monitor: serviceinit.PortValue{Port: 31303, Source: "/srv/monitor/cfg/monitor.cfg"},
		}
	}
	t.Cleanup(func() { discoverInitialExternalPorts = original })

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotGamePort != 10011 {
		t.Fatalf("invalid discovered game port did not fall back: %d", cfg.RobotGamePort)
	}
	if cfg.MonitorPort != 30303 || cfg.AuctionPort != 30803 || cfg.PointPort != 30603 || cfg.RelayPort != 7200 {
		t.Fatalf("unsupported service fields should remain defaults: %+v", cfg)
	}
}

func defaultConfigForTest() string {
	return `[Ports]
RobotAPI = 8111
Web = 8112
Game = 10011
Monitor = 30303
Auction = 30803
Point = 30603
Relay = 7200
PartyRoute0 = 5063
[Robot]
DfGameR = /home/neople/game/df_game_r
RobotInnerIp = 10.0.0.1
RobotConnectIp = 127.0.0.1
GameServerGroup = 3
[Web]
WebPassword = twadmin
[db]
db_host = 127.0.0.1
db_user_name = game
db_password = secret
db_database_name = d_taiwan
db_port = 3306
db_init_size = 4
db_max_size = 64
db_dial_timeout_sec = 5
db_read_timeout_sec = 30
db_write_timeout_sec = 30
db_conn_max_lifetime_sec = 1800
[system]
log_max_size_mb = 100
log_max_backups = 5
max_response_bytes = 4194304
`
}
