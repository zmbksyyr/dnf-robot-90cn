package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var forbiddenFileNameTokens = []string{
	"compat",
	"temp",
	"legacy",
	"bridge",
	"adapter",
	"misc",
	"helper",
}

var forbiddenRuntimeArtifactSuffixes = []string{
	".bak",
	".jsonl",
	".log",
	".tmp",
}

var sqlImportAllowedDirs = []string{
	"cmd/robot",
	"internal/foundation/sql",
	"internal/foundation/dbstatus",
	"internal/scheduler/repository",
	"internal/capability/mailnotify",
	"internal/capability/marketapp",
	"internal/composition/backend/s4a21",
	"internal/protocol/dnf",
}

var actionResultStateDirs = []string{
	"internal/actor",
	"internal/scheduler",
	"internal/capability/robotaction",
	"internal/capability/store",
}

func TestActionResultStatesUseNamedConstants(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range actionResultStateDirs {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch x := node.(type) {
				case *ast.CompositeLit:
					if !isActionResultType(x.Type) {
						return true
					}
					for _, elt := range x.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok || !identNamed(kv.Key, "State") || !isStringLiteral(kv.Value) {
							continue
						}
						t.Errorf("%s uses literal ActionResult.State in composite literal", path)
					}
				case *ast.AssignStmt:
					for i, lhs := range x.Lhs {
						if i >= len(x.Rhs) || !selectorNamed(lhs, "State") || !isStringLiteral(x.Rhs[i]) {
							continue
						}
						t.Errorf("%s assigns literal ActionResult.State", path)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func TestMarketAppLocksUsePurposeNames(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal", "capability", "marketapp", "marketapp_app.go")
	assertStructHasNoGenericLockField(t, path, "App")
}

func TestMarketAppLogEventStatusUsesNamedConstants(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "internal", "capability", "marketapp")
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok || !isLogEventType(lit.Type) {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok || !identNamed(kv.Key, "Status") || !isStringLiteral(kv.Value) {
					continue
				}
				t.Errorf("%s uses literal LogEvent.Status; use marketLogStatus* constants", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}

func TestActorLocksUsePurposeNames(t *testing.T) {
	root := repoRoot(t)
	assertStructHasNoGenericLockField(t, filepath.Join(root, "internal", "actor", "actor_core.go"), "Actor")
	assertStructHasNoGenericLockField(t, filepath.Join(root, "internal", "actor", "ledger.go"), "Ledger")
}

func TestStoreLocksUsePurposeNames(t *testing.T) {
	root := repoRoot(t)
	assertStructHasNoGenericLockField(t, filepath.Join(root, "internal", "capability", "store", "coordinator.go"), "PointCoordinator")
}

func assertStructHasNoGenericLockField(t *testing.T, path, structName string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			st, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == "mu" {
						t.Errorf("%s %s lock field uses generic name mu; use a purpose name", path, structName)
					}
				}
			}
		}
	}
}

func TestMutexDeclarationsStayInsideLockhub(t *testing.T) {
	root := repoRoot(t)
	internal := filepath.Join(root, "internal")
	err := filepath.WalkDir(internal, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/foundation/lockhub/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		mutexToken := "sync" + "." + "Mutex"
		rwMutexToken := "sync" + "." + "RWMutex"
		if strings.Contains(text, mutexToken) || strings.Contains(text, rwMutexToken) {
			t.Errorf("%s declares raw mutex outside lockhub", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internal, err)
	}
}

func TestSQLPackageImportsStayInRepositoryOrProtocolCode(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "database/sql" {
				continue
			}
			if !pathUnderAny(root, path, sqlImportAllowedDirs) {
				t.Errorf("%s imports database/sql outside approved SQL boundary", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestGoFileNamesDoNotUseTemporaryStructureNames(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".go"))
		for _, token := range forbiddenFileNameTokens {
			if base == token || strings.HasPrefix(base, token+"_") || strings.HasSuffix(base, "_"+token) || strings.Contains(base, "_"+token+"_") {
				t.Errorf("%s uses temporary structure token %q in file name", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestReadmeFilesDoNotFragmentDocumentation(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Base(path), "README.md") {
			t.Errorf("%s fragments documentation; use doc/规整文档.md", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestRuntimeArtifactsDoNotStayInRepository(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		base := strings.ToLower(filepath.Base(path))
		for _, suffix := range forbiddenRuntimeArtifactSuffixes {
			if strings.HasSuffix(base, suffix) {
				t.Errorf("%s is a runtime or temporary artifact; keep generated files outside the repository", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestSchedulerDoesNotExposeActionFacadeMethods(t *testing.T) {
	root := repoRoot(t)
	schedulerDir := filepath.Join(root, "internal", "scheduler")
	forbidden := map[string]bool{
		"Online":          true,
		"OnlineNoConfirm": true,
		"Logout":          true,
		"Move":            true,
		"ShoutOne":        true,
		"Store":           true,
	}
	err := filepath.WalkDir(schedulerDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !forbidden[fn.Name.Name] {
				continue
			}
			for _, field := range fn.Recv.List {
				if receiverIsRobotManager(field.Type) {
					t.Errorf("%s defines RobotManager.%s action facade; use managed command entry or capability service", path, fn.Name.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", schedulerDir, err)
	}
}

func TestSchedulerLockResourcesUseNamedConstants(t *testing.T) {
	root := repoRoot(t)
	targets := []string{
		filepath.Join(root, "internal", "scheduler"),
		filepath.Join(root, "internal", "scheduler", "repository"),
	}
	for _, dir := range targets {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(data)
			for _, token := range []string{
				`WithResource("scheduler"`,
				`WithResource("repository"`,
				`WithResource("config"`,
			} {
				if strings.Contains(text, token) {
					t.Errorf("%s uses literal lock resource scope %s; use named lock resource constants", path, token)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func receiverIsRobotManager(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.StarExpr:
		return receiverIsRobotManager(x.X)
	case *ast.Ident:
		return x.Name == "RobotManager"
	default:
		return false
	}
}

func isActionResultType(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "ActionResult"
	case *ast.Ident:
		return x.Name == "ActionResult"
	default:
		return false
	}
}

func isLogEventType(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "LogEvent"
	case *ast.Ident:
		return x.Name == "LogEvent"
	default:
		return false
	}
}

func identNamed(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func selectorNamed(expr ast.Expr, name string) bool {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == name
	case *ast.IndexExpr:
		return selectorNamed(x.X, name)
	default:
		return false
	}
}

func isStringLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func pathUnderAny(root string, path string, dirs []string) bool {
	cleanPath := filepath.ToSlash(filepath.Clean(path))
	for _, dir := range dirs {
		cleanDir := filepath.ToSlash(filepath.Join(root, filepath.FromSlash(dir)))
		if cleanPath == cleanDir || strings.HasPrefix(cleanPath, cleanDir+"/") {
			return true
		}
	}
	return false
}
