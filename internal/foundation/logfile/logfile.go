package logfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Prepare bounds the active log and numeric backups left by older versions.
func Prepare(path string, maxBytes int64, backups int) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty log path")
	}
	if maxBytes <= 0 {
		return errors.New("log max bytes must be positive")
	}
	if backups < 0 {
		backups = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := trimToTail(path, maxBytes); err != nil {
		return err
	}
	return prepareBackups(path, maxBytes, backups)
}

func prepareBackups(path string, maxBytes int64, backups int) error {
	for _, tmp := range []string{path + ".trim.tmp", path + ".rotate.tmp"} {
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		return err
	}
	for _, match := range matches {
		suffix := strings.TrimPrefix(match, path+".")
		index, parseErr := strconv.Atoi(suffix)
		if parseErr != nil || index <= 0 {
			continue
		}
		if index > backups {
			if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := trimToTail(match, maxBytes); err != nil {
			return err
		}
	}
	return nil
}

// Rotate moves the active file to .1 and shifts the configured numeric backups.
func Rotate(path string, backups int) error {
	if backups <= 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.Remove(fmt.Sprintf("%s.%d", path, backups)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for index := backups - 1; index >= 1; index-- {
		src := fmt.Sprintf("%s.%d", path, index)
		dst := fmt.Sprintf("%s.%d", path, index+1)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	if err := os.Remove(path + ".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Append writes one record and rotates before it would exceed maxBytes.
func Append(path string, data []byte, maxBytes int64, backups int) error {
	if err := Prepare(path, maxBytes, backups); err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("log record exceeds max bytes: %d > %d", len(data), maxBytes)
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.Size()+int64(len(data)) > maxBytes:
		if err := Rotate(path, backups); err != nil {
			return err
		}
	case err != nil && !os.IsNotExist(err):
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// TruncateToTail keeps the newest maxBytes of path using in-place truncation.
// Unlike trimToTail it never renames the file, so it also works on Windows
// when another process holds the log open (rename is blocked there, but
// truncation is not).
func TruncateToTail(path string, maxBytes int64) error {
	if maxBytes <= 0 {
		return errors.New("log max bytes must be positive")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= maxBytes {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	size := info.Size()
	start := size - maxBytes
	buffer := make([]byte, 128*1024)
	var offset int64
	for start+offset < size {
		n, readErr := f.ReadAt(buffer, start+offset)
		if n > 0 {
			if _, writeErr := f.WriteAt(buffer[:n], offset); writeErr != nil {
				return writeErr
			}
			offset += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}
	if err := f.Truncate(offset); err != nil {
		return err
	}
	return f.Sync()
}

func trimToTail(path string, maxBytes int64) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= maxBytes {
		return f.Close()
	}
	if _, err := f.Seek(info.Size()-maxBytes, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	tmp := path + ".trim.tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		_ = f.Close()
		return err
	}
	_, copyErr := io.CopyN(out, f, maxBytes)
	closeOutErr := out.Close()
	closeInErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeOutErr != nil {
		_ = os.Remove(tmp)
		return closeOutErr
	}
	if closeInErr != nil {
		_ = os.Remove(tmp)
		return closeInErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
