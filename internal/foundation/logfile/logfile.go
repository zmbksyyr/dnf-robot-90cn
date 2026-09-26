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

// Appender keeps one file descriptor open for a bursty producer. The caller
// serializes Append and Close; unlike Append, it prepares and scans backups
// only once when opened.
type Appender struct {
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func OpenAppender(path string, maxBytes int64, backups int) (*Appender, error) {
	if err := Prepare(path, maxBytes, backups); err != nil {
		return nil, err
	}
	a := &Appender{path: path, maxBytes: maxBytes, backups: backups}
	if err := a.open(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Appender) Append(data []byte) error {
	if a == nil || a.file == nil {
		return errors.New("log appender is closed")
	}
	if int64(len(data)) > a.maxBytes {
		return fmt.Errorf("log record exceeds max bytes: %d > %d", len(data), a.maxBytes)
	}
	if a.size+int64(len(data)) > a.maxBytes {
		if err := a.rotate(); err != nil {
			return err
		}
	}
	n, err := a.file.Write(data)
	a.size += int64(n)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (a *Appender) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}

func (a *Appender) open() error {
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	a.file = f
	a.size = info.Size()
	return nil
}

func (a *Appender) rotate() error {
	if err := a.Close(); err != nil {
		return err
	}
	if err := Rotate(a.path, a.backups); err != nil {
		return err
	}
	return a.open()
}

// CopyRotating drains src for its full lifetime while keeping only bounded files.
// Keeping the reader open after rotation is important for child processes whose
// stdout pipe must not close when the log reaches its size limit.
func CopyRotating(path string, src io.Reader, maxBytes int64, backups int) error {
	if err := Prepare(path, maxBytes, backups); err != nil {
		return err
	}
	w, err := newRotatingWriter(path, maxBytes, backups)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(w, src)
	closeErr := w.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// ContainsAnyTail searches a bounded tail without loading the whole file.
func ContainsAnyTail(path string, maxBytes int64, needles ...string) (bool, error) {
	if maxBytes <= 0 || len(needles) == 0 {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return false, err
	}
	lowerNeedles := make([]string, 0, len(needles))
	maxNeedle := 0
	for _, needle := range needles {
		needle = strings.ToLower(needle)
		if needle == "" {
			continue
		}
		lowerNeedles = append(lowerNeedles, needle)
		if len(needle) > maxNeedle {
			maxNeedle = len(needle)
		}
	}
	if len(lowerNeedles) == 0 {
		return false, nil
	}
	reader := io.LimitReader(f, maxBytes)
	buffer := make([]byte, 64*1024)
	overlap := ""
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			chunk := strings.ToLower(overlap + string(buffer[:n]))
			for _, needle := range lowerNeedles {
				if strings.Contains(chunk, needle) {
					return true, nil
				}
			}
			keep := maxNeedle - 1
			if keep > len(chunk) {
				keep = len(chunk)
			}
			if keep > 0 {
				overlap = chunk[len(chunk)-keep:]
			} else {
				overlap = ""
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return false, nil
			}
			return false, readErr
		}
	}
}

type rotatingWriter struct {
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func newRotatingWriter(path string, maxBytes int64, backups int) (*rotatingWriter, error) {
	w := &rotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		if w.size >= w.maxBytes {
			if err := w.rotate(); err != nil {
				return written, err
			}
		}
		remaining := w.maxBytes - w.size
		chunkSize := int64(len(data))
		if chunkSize > remaining {
			chunkSize = remaining
		}
		n, err := w.file.Write(data[:int(chunkSize)])
		written += n
		w.size += int64(n)
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (w *rotatingWriter) Close() error {
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *rotatingWriter) rotate() error {
	if err := w.Close(); err != nil {
		return err
	}
	if err := Rotate(w.path, w.backups); err != nil {
		return err
	}
	return w.open()
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
