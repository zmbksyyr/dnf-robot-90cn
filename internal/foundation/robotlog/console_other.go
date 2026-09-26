//go:build !windows

package robotlog

import (
	"os"
)

// colorSupported reports whether stdout is a character device (terminal).
func colorSupported() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
