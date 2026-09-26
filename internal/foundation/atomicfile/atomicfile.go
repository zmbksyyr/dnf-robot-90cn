// Package atomicfile publishes complete files without exposing partially
// written content to concurrent readers.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data. The temporary file is created
// beside path so the final rename stays on the same filesystem. When path is a
// symbolic link, the link target is replaced so the link keeps pointing at the
// live file (Linux deployments commonly link logs into /var/log).
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	target := resolveSymlinkTarget(path)
	tempPath, err := writeTemp(target, data, perm)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if err := clearReadOnly(target); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("replace %s: %w (target may be read-only or locked by another process)", target, err)
	}
	if perm&0o077 == 0 {
		// Best effort: Windows ACLs carry owner-only intent where the Unix
		// mode bits cannot.
		_ = HardenPrivate(target)
	}
	return nil
}

// WriteFileIfMissing atomically creates path without replacing an existing
// file. It returns true only when this call published the file.
func WriteFileIfMissing(path string, data []byte, perm fs.FileMode) (bool, error) {
	tempPath, err := writeTemp(path, data, perm)
	if err != nil {
		return false, err
	}
	defer os.Remove(tempPath)

	// Linking a complete same-directory temporary file gives create-if-absent
	// semantics without a stat/write race or a partially visible destination.
	if err := os.Link(tempPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		// Some filesystems (for example FAT/exFAT on Windows) do not support
		// hard links; fall back to an exclusive create.
		created, writeErr := writeExclusive(path, data, perm)
		if writeErr == nil && created && perm&0o077 == 0 {
			_ = HardenPrivate(path)
		}
		return created, writeErr
	}
	if perm&0o077 == 0 {
		_ = HardenPrivate(path)
	}
	return true, nil
}

// writeExclusive creates path only when it does not exist yet.
func writeExclusive(path string, data []byte, perm fs.FileMode) (bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	written, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return false, writeErr
	}
	if written != len(data) {
		_ = os.Remove(path)
		return false, io.ErrShortWrite
	}
	if syncErr != nil {
		_ = os.Remove(path)
		return false, syncErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return false, closeErr
	}
	return true, nil
}

// resolveSymlinkTarget returns the final target of a symbolic link so atomic
// replacement keeps the link itself intact.
func resolveSymlinkTarget(path string) string {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return target
}

// clearReadOnly removes the read-only attribute from an existing destination.
// On Windows a read-only file cannot be replaced by rename.
func clearReadOnly(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode().Perm()&0o200 != 0 {
		return nil
	}
	return os.Chmod(path, info.Mode().Perm()|0o200)
}

func writeTemp(path string, data []byte, perm fs.FileMode) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(perm); err != nil {
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	keep = true
	return tempPath, nil
}
