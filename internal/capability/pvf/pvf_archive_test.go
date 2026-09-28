package pvf

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenPVFRejectsTruncatedFileTreeEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Script.pvf")
	raw := make([]byte, 56)
	binary.LittleEndian.PutUint32(raw[16:20], 1)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := openPVF(path); err == nil || !strings.Contains(err.Error(), "entry 0 truncated") {
		t.Fatalf("openPVF error = %v, want truncated entry", err)
	}
}

func TestExtractItemListMarksPreTypeAddPropertiesIncompatible(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "add cast speed before type", body: "[add cast speed]\r\n20\r\n[equipment type]\r\n`weapon`\r\n", want: true},
		{name: "add physical attack before type", body: "[add physical attack]\r\n27\r\n[equipment type]\r\n`weapon`\r\n", want: true},
		{name: "add property after type", body: "[equipment type]\r\n`weapon`\r\n[add absolute damage]\r\n10\r\n", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := &pvfArchive{files: map[string]*pvfFile{
				"equipment/equipment.lst": {
					Data: []byte("101030240 `character/swordman/weapon/hsword/101030240.equ`"),
				},
				"equipment/character/swordman/weapon/hsword/101030240.equ": {
					Data: []byte("[name]\r\n`weapon`\r\n" + tc.body),
				},
			}}

			items := extractItemList(archive, "equipment/equipment.lst", "equipment/", false)
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1", len(items))
			}
			if items[0].ClientIncompatible != tc.want {
				t.Fatalf("ClientIncompatible = %v, want %v", items[0].ClientIncompatible, tc.want)
			}
		})
	}
}
