package backend

import (
	"testing"

	"robot/internal/shared"
)

func TestSelectNativeOnlyOnLinux(t *testing.T) {
	native, err := Select(shared.BackendNative, "linux")
	if err != nil || !native.Supports(shared.CapabilityProvision) {
		t.Fatalf("native on linux = %+v, %v", native, err)
	}
	if _, err := Select(shared.BackendNative, "windows"); err == nil {
		t.Fatal("native must not start on Windows")
	}
	if _, err := Select("missing", "linux"); err == nil {
		t.Fatal("unknown backend must fail closed")
	}
	for _, platform := range []string{"linux", "windows"} {
		if info, err := Select(shared.BackendS4A21, platform); err != nil || !info.Selectable {
			t.Fatalf("S4A21 on %s = %+v, %v", platform, info, err)
		}
	}
}
