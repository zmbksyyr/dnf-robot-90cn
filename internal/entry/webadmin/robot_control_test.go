package webadmin

import (
	"os"
	"path/filepath"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"strings"
	"testing"
)

func TestBuildRobotRestartScriptRestartsSingleRobotProcess(t *testing.T) {
	script := buildRobotRestartScript("/root/robot", "/root/config")
	for _, want := range []string{
		shellQuote(filepath.Join("/root/config", "logs", "stdout.log")),
		`nohup "$exe" >>`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("restart script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "bounded-log-sink") || strings.Contains(script, "web-admin") {
		t.Fatalf("single-process restart script retains child modes: %s", script)
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
		"PartyRoute0 = 5063",
		"",
		"[Robot]",
		"RobotConnectIp = 127.0.0.1",
		"RobotInnerIp = 10.0.0.1",
		"",
		"[Web]",
		"WebPassword = twadmin",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	s := New(&config.SysConfig{ConfigDir: dir, RobotConnectIP: "127.0.0.1", RobotGamePort: 10011}, "", "")

	cfg, err := s.writeExternalPort(20011)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotGamePort != 20011 {
		t.Fatalf("ports were not updated: cfg=%+v", cfg)
	}
	if s.cfg.RobotGamePort != 10011 {
		t.Fatalf("running server ports changed before restart: cfg=%+v", s.cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Game = 20011"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config file missing %q:\n%s", want, data)
		}
	}
	for _, want := range []string{"RobotConnectIp = 127.0.0.1", "RobotInnerIp = 10.0.0.1"} {
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
		ServerDirectory: "/srv/s4a21/DfoServer",
		LogMaxSizeMB:    100, MaxResponseBytes: 4 * 1024 * 1024,
	}
	disk := *running
	disk.ServerDirectory = "/srv/s4a21-next/DfoServer"
	disk.LogMaxSizeMB = 200
	disk.MaxResponseBytes = 8 * 1024 * 1024

	got := restartConfigDiff(running, &disk)
	for _, want := range []string{"server_directory", "log_max_size_mb", "max_response_bytes"} {
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
