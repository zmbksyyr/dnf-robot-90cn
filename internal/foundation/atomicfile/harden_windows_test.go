//go:build windows

package atomicfile

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHardenPrivateRestrictsACLOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := WriteFile(path, []byte("WebPassword = secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := HardenPrivate(path); err != nil {
		t.Fatalf("harden private file: %v", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("hardened file has no DACL")
	}
	// The hardened DACL grants exactly the current user, SYSTEM and the
	// built-in Administrators group; inherited Users/Everyone entries are gone.
	if dacl.AceCount != 3 {
		t.Fatalf("ACE count = %d, want 3 (user, SYSTEM, Administrators)", dacl.AceCount)
	}
}
