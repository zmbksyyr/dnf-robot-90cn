//go:build windows

package webadmin

import (
	"path/filepath"
	"testing"
)

func TestWindowsRestartLogStaysOutsideConfigDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	got := windowsRestartLogPath(root)
	want := root + ".restart.log"
	if got != want {
		t.Fatalf("restart log path = %q, want %q", got, want)
	}
	if filepath.Dir(got) == filepath.Clean(root) {
		t.Fatalf("restart log must not lock the config directory: %q", got)
	}
}
