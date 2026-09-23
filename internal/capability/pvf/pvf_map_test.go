package pvf

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type testTextArchive map[string]string

func (a testTextArchive) ReadText(path string) (string, error) {
	text, ok := a[path]
	if !ok {
		return "", fmt.Errorf("missing %s", path)
	}
	return text, nil
}

func TestReadTownMapCatalogRejectsMissingArchive(t *testing.T) {
	_, err := ReadTownMapCatalog(filepath.Join(t.TempDir(), "missing.pvf"))
	if err == nil {
		t.Fatal("missing PVF archive unexpectedly succeeded")
	}
}

func TestLiveReadNativeTownMapCatalog(t *testing.T) {
	path := os.Getenv("NATIVE_TEST_PVF")
	if path == "" {
		t.Skip("NATIVE_TEST_PVF is not set")
	}
	maps, err := ReadTownMapCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	usable := 0
	for _, item := range maps {
		if item.Use && len(item.Rectangles) > 0 {
			usable++
		}
	}
	if usable == 0 {
		t.Fatalf("parsed %d town areas without usable movement geometry", len(maps))
	}
	t.Logf("parsed %d town areas, %d with usable movement geometry", len(maps), usable)
}

func TestProjectItemCatalogsUsesBackendTextArchive(t *testing.T) {
	archive := testTextArchive{
		"equipment/equipment.lst":         "1001 `weapon/a.equ` 2001 `avatar/mage/cap/b.equ`",
		"equipment/weapon/a.equ":          "[name]\n`Blade`\n[equipment type]\n`[weapon]`\n[minimum level]\n10\n[usable job]\n1\n[/usable job]",
		"equipment/avatar/mage/cap/b.equ": "[name]\n`Hat`\n[equipment type]\n`[hat avatar]`\n[usable job]\n3\n[/usable job]",
		"stackable/stackable.lst":         "3001 `material/c.stk`",
		"stackable/material/c.stk":        "[name]\n`Ore`\n[stack limit]\n1000",
	}
	equipment, stackable, err := ProjectItemCatalogs(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(equipment) != 2 || equipment[0].ID != 1001 || equipment[0].ItemType != 1 || equipment[1].ItemType != 20 {
		t.Fatalf("equipment=%+v", equipment)
	}
	if len(stackable) != 1 || stackable[0].ID != 3001 || !stackable[0].BasicMaterial {
		t.Fatalf("stackable=%+v", stackable)
	}
}
