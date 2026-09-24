//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
)

func waitForFatalExit() {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "Robot failed to start. Press Enter to close this window.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
