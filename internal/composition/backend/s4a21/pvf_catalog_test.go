package s4a21

import (
	"os"
	"testing"
)

func TestLiveReadTownMapCatalog(t *testing.T) {
	path := os.Getenv("S4A21_TEST_PVF")
	if path == "" {
		t.Skip("S4A21_TEST_PVF is not set")
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
