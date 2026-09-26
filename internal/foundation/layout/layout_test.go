package layout

import (
	"path/filepath"
	"testing"
)

func TestNewBuildsCategorizedRuntimePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	paths := New(root)
	checks := map[string]string{
		paths.MainConfig():      filepath.Join(root, "conf", "config.ini"),
		paths.NameTemplates():   filepath.Join(root, "templates", "robot_name_templates.json"),
		paths.RobotLog():        filepath.Join(root, "logs", "robot.log"),
		paths.StorePointCache(): filepath.Join(root, "state", "store_points_cache.json"),
	}
	for got, want := range checks {
		if got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
	}
}

func TestInvalidRootNeverFallsBackToWorkingDirectory(t *testing.T) {
	for _, root := range []string{"", " \t", ".", "config", filepath.Join("srv", "robot", "config")} {
		paths := New(root)
		if paths != (Paths{}) || paths.Valid() {
			t.Fatalf("invalid root %q produced paths: %+v", root, paths)
		}
		for name, path := range map[string]string{
			"main config":       paths.MainConfig(),
			"robot config":      paths.RobotConfig(),
			"name templates":    paths.NameTemplates(),
			"shout templates":   paths.ShoutTemplates(),
			"store titles":      paths.StoreTitles(),
			"party skills":      paths.PartySkills(),
			"PVF manifest":      paths.PVFManifest(),
			"robot log":         paths.RobotLog(),
			"store point cache": paths.StorePointCache(),
		} {
			if path != "" {
				t.Fatalf("root %q synthesized %s path %q", root, name, path)
			}
		}
		if err := paths.Ensure(); err == nil {
			t.Fatalf("root %q unexpectedly ensured the working directory", root)
		}
	}
}
