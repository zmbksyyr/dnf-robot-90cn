package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigAcceptsNullOriginsByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	minimal := `[Ports]
RobotAPI = 19111
Web = 19112
Game = 10011
PartyRoute0 = 5063

[Robot]
RobotInnerIp = 10.0.0.1
RobotConnectIp = auto

[Web]
WebPassword = secret
`
	if err := os.WriteFile(path, []byte(minimal), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WebAllowNullOrigin {
		t.Fatal("null origins must be accepted without an explicit configuration key")
	}

	if err := os.WriteFile(path, []byte(minimal+"AllowNullOrigin = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebAllowNullOrigin {
		t.Fatal("AllowNullOrigin = false must reject null origins")
	}
}
