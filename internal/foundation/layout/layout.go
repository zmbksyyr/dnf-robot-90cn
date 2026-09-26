package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths is the single source of truth for every Robot-owned runtime file.
// Root is always the config directory next to the Robot executable.
type Paths struct {
	Root      string
	Conf      string
	Templates string
	PVF       string
	State     string
	Logs      string
	Temp      string
}

func New(root string) Paths {
	root = strings.TrimSpace(root)
	if root == "" || !isAbsoluteRoot(root) {
		return Paths{}
	}
	root = filepath.Clean(root)
	return Paths{
		Root:      root,
		Conf:      filepath.Join(root, "conf"),
		Templates: filepath.Join(root, "templates"),
		PVF:       filepath.Join(root, "pvf"),
		State:     filepath.Join(root, "state"),
		Logs:      filepath.Join(root, "logs"),
		Temp:      filepath.Join(root, "tmp"),
	}
}

// Valid reports whether this layout is anchored to an absolute config root.
func (p Paths) Valid() bool {
	return p.Root != "" && isAbsoluteRoot(p.Root)
}

func FromExecutable(executable string) (Paths, error) {
	if strings.TrimSpace(executable) == "" {
		return Paths{}, fmt.Errorf("empty executable path")
	}
	abs, err := filepath.Abs(executable)
	if err != nil {
		return Paths{}, err
	}
	paths := New(filepath.Join(filepath.Dir(abs), "config"))
	if !paths.Valid() {
		return Paths{}, fmt.Errorf("invalid config layout for executable %s", executable)
	}
	return paths, nil
}

func (p Paths) Ensure() error {
	if !p.Valid() {
		return fmt.Errorf("config root must be absolute")
	}
	for _, dir := range []string{p.Root, p.Conf, p.Templates, p.PVF, p.State, p.Logs, p.Temp} {
		if !isAbsoluteRoot(dir) {
			return fmt.Errorf("categorized config directory must be absolute")
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func (p Paths) MainConfig() string  { return categorizedPath(p.Conf, "config.ini") }
func (p Paths) RobotConfig() string { return categorizedPath(p.Conf, "robot_config.ini") }

func (p Paths) NameTemplates() string {
	return categorizedPath(p.Templates, "robot_name_templates.json")
}
func (p Paths) ShoutTemplates() string {
	return categorizedPath(p.Templates, "robot_shout_templates.json")
}
func (p Paths) StoreTitles() string { return categorizedPath(p.Templates, "robot_store_titles.json") }
func (p Paths) PartySkills() string { return categorizedPath(p.Templates, "party_skill_catalog.json") }

func (p Paths) PVFManifest() string    { return categorizedPath(p.PVF, "pvf_manifest.json") }
func (p Paths) PVFEquipment() string   { return categorizedPath(p.PVF, "equipment_catalog.json") }
func (p Paths) PVFStackable() string   { return categorizedPath(p.PVF, "stackable_catalog.json") }
func (p Paths) PVFMaps() string        { return categorizedPath(p.PVF, "map_catalog.json") }
func (p Paths) PVFSkillStates() string { return categorizedPath(p.PVF, "skill_state_catalog.json") }
func (p Paths) PVFLevelExp() string    { return categorizedPath(p.PVF, "level_exp_catalog.json") }

func (p Paths) RobotLog() string      { return categorizedPath(p.Logs, "robot.log") }
func (p Paths) StdoutLog() string     { return categorizedPath(p.Logs, "stdout.log") }
func (p Paths) StartErrorLog() string { return categorizedPath(p.Logs, "start_error.log") }

func (p Paths) StorePointCache() string  { return categorizedPath(p.State, "store_points_cache.json") }
func (p Paths) StorePointActive() string { return categorizedPath(p.State, "store_points_active.json") }
func (p Paths) BackendSelection() string { return categorizedPath(p.State, "backend_selection.json") }
func (p Paths) BackendRuntime() string   { return categorizedPath(p.State, "backend_runtime.json") }

func categorizedPath(dir, name string) string {
	if strings.TrimSpace(dir) == "" || !isAbsoluteRoot(dir) {
		return ""
	}
	return filepath.Join(dir, name)
}

// isAbsoluteRoot also recognizes a Unix-rooted path while tests are running
// on Windows. Production roots still come from os.Executable and therefore
// use the host-native absolute form.
func isAbsoluteRoot(value string) bool {
	if filepath.IsAbs(value) {
		return true
	}
	return strings.HasPrefix(strings.ReplaceAll(value, `\`, "/"), "/")
}
