package runtime

import (
	"os"
	"testing"

	"robot/internal/capability/robotconfig"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestInitConfigOnlyDoesNotRequirePVF(t *testing.T) {
	dir := t.TempDir()
	if err := InitConfigOnly(&config.SysConfig{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.New(dir).NameTemplates()); err != nil {
		t.Fatalf("name templates were not initialized: %v", err)
	}
}

func TestInitConfigForCN90ReleasesOnlySupportedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := InitConfigForBackend(&config.SysConfig{ConfigDir: dir}, backendInfoForTest()); err != nil {
		t.Fatal(err)
	}
	paths := layout.New(dir)
	for _, path := range []string{paths.RobotConfig(), paths.NameTemplates(), paths.ShoutTemplates()} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("90CN runtime file was not released: %s: %v", path, err)
		}
	}
	for _, path := range []string{paths.PartySkills(), paths.StoreTitles()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unsupported runtime file was released: %s", path)
		}
	}
	rc, err := robotconfig.LoadFile(paths.RobotConfig())
	if err != nil {
		t.Fatal(err)
	}
	if rc.AutoMailNotify || rc.MaxOnlineRobots != 10000 || rc.MaxPetArtifactSlots != 3 {
		t.Fatalf("90CN runtime config was not selected: %+v", rc)
	}
}

func backendInfoForTest() (info shared.BackendInfo) {
	return shared.BackendInfo{ID: shared.BackendID("test")}
}
