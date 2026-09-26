//go:build linux

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
)

// WithMemoryFile opens another process's memory. Older kernels require the
// caller to ptrace-attach even for root, while newer layouts allow direct
// access. The callback reports whether ptrace already stopped the process.
type MemoryFile interface {
	io.ReaderAt
	io.WriterAt
}

type processMemoryFile struct {
	file   *os.File
	pid    int
	traced bool
}

func (m *processMemoryFile) ReadAt(p []byte, off int64) (int, error) {
	return m.file.ReadAt(p, off)
}

func (m *processMemoryFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := m.file.WriteAt(p, off)
	if err == nil || !m.traced || n != 0 || !processMemoryWriteNeedsPtrace(err) {
		return n, err
	}
	n, err = syscall.PtracePokeData(m.pid, uintptr(off), p)
	if err != nil {
		return n, fmt.Errorf("ptrace write pid %d at 0x%x: %w", m.pid, off, err)
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func processMemoryWriteNeedsPtrace(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EIO)
}

func WithMemoryFile(pid int, writable bool, probeAddress int64, fn func(MemoryFile, bool) error) error {
	if pid <= 0 {
		return fmt.Errorf("invalid process pid %d", pid)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	mem, err := openMemoryFile(pid, writable)
	if err != nil {
		return err
	}
	probeErr := probeMemoryFile(mem, probeAddress)
	if probeErr == nil {
		defer mem.Close()
		return fn(&processMemoryFile{file: mem, pid: pid}, false)
	}
	_ = mem.Close()
	if !errors.Is(probeErr, syscall.EPERM) && !errors.Is(probeErr, syscall.EACCES) {
		return probeErr
	}

	if err := syscall.PtraceAttach(pid); err != nil {
		return fmt.Errorf("ptrace attach pid %d: %w", pid, err)
	}
	attached := true
	defer func() {
		if attached {
			_ = syscall.PtraceDetach(pid)
		}
	}()
	var status syscall.WaitStatus
	for {
		_, err = syscall.Wait4(pid, &status, 0, nil)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("wait for ptrace pid %d: %w", pid, err)
	}
	if !status.Stopped() {
		return fmt.Errorf("ptrace pid %d did not stop", pid)
	}

	mem, err = openMemoryFile(pid, writable)
	if err != nil {
		return err
	}
	defer mem.Close()
	if err := probeMemoryFile(mem, probeAddress); err != nil {
		return err
	}
	callbackErr := fn(&processMemoryFile{file: mem, pid: pid, traced: true}, true)
	detachErr := syscall.PtraceDetach(pid)
	attached = false
	if callbackErr != nil {
		return callbackErr
	}
	if detachErr != nil {
		return fmt.Errorf("ptrace detach pid %d: %w", pid, detachErr)
	}
	return nil
}

func openMemoryFile(pid int, writable bool) (*os.File, error) {
	path := fmt.Sprintf("/proc/%d/mem", pid)
	if writable {
		return os.OpenFile(path, os.O_RDWR, 0)
	}
	return os.Open(path)
}

func probeMemoryFile(mem *os.File, address int64) error {
	var probe [1]byte
	_, err := mem.ReadAt(probe[:], address)
	return err
}
