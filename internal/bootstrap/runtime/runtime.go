package runtime

import (
	"crypto/md5"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"robot/internal/capability/catalog"
	"robot/internal/capability/pvf"
	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

//go:embed defaults/*
var defaultFiles embed.FS

func Init(cfg *config.SysConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if cfg.ConfigDir == "" {
		return fmt.Errorf("empty runtime config directory")
	}
	paths := layout.New(cfg.ConfigDir)
	if err := paths.Ensure(); err != nil {
		return err
	}
	if err := ensureConfigRuntimeFiles(paths); err != nil {
		return err
	}
	if err := pvf.EnsureExports(cfg.DFGameR, paths.PVF, paths.Temp); err != nil {
		return err
	}
	if changed, err := catalog.SyncPartySkillCatalog(paths.PartySkills(), paths.PVFSkillStates()); err != nil {
		foundationlog.Robotf("RUNTIME_PARTY_SKILL_SWITCH_SYNC_FAILED path=%s err=%v\n", paths.PartySkills(), err)
	} else if changed {
		foundationlog.Robotf("RUNTIME_PARTY_SKILL_SWITCH_SYNCED path=%s source=pvf\n", paths.PartySkills())
	}
	if err := catalog.LoadPartySkills(paths.Templates); err != nil {
		foundationlog.Robotf("RUNTIME_PARTY_SKILL_CATALOG_LOAD_FAILED path=%s err=%v\n", paths.PartySkills(), err)
	}
	if err := updateRuntimeManifest(cfg); err != nil {
		foundationlog.Robotf("RUNTIME_MANIFEST_UPDATE_SKIPPED path=%s err=%v\n", filepath.Join(paths.PVF, "pvf_manifest.json"), err)
	}
	return nil
}

// InitConfigOnly prepares robot-owned configuration and templates without
// parsing or exporting a backend PVF. Simulated backends use their own PVF
// adapter and must not inherit native runtime initialization.
func InitConfigOnly(cfg *config.SysConfig) error {
	return InitConfigForBackend(cfg, shared.BackendInfo{
		Capabilities: shared.CapabilityMatrix(shared.CapabilityStatus{Enabled: true}),
	})
}

// InitConfigForBackend releases only common files and files supported by the
// selected backend. Backend switches use a fresh runtime directory, so an
// unsupported backend-specific file is never presented as active config.
func InitConfigForBackend(cfg *config.SysConfig, backend shared.BackendInfo) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if cfg.ConfigDir == "" {
		return fmt.Errorf("empty runtime config directory")
	}
	paths := layout.New(cfg.ConfigDir)
	if err := paths.Ensure(); err != nil {
		return err
	}
	if err := releaseBackendDefaults(paths, backend); err != nil {
		return err
	}
	normalizeConfigFileModes(paths)
	return nil
}

func releaseBackendDefaults(paths layout.Paths, backend shared.BackendInfo) error {
	names := []string{"robot_name_templates.json", "robot_shout_templates.json"}
	configName := "robot_config.ini"
	if backend.ID == shared.BackendS4A21 {
		configName = "robot_config_s4a21.ini"
	}
	names = append(names, configName)
	if backend.Supports(shared.CapabilitySkill) {
		names = append(names, "party_skill_catalog.json")
	}
	if backend.Supports(shared.CapabilityMailboxGuard) {
		names = append(names, "compat.json")
	}
	if backend.Supports(shared.CapabilityPartyCompatibility) {
		names = append(names, "party_compat.json")
	}
	if backend.Supports(shared.CapabilityStore) {
		names = append(names, "robot_store_titles.json")
	}
	for _, name := range names {
		data, err := defaultFiles.ReadFile("defaults/" + name)
		if err != nil {
			return err
		}
		releaseName := name
		if name == "robot_config_s4a21.ini" {
			releaseName = "robot_config.ini"
		}
		dst, err := defaultReleasePath(paths, releaseName)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0644)
		if _, err := atomicfile.WriteFileIfMissing(dst, data, mode); err != nil {
			return err
		}
	}
	return nil
}

type runtimeManifest struct {
	CheckedAt         string                       `json:"checked_at"`
	DFGameR           runtimeFileStatus            `json:"df_game_r"`
	ConfigFiles       map[string]runtimeFileStatus `json:"config_files"`
	GameFiles         map[string]runtimeFileStatus `json:"game_files"`
	AllRuntimeFilesOK bool                         `json:"all_runtime_files_ok"`
	ExpectedGameDir   string                       `json:"expected_game_dir"`
}

