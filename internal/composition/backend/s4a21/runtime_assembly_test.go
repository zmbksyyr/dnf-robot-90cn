package s4a21

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/layout"
)

func TestResolvePVFPathAcceptsExecutableDirectoryAndFile(t *testing.T) {
	dir := t.TempDir()
	pvf := filepath.Join(dir, "Script.pvf")
	if err := os.WriteFile(pvf, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePVFPath(filepath.Join(dir, "DfoServer.exe")); err != nil || got != pvf {
		t.Fatalf("executable resolution = %q, %v", got, err)
	}
	if got, err := ResolvePVFPath(dir); err != nil || got != pvf {
		t.Fatalf("directory resolution = %q, %v", got, err)
	}
	if got, err := ResolvePVFPath(pvf); err != nil || got != pvf {
		t.Fatalf("file resolution = %q, %v", got, err)
	}
	if _, err := ResolvePVFPath(""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
	if _, err := ResolvePVFPath(t.TempDir()); err == nil {
		t.Fatal("directory without Script.pvf was accepted")
	}
}

func TestResolveDatabasePathPrefersExplicitPath(t *testing.T) {
	dbDir := t.TempDir()
	explicit := filepath.Join(dbDir, "custom.db")
	if err := os.WriteFile(explicit, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveDatabasePath("", explicit); err != nil || got != explicit {
		t.Fatalf("explicit database = %q, %v", got, err)
	}

	serverDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(serverDir, "Data"), 0755); err != nil {
		t.Fatal(err)
	}
	derived := filepath.Join(serverDir, "Data", "inventory.db")
	if err := os.WriteFile(derived, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveDatabasePath(serverDir, ""); err != nil || got != derived {
		t.Fatalf("derived database = %q, %v", got, err)
	}
	if got, err := ResolveDatabasePath(filepath.Join(serverDir, "DfoServer.exe"), ""); err != nil || got != derived {
		t.Fatalf("executable database = %q, %v", got, err)
	}
	if _, err := ResolveDatabasePath("", ""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
}

func TestNewRuntimeTransportValidatesAddress(t *testing.T) {
	if _, err := NewRuntimeTransport("", 0); err == nil {
		t.Fatal("empty address was accepted")
	}
	if _, err := NewRuntimeTransport("127.0.0.1", 0); err == nil {
		t.Fatal("missing port was accepted")
	}
	transport, err := NewRuntimeTransport("127.0.0.1", 10011)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.CloseAll(); err != nil {
		t.Fatalf("close transport: %v", err)
	}
}

func TestComposeRuntimeRequiresAdapterInputs(t *testing.T) {
	paths := layout.New(t.TempDir())
	cases := []RuntimeComposeOptions{
		{},
		{AccountPrefix: "robot"},
		{AccountPrefix: "robot", ConnectIP: "127.0.0.1", GamePort: 10011},
		{AccountPrefix: "robot", ConnectIP: "127.0.0.1", GamePort: 10011, RandIntn: func(int) int { return 0 }, RandBetween: func(int, int) int { return 0 }},
	}
	for _, opts := range cases {
		opts.Paths = paths
		if _, err := ComposeRuntime(context.Background(), opts); err == nil {
			t.Fatalf("incomplete options accepted: %+v", opts)
		}
	}
	// Missing random sources fail before any file or network access.
	_, err := ComposeRuntime(context.Background(), RuntimeComposeOptions{
		AccountPrefix: "robot", ConnectIP: "127.0.0.1", GamePort: 10011,
		Paths: paths, Config: robotconfig.Default(),
	})
	if err == nil {
		t.Fatal("missing random sources accepted")
	}
}
