package s4a21

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/layout"
)

func TestResolvePVFPathUsesServerDataLayout(t *testing.T) {
	t.Setenv(pvfArchivePathEnv, "")
	dir := t.TempDir()
	pvf := filepath.Join(dir, "Data", "Pvf", "Script.pvf")
	if err := os.MkdirAll(filepath.Dir(pvf), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pvf, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePVFPath(dir); err != nil || got != pvf {
		t.Fatalf("directory resolution = %q, %v", got, err)
	}
	if got, err := ResolvePVFPath(filepath.Join(dir, "DfoServer.exe")); err != nil || got != pvf {
		t.Fatalf("executable resolution = %q, %v", got, err)
	}
	if _, err := ResolvePVFPath(""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
	if _, err := ResolvePVFPath(t.TempDir()); err == nil {
		t.Fatal("directory without a PVF archive was accepted")
	}
	if _, err := ResolvePVFPath(pvf); err == nil {
		t.Fatal("a .pvf file was accepted as the server directory")
	}
}

func TestResolvePVFPathMirrorsServerArchiveOverride(t *testing.T) {
	dir := t.TempDir()
	defaultPVF := filepath.Join(dir, "Data", "Pvf", "Script.pvf")
	if err := os.MkdirAll(filepath.Dir(defaultPVF), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPVF, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dir, "custom.pvf")
	if err := os.WriteFile(custom, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pvfArchivePathEnv, custom)
	if got, err := ResolvePVFPath(dir); err != nil || got != custom {
		t.Fatalf("absolute override = %q, %v", got, err)
	}
	t.Setenv(pvfArchivePathEnv, "custom.pvf")
	if got, err := ResolvePVFPath(dir); err != nil || got != custom {
		t.Fatalf("relative override = %q, %v", got, err)
	}
	t.Setenv(pvfArchivePathEnv, "missing.pvf")
	if got, err := ResolvePVFPath(dir); err != nil || got != defaultPVF {
		t.Fatalf("missing override fallback = %q, %v", got, err)
	}
}

func TestResolvePVFPathFallsBackToFirstArchive(t *testing.T) {
	t.Setenv(pvfArchivePathEnv, "")
	dir := t.TempDir()
	pvfDir := filepath.Join(dir, "Data", "Pvf")
	if err := os.MkdirAll(pvfDir, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(pvfDir, "Other.pvf")
	if err := os.WriteFile(archive, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePVFPath(dir); err != nil || got != archive {
		t.Fatalf("Data/Pvf fallback = %q, %v", got, err)
	}

	legacy := t.TempDir()
	legacyArchive := filepath.Join(legacy, "Client.pvf")
	if err := os.WriteFile(legacyArchive, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePVFPath(legacy); err != nil || got != legacyArchive {
		t.Fatalf("server base fallback = %q, %v", got, err)
	}
}

func TestResolvePathsReportSources(t *testing.T) {
	t.Setenv(pvfArchivePathEnv, "")
	t.Setenv(inventoryDatabasePathEnv, "")
	dir := t.TempDir()
	pvf := filepath.Join(dir, "Data", "Pvf", "Script.pvf")
	if err := os.MkdirAll(filepath.Dir(pvf), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pvf, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "Data", "inventory.db")
	if err := os.WriteFile(db, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, source, err := resolvePVFPath(dir); err != nil || source != "Data/Pvf/Script.pvf" {
		t.Fatalf("default PVF source=%q err=%v", source, err)
	}
	if _, source, err := resolveDatabasePath(dir, ""); err != nil || source != "Data/inventory.db" {
		t.Fatalf("derived database source=%q err=%v", source, err)
	}

	custom := filepath.Join(dir, "custom.pvf")
	if err := os.WriteFile(custom, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pvfArchivePathEnv, custom)
	if path, source, err := resolvePVFPath(dir); err != nil || path != custom || source != "env "+pvfArchivePathEnv {
		t.Fatalf("override PVF path=%q source=%q err=%v", path, source, err)
	}

	explicit := filepath.Join(dir, "explicit.db")
	if err := os.WriteFile(explicit, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if path, source, err := resolveDatabasePath(dir, explicit); err != nil || path != explicit || source != "adapter database_path setting" {
		t.Fatalf("explicit database path=%q source=%q err=%v", path, source, err)
	}
	t.Setenv(inventoryDatabasePathEnv, "env.db")
	if err := os.WriteFile(filepath.Join(dir, "env.db"), []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, source, err := resolveDatabasePath(dir, ""); err != nil || source != "env "+inventoryDatabasePathEnv {
		t.Fatalf("environment database source=%q err=%v", source, err)
	}
}

func TestSamePathNormalizesRelativeSegments(t *testing.T) {
	dir := t.TempDir()
	if !samePath(filepath.Join(dir, "a.db"), filepath.Join(dir, ".", "a.db")) {
		t.Fatal("equivalent paths reported as different")
	}
	if samePath(filepath.Join(dir, "a.db"), filepath.Join(dir, "b.db")) {
		t.Fatal("different paths reported as equal")
	}
}

func TestResolveDatabasePathPrefersExplicitPath(t *testing.T) {
	t.Setenv(inventoryDatabasePathEnv, "")
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
	if _, err := ResolveDatabasePath(filepath.Join(serverDir, "Data", "Pvf", "Script.pvf"), ""); err == nil {
		t.Fatal("a .pvf file was accepted as the server base for the database")
	}
	if _, err := ResolveDatabasePath("", ""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
}

func TestResolveDatabasePathMirrorsServerOverride(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(serverDir, "Data"), 0755); err != nil {
		t.Fatal(err)
	}
	derived := filepath.Join(serverDir, "Data", "inventory.db")
	if err := os.WriteFile(derived, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(serverDir, "custom.db")
	if err := os.WriteFile(custom, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(inventoryDatabasePathEnv, custom)
	if got, err := ResolveDatabasePath(serverDir, ""); err != nil || got != custom {
		t.Fatalf("absolute override = %q, %v", got, err)
	}
	t.Setenv(inventoryDatabasePathEnv, "custom.db")
	if got, err := ResolveDatabasePath(serverDir, ""); err != nil || got != custom {
		t.Fatalf("relative override = %q, %v", got, err)
	}
	if got, err := ResolveDatabasePath(serverDir, derived); err != nil || got != derived {
		t.Fatalf("explicit setting = %q, %v", got, err)
	}
	t.Setenv(inventoryDatabasePathEnv, "missing.db")
	if _, err := ResolveDatabasePath(serverDir, ""); err == nil {
		t.Fatal("missing override was accepted")
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
