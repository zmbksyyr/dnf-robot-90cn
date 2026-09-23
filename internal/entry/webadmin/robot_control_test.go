package webadmin

import (
	"os"
	"path/filepath"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"strings"
	"testing"
)

func TestBuildRobotRestartScriptKeepsOtherBoundedLogSinks(t *testing.T) {
	script := buildRobotRestartScript("/root/robot", "/root/config")
	for _, want := range []string{
		`[ "$mode" = "--web-admin" ]`,
		`[ "$mode" = "--bounded-log-sink" ] && [ "$sink" = "$log_path" ]`,
		"log_path=" + shellQuote(filepath.Join("/root/config", "logs", "stdout.log")),
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("restart script missing %q:\n%s", want, script)
		}
	}
}

func TestRestartHelperRequestedRequiresFirstExactArgument(t *testing.T) {
	if !RestartHelperRequested([]string{"--restart-helper", "--restart-parent-pid", "1"}) {
		t.Fatal("restart helper argument was not recognized")
	}
	for _, args := range [][]string{
		nil,
		{"--web-admin", "--restart-helper"},
		{"--restart-helper=true"},
	} {
		if RestartHelperRequested(args) {
			t.Fatalf("unexpected restart helper match for %v", args)
		}
	}
}

func TestConfigPathRejectsMissingRuntimeRoot(t *testing.T) {
	if got := (&Server{}).configPath(); got != "" {
		t.Fatalf("config path = %q, want empty path without configured runtime root", got)
	}
}

func TestWriteGamePortUpdatesMainConfig(t *testing.T) {
	dir := t.TempDir()
	paths := layout.New(dir)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	path := paths.MainConfig()
	text := strings.Join([]string{
		"[Ports]",
		"RobotAPI = 8111",
		"Web = 8112",
		"Game = 10011",
		"Monitor = 30303",
		"Auction = 30803",
		"Point = 30603",
		"Relay = 7200",
		"PartyRoute0 = 5063",
		"",
		"[Robot]",
		"RobotConnectIp = 127.0.0.1",
		"RobotInnerIp = 10.0.0.1",
		"",
		"[Services]",
		"AuctionHost = 192.168.1.10",
		"PointHost = 192.168.1.11",
		"RelayHost = 192.168.1.12",
		"Root = /home/neople",
		"RunScript = /root/run",
		"",
		"[Web]",
		"WebPassword = twadmin",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	s := New(&config.SysConfig{ConfigDir: dir, RobotConnectIP: "127.0.0.1", RobotGamePort: 10011, MonitorPort: 30303, AuctionPort: 30803, PointPort: 30603, RelayPort: 7200}, "", "")

	cfg, err := s.writeExternalPorts(20011, 31303, 31803, 31603, 17200)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotGamePort != 20011 || cfg.MonitorPort != 31303 || cfg.AuctionPort != 31803 || cfg.PointPort != 31603 || cfg.RelayPort != 17200 {
		t.Fatalf("ports were not updated: cfg=%+v", cfg)
	}
	if s.cfg.RobotGamePort != 10011 || s.cfg.MonitorPort != 30303 || s.cfg.AuctionPort != 30803 || s.cfg.PointPort != 30603 || s.cfg.RelayPort != 7200 {
		t.Fatalf("running server ports changed before restart: cfg=%+v", s.cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Game = 20011", "Monitor = 31303", "Auction = 31803", "Point = 31603", "Relay = 17200"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config file missing %q:\n%s", want, data)
		}
	}
	for _, want := range []string{"RobotConnectIp = 127.0.0.1", "RobotInnerIp = 10.0.0.1", "AuctionHost = 192.168.1.10", "PointHost = 192.168.1.11", "RelayHost = 192.168.1.12", "Root = /home/neople", "RunScript = /root/run"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("non-port config changed while saving ports, missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "RobotGamePort") {
		t.Fatalf("config file was not updated:\n%s", data)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellQuote("/root/robot's/bin")
	want := "'/root/robot'\"'\"'s/bin'"
	if got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}

func TestRestartConfigDiffIncludesStartupOnlyFields(t *testing.T) {
	running := &config.SysConfig{
		DFGameR: "/home/neople/game/df_game_r", GameServerGroup: 3,
		DBMaxSize: 64, DBDialTimeoutSec: 5, LogMaxSizeMB: 100, MaxResponseBytes: 4 * 1024 * 1024,
	}
	disk := *running
	disk.DFGameR = "/srv/game/df_game_r"
	disk.GameServerGroup = 4
	disk.DBMaxSize = 128
	disk.DBDialTimeoutSec = 8
	disk.LogMaxSizeMB = 200
	disk.MaxResponseBytes = 8 * 1024 * 1024

	got := restartConfigDiff(running, &disk)
	for _, want := range []string{"df_game_r", "game_server_group", "database_max_size", "database_dial_timeout_sec", "log_max_size_mb", "max_response_bytes"} {
		if !containsString(got, want) {
			t.Fatalf("restart diff missing %q: %v", want, got)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
