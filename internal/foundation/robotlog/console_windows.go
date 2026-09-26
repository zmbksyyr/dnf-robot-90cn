//go:build windows

package robotlog

import (
	"os"

	"golang.org/x/sys/windows"
)

// colorSupported reports whether stdout is a console with virtual terminal
// processing. The mode is enabled here so Windows 10+ consoles render ANSI
// colors instead of raw escape sequences.
func colorSupported() bool {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
		if err := windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
			return false
		}
	}
	return true
}
