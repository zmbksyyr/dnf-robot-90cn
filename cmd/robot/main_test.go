package main

import (
	"os"
	"path/filepath"
	"testing"

	"robot/internal/entry/tcpapi"
	"robot/internal/shared"
)

func TestLoadRequiredRobotConfigRejectsMissingOrInvalidFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "robot_config.ini")
	if _, err := loadRequiredRobotConfig(missing); err == nil {
		t.Fatal("missing robot config unexpectedly accepted")
	}

	invalid := filepath.Join(t.TempDir(), "robot_config.ini")
	if err := os.WriteFile(invalid, []byte("[auto]\nauto_actions = enabled\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRequiredRobotConfig(invalid); err == nil {
		t.Fatal("invalid robot config unexpectedly accepted")
	}
}

func TestLoadBackendSelectionDefaultsAndRejectsInvalid(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "backend_selection.json")
	selection, err := loadBackendSelection(missing)
	if err != nil || selection.BackendID != shared.BackendNative {
		t.Fatalf("missing selection = %+v, %v", selection, err)
	}
	invalid := filepath.Join(t.TempDir(), "backend_selection.json")
	if err := os.WriteFile(invalid, []byte(`{"backend_id":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBackendSelection(invalid); err == nil {
		t.Fatal("empty backend selection unexpectedly accepted")
	}
}

func TestLoadRequiredRobotConfigAcceptsValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_config.ini")
	if err := os.WriteFile(path, []byte("[auto]\nauto_actions = false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rc, err := loadRequiredRobotConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if rc.AutoActions {
		t.Fatal("auto_actions setting was not loaded")
	}
}

func TestRequiresValidKeypair(t *testing.T) {
	blocked := []string{
		"createRobots",
		"robotsOnline",
		"robotsOnlineAsync",
		"robotsMove",
		"robotsShout",
		"robotsShoutWorld",
		"robotsShoutLocal",
		"robotsStore",
		"robotsStoreAsync",
		"robotsLogout",
		"robotsLogoutAsync",
		"autoStart",
	}
	for _, cmd := range blocked {
		if !tcpapi.RequiresValidKeypair(cmd) {
			t.Fatalf("expected %s to require a valid keypair", cmd)
		}
	}

	allowed := []string{
		"05",
		"sys",
		"robotsStatus",
		"autoStatus",
		"schedulerStatus",
		"systemStatus",
		"systemAnnouncement",
		"goroutineDump",
		"keypairStatus",
		"keypairReleaseDefault",
		"autoStop",
		"robotConfigGet",
		"robotConfigUpdate",
		"cleanupRobots",
		"cleanupRobotsAsync",
		"dangerousDeleteUnlock",
		"dangerousDeleteAsync",
	}
	for _, cmd := range allowed {
		if tcpapi.RequiresValidKeypair(cmd) {
			t.Fatalf("expected %s to be allowed without a valid keypair", cmd)
		}
	}
}
