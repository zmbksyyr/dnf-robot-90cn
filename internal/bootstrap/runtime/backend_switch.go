package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

const backendBackupPrefix = "config.backend-bak."

// PrepareBackendRuntime applies a persisted backend selection on the next
// startup. It intentionally preserves the system config.ini, while resetting
// robot-owned runtime files and state after a backend generation changes.
func PrepareBackendRuntime(paths layout.Paths, selection shared.BackendSelection) (bool, error) {
	if !paths.Valid() {
		return false, fmt.Errorf("invalid runtime layout")
	}
	if err := paths.Ensure(); err != nil {
		return false, err
	}
	previous, err := readBackendRuntime(paths.BackendRuntime())
	needsReset := true
	if errors.Is(err, fs.ErrNotExist) {
		// Generation zero is the untouched/default environment. A persisted
		// non-zero selection means the user explicitly requested a switch even
		// if this installation predates the applied-generation marker.
		if selection.ConfigGeneration == 0 {
			return false, nil
		}
	} else if err != nil {
		return false, err
	} else if previous.BackendID == selection.BackendID && previous.ConfigGeneration == selection.ConfigGeneration {
		needsReset = false
	}
	if !needsReset {
		return false, nil
	}
	backup, moved, err := backupBackendRuntime(paths)
	if err != nil {
		return false, err
	}
	if err := paths.Ensure(); err != nil {
		_ = restoreBackendRuntime(backup, moved)
		return false, err
	}
	if err := writeBackendSelection(paths.BackendSelection(), selection); err != nil {
		_ = restoreBackendRuntime(backup, moved)
		return false, err
	}
	_ = pruneBackendBackups(filepath.Dir(paths.Root), 3)
	return true, nil
}

// MarkBackendRuntimeApplied records the selection only after backend-specific
// runtime initialization has succeeded. A failed startup therefore retries the
// reinitialization on the next run.
func MarkBackendRuntimeApplied(paths layout.Paths, selection shared.BackendSelection) error {
	if !paths.Valid() {
		return fmt.Errorf("invalid runtime layout")
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	return writeBackendSelection(paths.BackendRuntime(), selection)
}

func readBackendRuntime(path string) (shared.BackendSelection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return shared.BackendSelection{}, err
	}
	var selection shared.BackendSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return shared.BackendSelection{}, fmt.Errorf("decode backend runtime state: %w", err)
	}
	if selection.BackendID == "" {
		return shared.BackendSelection{}, fmt.Errorf("backend runtime state has empty backend_id")
	}
	return selection, nil
}

func writeBackendSelection(path string, selection shared.BackendSelection) error {
	data, err := json.MarshalIndent(selection, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(data, '\n'), 0600)
}

type backendRuntimeMove struct {
	source string
	target string
}

func backupBackendRuntime(paths layout.Paths) (string, []backendRuntimeMove, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backup := filepath.Join(filepath.Dir(paths.Root), backendBackupPrefix+stamp)
	if err := os.MkdirAll(backup, 0755); err != nil {
		return "", nil, err
	}
	moved := make([]backendRuntimeMove, 0)
	move := func(source, target string) error {
		if _, err := os.Stat(source); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.Rename(source, target); err != nil {
			return err
		}
		moved = append(moved, backendRuntimeMove{source: source, target: target})
		return nil
	}
	for _, name := range []string{"templates", "keys", "pvf", "state", "logs", "tmp"} {
		if err := move(filepath.Join(paths.Root, name), filepath.Join(backup, name)); err != nil {
			_ = restoreBackendRuntime(backup, moved)
			return "", nil, err
		}
	}
	for _, name := range []string{"robot_config.ini", "market_config.ini", "market_item_price_ranges.json", "compat.json", "party_compat.json"} {
		source := filepath.Join(paths.Conf, name)
		target := filepath.Join(backup, "conf", name)
		if err := move(source, target); err != nil {
			_ = restoreBackendRuntime(backup, moved)
			return "", nil, err
		}
	}
	return backup, moved, nil
}

func restoreBackendRuntime(backup string, moved []backendRuntimeMove) error {
	var firstErr error
	for index := len(moved) - 1; index >= 0; index-- {
		item := moved[index]
		if err := os.RemoveAll(item.source); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
		if err := os.MkdirAll(filepath.Dir(item.source), 0755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.Rename(item.target, item.source); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	if err := os.RemoveAll(backup); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func pruneBackendBackups(parent string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	type backupEntry struct {
		name string
		when time.Time
	}
	backups := make([]backupEntry, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), backendBackupPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		backups = append(backups, backupEntry{name: entry.Name(), when: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].when.After(backups[j].when) })
	if len(backups) <= keep {
		return nil
	}
	for _, entry := range backups[keep:] {
		if err := os.RemoveAll(filepath.Join(parent, entry.name)); err != nil {
			return err
		}
	}
	return nil
}
