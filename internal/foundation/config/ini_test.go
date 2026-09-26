package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestINIValueKeepsCommentCharacters(t *testing.T) {
	cfg, err := LoadFromString("[db]\npassword = uu5!^%jg#semi;tail\n# ignored = yes\n")
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.GetString("db", "password", "")
	want := "uu5!^%jg#semi;tail"
	if got != want {
		t.Fatalf("password mismatch: got %q want %q", got, want)
	}
}

func TestLoadConfigConcurrentDefaultGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", "config.ini")
	const readers = 24
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg, err := LoadConfig(path)
			if err == nil && (cfg.RobotPort != 8111 || cfg.WebPort != 8112) {
				err = fmt.Errorf("unexpected defaults: robot=%d web=%d", cfg.RobotPort, cfg.WebPort)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadConfigRejectsEmptyPath(t *testing.T) {
	if _, err := LoadConfig(" "); err == nil {
		t.Fatal("empty config path unexpectedly accepted")
	}
}

func TestINIRejectsMalformedLines(t *testing.T) {
	for _, content := range []string{
		"broken line",
		"key = value",
		"[broken\nkey = value",
		"[]\nkey = value",
		"[valid] trailing\nkey = value",
	} {
		if _, err := LoadFromString(content); err == nil {
			t.Fatalf("malformed INI unexpectedly loaded: %q", content)
		}
	}
}

func TestINIRejectsDuplicateEntries(t *testing.T) {
	if _, err := LoadFromString("[auto]\nenabled = true\nenabled = false\n"); err == nil {
		t.Fatal("duplicate INI entry unexpectedly loaded")
	}
}

func TestINIRejectsDuplicateSections(t *testing.T) {
	if _, err := LoadFromString("[auto]\nenabled = true\n[auto]\ntarget = 1\n"); err == nil {
		t.Fatal("duplicate INI section unexpectedly loaded")
	}
}

func TestDecoderRejectsNonCanonicalValuesAndUnknownEmptySection(t *testing.T) {
	for _, text := range []string{
		"[auto]\nenabled = TRUE\n",
		"[auto]\nenabled = 1\n",
		"[create]\njobs = 1 2 3\n",
		"[create]\njobs = 1,2,\n",
		"[create]\njobs = 1,2,1\n",
		"[create]\njobs = \n",
		"[legacy]\n",
	} {
		ini, err := LoadFromString(text)
		if err != nil {
			continue
		}
		dec := NewDecoder(ini, "test config")
		dec.Bool("auto", "enabled", false)
		dec.IntList("create", "jobs", []int{0})
		if err := dec.Validate(); err == nil {
			t.Fatalf("non-canonical INI unexpectedly decoded: %q", text)
		}
	}
}

func TestDecodeJSONRejectsUnknownAndTrailingValues(t *testing.T) {
	type payload struct {
		Enabled bool `json:"enabled"`
		Nested  *struct {
			Value int `json:"value"`
		} `json:"nested"`
	}
	for _, raw := range []string{
		`{"enabled":true,"legacy":1}`,
		`{"enabled":true} {"enabled":false}`,
		`{"enabled":true,"enabled":false}`,
		`{"enabled":true,"nested":{"value":1,"value":2}}`,
		`{"Enabled":true}`,
	} {
		var out payload
		if err := DecodeJSON(bytes.NewBufferString(raw), &out); err == nil {
			t.Fatalf("invalid JSON unexpectedly decoded: %s", raw)
		}
	}
	var out payload
	if err := DecodeJSON(bytes.NewBufferString(`{"enabled":true}`), &out); err != nil || !out.Enabled {
		t.Fatalf("canonical JSON decoded as %+v with error %v", out, err)
	}
	out = payload{}
	if err := DecodeJSONBytes([]byte(`{"enabled":true}`), &out); err != nil || !out.Enabled {
		t.Fatalf("buffered canonical JSON decoded as %+v with error %v", out, err)
	}
}

func TestDecodeJSONLimitRejectsOversizedInput(t *testing.T) {
	var out map[string]interface{}
	if err := DecodeJSONLimit(bytes.NewBufferString("{} "), 2, &out); err == nil {
		t.Fatal("oversized JSON unexpectedly decoded")
	}
	if err := DecodeJSONLimit(bytes.NewBufferString("{}"), 0, &out); err == nil {
		t.Fatal("non-positive JSON size limit unexpectedly accepted")
	}
}

func TestLoadConfigReadsPortsSection(t *testing.T) {
	path := t.TempDir() + "/config.ini"
	text := `[Ports]
RobotAPI = 18111
Web = 18112
Game = 20011
PartyRoute0 = 5064

[Robot]
ServerDirectory = /srv/s4a21/DfoServer
RobotInnerIp = 10.0.0.1
RobotConnectIp = 127.0.0.1

[Web]
WebPassword = twadmin
`
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotPort != 18111 || cfg.WebPort != 18112 || cfg.RobotGamePort != 20011 || cfg.PartyRoute0Port != 5064 {
		t.Fatalf("ports not loaded: %+v", cfg)
	}
	if cfg.ServerDirectory != "/srv/s4a21/DfoServer" {
		t.Fatalf("server directory = %q", cfg.ServerDirectory)
	}
	if cfg.RobotConnectIPSetting != "127.0.0.1" || cfg.RobotConnectIP != "127.0.0.1" {
		t.Fatalf("connect setting=%q resolved=%q", cfg.RobotConnectIPSetting, cfg.RobotConnectIP)
	}
}

func TestLoadConfigResolvesAutoConnectIP(t *testing.T) {
	text := strings.Replace("[Ports]\nRobotAPI = 8111\nWeb = 8112\nGame = 10011\nPartyRoute0 = 5063\n\n[Robot]\nServerDirectory = /srv/s4a21/DfoServer\nRobotInnerIp = 10.0.0.1\nRobotConnectIp = 127.0.0.1\n\n[Web]\nWebPassword = twadmin\n", "RobotConnectIp = 127.0.0.1", "RobotConnectIp = auto", 1)
	cfg, err := ParseConfig(text)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RobotConnectIPSetting != "auto" || strings.TrimSpace(cfg.RobotConnectIP) == "" {
		t.Fatalf("connect setting=%q resolved=%q", cfg.RobotConnectIPSetting, cfg.RobotConnectIP)
	}
}

func TestLoadConfigRejectsInvalidOrUnknownSettings(t *testing.T) {
	for _, text := range []string{
		"[Ports]\nWeb = invalid\n",
		"[Ports]\nWeb = 0\n",
		"[Ports]\nGame = 65000\n",
		"[Ports]\nWebPort = 8112\n",
		"[Robot]\nConfigDir = ./config\n",
		"[Robot]\nRobotInnerIp = \n",
		"[Robot]\nRemovedSetting = value\n",
		"[Web]\nWebPassword = \n",
		"[system]\nlog_max_backups = 0\n",
		"[legacy]\n",
	} {
		path := filepath.Join(t.TempDir(), "config.ini")
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("invalid main config unexpectedly loaded: %q", text)
		}
	}
}
