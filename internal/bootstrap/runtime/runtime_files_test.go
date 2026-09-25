package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"robot/internal/capability/robotconfig"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestInitRejectsEmptyRuntimeDirectory(t *testing.T) {
	if err := Init(&config.SysConfig{}); err == nil {
		t.Fatal("empty runtime directory unexpectedly accepted")
	}
}

func TestInitConfigOnlyDoesNotRequirePVF(t *testing.T) {
	dir := t.TempDir()
	if err := InitConfigOnly(&config.SysConfig{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.New(dir).NameTemplates()); err != nil {
		t.Fatalf("name templates were not initialized: %v", err)
	}
}

func TestInitConfigForS4A21ReleasesOnlySupportedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := InitConfigForBackend(&config.SysConfig{ConfigDir: dir}, backendInfoForTest()); err != nil {
		t.Fatal(err)
	}
	paths := layout.New(dir)
	for _, path := range []string{paths.RobotConfig(), paths.NameTemplates(), paths.ShoutTemplates()} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("S4A21 runtime file was not released: %s: %v", path, err)
		}
	}
	for _, path := range []string{paths.PrivateKey(), paths.PublicKey(), paths.PartySkills(), paths.MailboxGuard(), paths.PartyCompatibility(), paths.StoreTitles()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("native or unsupported runtime file was released: %s", path)
		}
	}
	rc, err := robotconfig.LoadFile(paths.RobotConfig())
	if err != nil {
		t.Fatal(err)
	}
	if rc.AutoMailNotify || rc.MaxOnlineRobots != 10000 || rc.MaxPetArtifactSlots != 3 {
		t.Fatalf("S4A21 runtime config was not selected: %+v", rc)
	}
}

func TestReleaseDefaultsContainsOnlySharedS4A21Assets(t *testing.T) {
	paths := layout.New(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := releaseDefaults(paths); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"robot_name_templates.json", "robot_shout_templates.json"} {
		if _, err := os.Stat(filepath.Join(paths.Templates, name)); err != nil {
			t.Fatalf("shared runtime asset %s was not released: %v", name, err)
		}
	}
	if _, err := defaultFiles.ReadFile("defaults/robot_config_s4a21.ini"); err != nil {
		t.Fatalf("S4A21 config asset is missing: %v", err)
	}
}

func backendInfoForTest() (info shared.BackendInfo) {
	return shared.BackendInfo{ID: shared.DefaultBackendID()}
}
