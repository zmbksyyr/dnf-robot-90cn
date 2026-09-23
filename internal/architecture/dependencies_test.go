package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var knownInternalDirs = map[string]bool{
	"actor":        true,
	"architecture": true,
	"bootstrap":    true,
	"capability":   true,
	"composition":  true,
	"entry":        true,
	"foundation":   true,
	"protocol":     true,
	"scheduler":    true,
	"shared":       true,
}

var allowedLayerImports = []struct {
	dir     string
	allowed []string
}{
	{dir: "internal/bootstrap", allowed: []string{"robot/internal/capability", "robot/internal/foundation", "robot/internal/shared"}},
	{dir: "internal/composition", allowed: []string{"robot/internal/composition", "robot/internal/capability", "robot/internal/foundation", "robot/internal/protocol", "robot/internal/shared"}},
	{dir: "internal/entry", allowed: []string{"robot/internal/scheduler", "robot/internal/capability", "robot/internal/foundation", "robot/internal/shared"}},
	{dir: "internal/scheduler", allowed: []string{"robot/internal/scheduler", "robot/internal/actor", "robot/internal/capability", "robot/internal/foundation", "robot/internal/shared"}},
	{dir: "internal/actor", allowed: []string{"robot/internal/capability", "robot/internal/foundation", "robot/internal/shared"}},
	{dir: "internal/capability", allowed: []string{"robot/internal/capability", "robot/internal/foundation", "robot/internal/shared"}},
	{dir: "internal/protocol", allowed: []string{"robot/internal/foundation", "robot/internal/shared", "robot/internal/protocol"}},
	{dir: "internal/foundation", allowed: []string{"robot/internal/foundation", "robot/internal/shared"}},
	{dir: "internal/shared", allowed: nil},
}

func TestTopLevelInternalDirectoriesAreKnownLayers(t *testing.T) {
	root := repoRoot(t)
	internal := filepath.Join(root, "internal")
	entries, err := os.ReadDir(internal)
	if err != nil {
		t.Fatalf("read internal dir: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !knownInternalDirs[entry.Name()] {
			t.Errorf("internal/%s is not assigned to an architecture layer", entry.Name())
		}
	}
}

func TestLayerImportWhitelist(t *testing.T) {
	root := repoRoot(t)
	for _, rule := range allowedLayerImports {
		dir := filepath.Join(root, filepath.FromSlash(rule.dir))
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			checkFileImportWhitelist(t, path, rule.allowed)
			return nil
		}); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func checkFileImportWhitelist(t *testing.T, path string, allowed []string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse imports %s: %v", path, err)
	}
	for _, imp := range file.Imports {
		value := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(value, "robot/internal/") {
			continue
		}
		if importAllowed(value, allowed) {
			continue
		}
		t.Errorf("%s imports unapproved internal layer %s", path, value)
	}
}

func importAllowed(value string, allowed []string) bool {
	for _, prefix := range allowed {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}
