package pvf

import (
	"testing"
)

const testCharacterFile = `[name]
` + "`swordman`" + `
[initial value]
[HP MAX] 100
[MP MAX] 50
[physical attack] 10
[growtype 1]
[HP MAX] 1
[growtype 2]
[HP MAX] 2
[awakening 1]
[HP MAX] 3
[awakening 2]
[HP MAX] 4
[growtype 3]
[HP MAX] 20
`

func TestParseCharacterStatTablesSplitsGrowthAndAwakeningRows(t *testing.T) {
	tables, err := parseCharacterStatTables(testCharacterFile)
	if err != nil {
		t.Fatal(err)
	}
	if tables.Base.HpMax != 1000 || tables.Base.MpMax != 500 || tables.Base.PhysAtk != 100 {
		t.Fatalf("base row=%+v", tables.Base)
	}
	if !tables.GrowtypeSet[1] || !tables.GrowtypeSet[2] || !tables.GrowtypeSet[3] {
		t.Fatalf("growtype rows set=%v", tables.GrowtypeSet)
	}
	if tables.GrowtypeSet[4] || tables.GrowtypeSet[5] || tables.GrowtypeSet[6] {
		t.Fatalf("unexpected growtype rows set=%v", tables.GrowtypeSet)
	}
	if tables.Growtype[1].HpMax != 10 || tables.Growtype[2].HpMax != 20 || tables.Growtype[3].HpMax != 200 {
		t.Fatalf("growtype rows=%v", tables.Growtype)
	}
	if !tables.AwakenSet[2][1] || !tables.AwakenSet[2][2] {
		t.Fatalf("awakening rows set=%v", tables.AwakenSet)
	}
	if tables.Awakening[2][1].HpMax != 30 || tables.Awakening[2][2].HpMax != 40 {
		t.Fatalf("awakening rows=%v", tables.Awakening)
	}
	if tables.AwakenSet[3][1] {
		t.Fatalf("unexpected awakening row set=%v", tables.AwakenSet)
	}
	if _, err := parseCharacterStatTables("[initial value]\n[HP MAX] 1\n"); err == nil {
		t.Fatal("file without [growtype 1] was accepted")
	}
}

func TestBuildAdditionalInfoMatchesServerAccumulation(t *testing.T) {
	tables, err := parseCharacterStatTables(testCharacterFile)
	if err != nil {
		t.Fatal(err)
	}
	hp := func(level, first, second int) int64 {
		blob, err := BuildAdditionalInfo(tables, level, first, second)
		if err != nil {
			t.Fatalf("build level=%d first=%d second=%d: %v", level, first, second, err)
		}
		fields, err := ParseCombatStatFields(blob)
		if err != nil {
			t.Fatal(err)
		}
		return fields.HpMax
	}
	// Base 1000 + premium 9800.
	if got := hp(1, 0, 0); got != 10800 {
		t.Fatalf("level 1 hp=%d", got)
	}
	// 14 levels of the base grow row (10/level).
	if got := hp(15, 0, 0); got != 1000+140+9800 {
		t.Fatalf("level 15 hp=%d", got)
	}
	// Transfer to branch 1: levels 15..49 use [growtype 2] (20/level).
	if got := hp(50, 1, 0); got != 1000+140+700+9800 {
		t.Fatalf("level 50 branch hp=%d", got)
	}
	// Awakening 1 switches the 50+ segment to [awakening 1] (30/level).
	if got := hp(51, 1, 1); got != 1000+140+700+30+9800 {
		t.Fatalf("level 51 awakening hp=%d", got)
	}
	// Branch 2 uses [growtype 3] (200/level) for 15..49.
	if got := hp(50, 2, 0); got != 1000+140+7000+9800 {
		t.Fatalf("level 50 branch 2 hp=%d", got)
	}
	if _, err := BuildAdditionalInfo(tables, 60, 2, 1); err == nil {
		t.Fatal("missing awakening row was accepted")
	}
}

func TestParseCombatStatFieldsUsesServerOffsets(t *testing.T) {
	blob, err := BuildAdditionalInfo(FallbackCharacterStatTables(), 1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ParseCombatStatFields(blob)
	if err != nil {
		t.Fatal(err)
	}
	if fields.HpMax != 1800+9800 || fields.MpMax != 1400+10500 {
		t.Fatalf("hp/mp=%d/%d", fields.HpMax, fields.MpMax)
	}
	if fields.PhysAtk != 75 || fields.PhysDef != 75 || fields.MagAtk != 45 || fields.MagDef != 45 {
		t.Fatalf("attack/defense=%+v", fields)
	}
	if fields.InventoryLimit != 480000 || fields.MpRegen != 500 || fields.MoveSpeed != 8500 {
		t.Fatalf("inventory/regen/move=%+v", fields)
	}
	if fields.AttackSpeed != 8500 || fields.CastSpeed != 7000 || fields.HitRecovery != 6000 ||
		fields.JumpPower != 4300 || fields.Weight != 500000 {
		t.Fatalf("speed/weight=%+v", fields)
	}
	if _, err := ParseCombatStatFields(blob[:81]); err == nil {
		t.Fatal("short blob was accepted")
	}
}

func TestProjectLevelThresholdsAndLevelExpFor(t *testing.T) {
	archive := testTextArchive{"character/exptable.tbl": "0 100 300 600 1000"}
	thresholds := ProjectLevelThresholds(archive)
	if len(thresholds) != 5 || thresholds[2] != 300 {
		t.Fatalf("thresholds=%v", thresholds)
	}
	for level, want := range map[int]int{1: 0, 2: 0, 3: 100, 4: 300, 5: 600, 6: 1000, 99: 1000} {
		if got := LevelExpFor(thresholds, level); got != want {
			t.Fatalf("level %d exp=%d want %d", level, got, want)
		}
	}
	if got := LevelExpFor(nil, 50); got != 0 {
		t.Fatalf("empty thresholds exp=%d", got)
	}
}

func TestProjectCharacterStatCatalogFallsBackOnMalformedFile(t *testing.T) {
	archive := testTextArchive{
		"character/character.lst":         "0 `Swordman/Swordman.chr` 1 `Broken/Broken.chr`",
		"character/swordman/swordman.chr": testCharacterFile,
		"character/broken/broken.chr":     "[name]\n`broken`\n",
	}
	catalog, fallbacks := ProjectCharacterStatCatalog(archive)
	if len(catalog) != 2 {
		t.Fatalf("catalog=%v", catalog)
	}
	if catalog[0].Base.HpMax != 1000 {
		t.Fatalf("parsed job=%+v", catalog[0])
	}
	if len(fallbacks) != 1 || fallbacks[0] != 1 {
		t.Fatalf("fallback jobs=%v", fallbacks)
	}
	fallback := catalog[1]
	if !fallback.GrowtypeSet[1] || fallback.Base.HpMax != 1800 {
		t.Fatalf("fallback job=%+v", fallback)
	}
	// The fallback stays buildable above level 1: base row only, no growth.
	blob, err := BuildAdditionalInfo(fallback, 50, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ParseCombatStatFields(blob)
	if err != nil {
		t.Fatal(err)
	}
	if fields.HpMax != 1800+9800 {
		t.Fatalf("fallback level 50 hp=%d", fields.HpMax)
	}
}
