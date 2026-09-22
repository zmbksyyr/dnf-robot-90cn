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
	if _, err := Select(shared.BackendS4A21, "linux"); err == nil {
		t.Fatal("pending S4A21 backend must not be selectable")
	}
}
