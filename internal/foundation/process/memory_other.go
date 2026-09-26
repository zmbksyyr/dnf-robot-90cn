//go:build !linux

package process

import (
	"fmt"
	"io"
)

type MemoryFile interface {
	io.ReaderAt
	io.WriterAt
}

func WithMemoryFile(pid int, writable bool, probeAddress int64, fn func(MemoryFile, bool) error) error {
	return fmt.Errorf("process memory access is only supported on Linux")
}