type runtimeFileStatus struct {
	Path         string `json:"path"`
	Exists       bool   `json:"exists"`
	Size         int64  `json:"size,omitempty"`
	ModTime      int64  `json:"mod_time,omitempty"`
	MD5          string `json:"md5,omitempty"`
	SameAsConfig bool   `json:"same_as_config,omitempty"`
}

func ensureConfigRuntimeFiles(paths layout.Paths) error {
	if err := releaseDefaults(paths); err != nil {
		return err
	}
	normalizeConfigFileModes(paths)
	return nil
}

func releaseDefaults(paths layout.Paths) error {
	return fs.WalkDir(defaultFiles, "defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if path == "defaults/robot_config_s4a21.ini" {
			return nil
		}
		data, err := defaultFiles.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path, "defaults/")
		dst, err := defaultReleasePath(paths, name)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0644)
		_, err = atomicfile.WriteFileIfMissing(dst, data, mode)
		return err
	})
}

func defaultReleasePath(paths layout.Paths, name string) (string, error) {
	switch name {
	case "robot_config.ini":
		return paths.RobotConfig(), nil
	case "privatekey.pem":
		return paths.PrivateKey(), nil
	case "publickey.pem":
		return paths.PublicKey(), nil
	case "party_skill_catalog.json":
		return paths.PartySkills(), nil
	case "compat.json":
		return paths.MailboxGuard(), nil
	case "party_compat.json":
		return paths.PartyCompatibility(), nil
	case "robot_name_templates.json":
		return paths.NameTemplates(), nil
	case "robot_shout_templates.json":
		return paths.ShoutTemplates(), nil
	case "robot_store_titles.json":
		return paths.StoreTitles(), nil
	default:
		return "", fmt.Errorf("runtime default %q has no categorized destination", name)
	}
}

func normalizeConfigFileModes(paths layout.Paths) {
	_ = os.Chmod(paths.MainConfig(), 0600)
	_ = os.Chmod(paths.PrivateKey(), 0600)
	_ = os.Chmod(paths.PublicKey(), 0644)
}

func updateRuntimeManifest(cfg *config.SysConfig) error {
	if cfg == nil || cfg.ConfigDir == "" || cfg.DFGameR == "" {
		return nil
	}
	paths := layout.New(cfg.ConfigDir)
	manifestPath := filepath.Join(paths.PVF, "pvf_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var manifest map[string]interface{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &manifest); err != nil {
			return err
		}
	}
	if manifest == nil {
		manifest = map[string]interface{}{}
	}
	runtimeStatus := buildRuntimeManifest(cfg)
	manifest["runtime"] = &runtimeStatus
	return pvf.WriteJSON(manifestPath, manifest)
}

func buildRuntimeManifest(cfg *config.SysConfig) runtimeManifest {
	gameDir := filepath.Dir(cfg.DFGameR)
	paths := layout.New(cfg.ConfigDir)
	configFiles := map[string]runtimeFileStatus{}
	gameFiles := map[string]runtimeFileStatus{}
	for _, name := range []string{"privatekey.pem", "publickey.pem"} {
		cfgPath := filepath.Join(paths.Keys, name)
		gamePath := filepath.Join(gameDir, name)
		cfgStatus := fileStatus(cfgPath)
		gameStatus := fileStatus(gamePath)
		if cfgStatus.Exists && gameStatus.Exists && cfgStatus.MD5 != "" && cfgStatus.MD5 == gameStatus.MD5 {
			gameStatus.SameAsConfig = true
		}
		configFiles[name] = cfgStatus
		gameFiles[name] = gameStatus
	}
	out := runtimeManifest{
		CheckedAt:         time.Now().Format(time.RFC3339),
		DFGameR:           fileStatus(cfg.DFGameR),
		ConfigFiles:       configFiles,
		GameFiles:         gameFiles,
		ExpectedGameDir:   gameDir,
		AllRuntimeFilesOK: true,
	}
	for _, name := range []string{"privatekey.pem", "publickey.pem"} {
		if !configFiles[name].Exists || !gameFiles[name].Exists || !gameFiles[name].SameAsConfig {
			out.AllRuntimeFilesOK = false
		}
	}
	return out
}

func fileStatus(path string) runtimeFileStatus {
	out := runtimeFileStatus{Path: path}
	stat, err := os.Stat(path)
	if err != nil {
		return out
	}
	out.Exists = true
	out.Size = stat.Size()
	out.ModTime = stat.ModTime().Unix()
	if sum, err := fileMD5(path); err == nil {
		out.MD5 = sum
	}
	return out
}

func fileMD5(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
