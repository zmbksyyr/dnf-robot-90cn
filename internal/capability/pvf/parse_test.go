package pvf

import (
	"reflect"
	"testing"

	"robot/internal/shared"
)

func assertIntSlice(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length got %d want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice[%d] got %d want %d: got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}

func TestParseRecipeTargetID(t *testing.T) {
	body := "[int data]\n5 27014 1 3037 431 3024 1 3029 16 3047 4 1 27020 1 0\n[/int data]"
	if got := parseRecipeTargetID(body); got != 27020 {
		t.Fatalf("recipe target ID = %d, want 27020", got)
	}
}

func TestEquipmentTypeRecognizesTitleAndMagicStone(t *testing.T) {
	if got := equipmentType("[title name]"); got != 2 {
		t.Fatalf("title name type got %d want 2", got)
	}
	if got := equipmentType("[magic stone]"); got != 12 {
		t.Fatalf("magic stone type got %d want 12", got)
	}
	if got := equipmentType("[red artifact]"); got != 31 {
		t.Fatalf("red artifact type got %d want 31", got)
	}
	if got := equipmentType("[creature blue artifact]"); got != 32 {
		t.Fatalf("creature blue artifact type got %d want 32", got)
	}
	if got := equipmentType("[pet_green_artifact]"); got != 33 {
		t.Fatalf("pet green artifact type got %d want 33", got)
	}
}

func TestParseJobsRecognizesMultiWordJobs(t *testing.T) {
	got := parseJobs("`[swordman]`\t\n`[at gunner]`\n`[thief]`\n`[at fighter]`\n`[at mage]`\n`[at priest]`\n`[demonic swordman]`")
	assertIntSlice(t, got, []int{0, 5, 6, 7, 8, 14, 9})

	got = parseJobs("swordman at gunner thief at fighter at mage at priest demonic swordman")
	assertIntSlice(t, got, []int{0, 5, 6, 7, 8, 14, 9})
}

func TestPriestAvatarPathKeepsGenderJob(t *testing.T) {
	if got := jobFromEquipmentPath("character/priest/avatar/coat/100.equ"); got != 4 {
		t.Fatalf("male priest avatar job got %d want 4", got)
	}
	if got := jobFromEquipmentPath("character/priest/at_avatar/coat/200.equ"); got != 14 {
		t.Fatalf("female priest avatar job got %d want 14", got)
	}
}

func TestSwordmanAvatarPathKeepsGenderJob(t *testing.T) {
	if got := jobFromEquipmentPath("character/swordman/avatar/coat/100.equ"); got != 0 {
		t.Fatalf("male swordman avatar job got %d want 0", got)
	}
	if got := jobFromEquipmentPath("character/swordman/at_avatar/coat/200.equ"); got != 11 {
		t.Fatalf("female swordman avatar job got %d want 11", got)
	}
}

func TestEquipmentExplicitJobsOverridePathFallback(t *testing.T) {
	item := shared.EquipmentCatalogItem{ItemType: 1, UseJob: []int{14}}
	applyEquipmentPathJob(&item, 4)
	assertIntSlice(t, item.UseJob, []int{14})

	item = shared.EquipmentCatalogItem{ItemType: 1}
	applyEquipmentPathJob(&item, 4)
	assertIntSlice(t, item.UseJob, []int{4})

	item = shared.EquipmentCatalogItem{ItemType: 23, UseJob: []int{4}}
	applyEquipmentPathJob(&item, 14)
	assertIntSlice(t, item.UseJob, []int{14})
}

func TestResolvePVFItemSetKeysJoinsMasterMembersAndPackageExtras(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 100, ItemType: 20, UseJob: []int{2}, Name2: "Nobility Hat"},
		{ID: 200, ItemType: 23, UseJob: []int{2}, Name2: "Nobility Coat"},
		{ID: 300, ItemType: 26, UseJob: []int{2}, Name2: "Summer Tube"},
		{ID: 400, ItemType: 28, UseJob: []int{2}, Name2: "Nobility Skin"},
	}
	bodies := map[int]string{
		100: "[set item master]\n200\n[explain]\n`2010年夏日 套裝 3`",
		200: "[set name]\n`2010年夏日 套裝 3`\n[set item]\n100 200\n[/set item]",
		300: "[part set index]\n2\n[explain]\n`2010年夏日禮包 3`",
		400: "[name2]\n`Nobility Skin`",
	}
	setInfo := make(map[int]pvfItemSetInfo, len(bodies))
	for id, body := range bodies {
		setInfo[id] = parsePVFItemSetInfo(body)
	}
	resolvePVFItemSetKeys(items, setInfo)
	want := pvfMasterSetKey(200)
	for _, item := range items {
		if item.SetKey != want {
			t.Fatalf("item %d set key got %q want %q", item.ID, item.SetKey, want)
		}
	}
}

func TestExplicitPVFSetKeyIgnoresPartSetIndex(t *testing.T) {
	if got := explicitPVFSetKey("[part set index]\n2"); got != "" {
		t.Fatalf("part set index became set key %q", got)
	}
	if got := explicitPVFSetKey("[set item master]\n123"); got != "set item master:123" {
		t.Fatalf("master set key got %q", got)
	}
}

func TestAttachAvatarNameFamiliesGroupsColorsAndSlots(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{Name: "绿色扬帆远航帽子", ItemType: 20, UseJob: []int{2}},
		{Name: "黑色扬帆远航头发", ItemType: 21, UseJob: []int{2}},
		{Name: "蓝色扬帆远航墨镜", ItemType: 22, UseJob: []int{2}},
		{Name: "红色扬帆远航上衣", ItemType: 23, UseJob: []int{2}},
		{Name: "白色扬帆远航裤子", ItemType: 24, UseJob: []int{2}},
		{Name: "金色扬帆远航鞋子", ItemType: 25, UseJob: []int{2}},
	}
	attachAvatarNameFamilies(items)
	if items[0].SetKey == "" {
		t.Fatalf("avatar family was not assigned: %q %q %q %q %q %q", avatarNameFamily(items[0].Name), avatarNameFamily(items[1].Name), avatarNameFamily(items[2].Name), avatarNameFamily(items[3].Name), avatarNameFamily(items[4].Name), avatarNameFamily(items[5].Name))
	}
	for _, item := range items[1:] {
		if item.SetKey != items[0].SetKey {
			t.Fatalf("avatar family key %q differs from %q", item.SetKey, items[0].SetKey)
		}
	}
}

func TestAttachEquipmentNameFamiliesRequiresArmorCoverage(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{Name: "远古尘封术士上衣", ItemType: 3, UseJob: []int{3}},
		{Name: "远古尘封术士护肩", ItemType: 4, UseJob: []int{3}},
		{Name: "远古尘封术士下装", ItemType: 5, UseJob: []int{3}},
		{Name: "远古尘封术士鞋", ItemType: 6, UseJob: []int{3}},
		{Name: "远古尘封术士腰带", ItemType: 7, UseJob: []int{3}},
		{Name: "普通散件上衣", ItemType: 3, UseJob: []int{3}},
	}
	attachEquipmentNameFamilies(items)
	if items[0].SetKey == "" {
		t.Fatal("equipment family was not assigned")
	}
	for _, item := range items[1:5] {
		if item.SetKey != items[0].SetKey {
			t.Fatalf("equipment family key %q differs from %q", item.SetKey, items[0].SetKey)
		}
	}
	if items[5].SetKey != "" {
		t.Fatalf("unrelated equipment received set key %q", items[5].SetKey)
	}
}

func TestAppendItemInfoCreatureArtifacts(t *testing.T) {
	raw := "#PVF_File\r\n" +
		"63500 1 1 1 1 1 1 1 1 1 1 1 1 70 `red` `red2` 14002\r\n" +
		"64000 2 1 1 1 1 1 1 1 1 1 1 1 70 `blue` `blue2` 14003\r\n" +
		"64500 3 1 1 1 1 1 1 1 1 1 1 1 70 `green` `green2` 14004\r\n" +
		"63000 1 1 1 1 1 1 1 1 1 1 1 1 70 `creature` `creature2` 14001\r\n"
	got := appendItemInfoCreatureArtifacts(nil, raw)
	if len(got) != 4 {
		t.Fatalf("creature/artifact count got %d want 4: %#v", len(got), got)
	}
	if got[0].ID != 63000 || got[0].Slot != "creature" || got[0].ItemType != 30 {
		t.Fatalf("creature not parsed: %#v", got[0])
	}
	if got[1].ID != 63500 || got[1].Slot != "artifact red" || got[1].ItemType != 31 {
		t.Fatalf("red artifact not parsed: %#v", got[1])
	}
	if got[2].ID != 64000 || got[2].Slot != "artifact blue" || got[2].ItemType != 32 {
		t.Fatalf("blue artifact not parsed: %#v", got[2])
	}
	if got[3].ID != 64500 || got[3].Slot != "artifact green" || got[3].ItemType != 33 {
		t.Fatalf("green artifact not parsed: %#v", got[3])
	}
}

func TestParseTownAreasKeepsMapPathAndGateMetadata(t *testing.T) {
	body := "[area]\n0 `HendonMyre/Hendon.map`\n`[normal]`\n[/area]\n" +
		"[area]\n1 `HendonMyre/Gate.map`\n`[gate]`\n474 234\n[/area]\n" +
		"[area]\n2 `HendonMyre/Hendon_Auction.map`\n`[normal]`\n[/area]\n"
	got := parseTownAreas(body)
	if len(got) != 3 {
		t.Fatalf("areas=%+v", got)
	}
	if got[0].ID != 0 || got[0].MapPath != "hendonmyre/hendon.map" || got[0].Gate || got[0].Kind != "normal" {
		t.Fatalf("first area=%+v", got[0])
	}
	if got[1].ID != 1 || got[1].MapPath != "hendonmyre/gate.map" || !got[1].Gate || got[1].Kind != "gate" {
		t.Fatalf("second area=%+v", got[1])
	}
	if got[2].ID != 2 || got[2].MapPath != "hendonmyre/hendon_auction.map" || got[2].Gate || got[2].Kind != "normal" {
		t.Fatalf("third area=%+v", got[2])
	}
}

func TestParseTownAreasMarksExplicitPVP(t *testing.T) {
	got := parseTownAreas("[area]\n7 `Arena/Ready.map`\n`[pvp]`\n[/area]\n[end]")
	if len(got) != 1 || got[0].Kind != "pvp" || got[0].Gate {
		t.Fatalf("pvp area=%+v", got)
	}
}

func TestTownMapMovableBoundsUsesVirtualRectangles(t *testing.T) {
	body := "[town movable area]\n10 100 20 40 2 1 500 120 30 50 2 2\n" +
		"900 140 40 20 3 0\n[/town movable area]\n" +
		"[virtual movable area]\n30 90 100 60 500 120 300 50\n" +
		"[unknown metadata]\n4117\n[nested condition]\n99\n[/nested condition]\n[/unknown metadata]\n" +
		"900 140 400 20\n[/virtual movable area]\n" +
		"[pvp start area]\n200 110 50 20\n[type]\n`[normal]`\n"
	xMin, xMax, yMin, yMax, ok := townMapMovableBounds(body)
	if !ok || xMin != 30 || xMax != 1300 || yMin != 90 || yMax != 170 {
		t.Fatalf("bounds=%d..%d/%d..%d ok=%t", xMin, xMax, yMin, yMax, ok)
	}
	rectangles := townMapMovableRectangles(body)
	want := []shared.MapRectangle{
		{XMin: 30, XMax: 130, YMin: 90, YMax: 150},
		{XMin: 500, XMax: 800, YMin: 120, YMax: 170},
		{XMin: 900, XMax: 1300, YMin: 140, YMax: 160},
	}
	if !reflect.DeepEqual(rectangles, want) {
		t.Fatalf("rectangles=%+v want %+v", rectangles, want)
	}
}

func TestTownMapMovableBoundsClampsAndRejectsMissingData(t *testing.T) {
	xMin, xMax, yMin, yMax, ok := townMapMovableBounds("[virtual movable area]\n-20 -10 100 80\n[/virtual movable area]")
	if !ok || xMin != 0 || xMax != 80 || yMin != 0 || yMax != 70 {
		t.Fatalf("clamped bounds=%d..%d/%d..%d ok=%t", xMin, xMax, yMin, yMax, ok)
	}
	xMin, xMax, yMin, yMax, ok = townMapMovableBounds("[virtual movable area]\n-20 -10 70000 70000\n[/virtual movable area]")
	if ok || xMin != 0 || xMax != 0 || yMin != 0 || yMax != 0 {
		t.Fatalf("oversized rectangle produced coordinates: %d..%d/%d..%d ok=%t", xMin, xMax, yMin, yMax, ok)
	}
	for _, body := range []string{
		"[town movable area]\n10 20 100 80 1 0\n[/town movable area]",
		"[virtual movable area]\n10 20 100\n[/virtual movable area]",
		"[pvp start area]\n10 20 100 80\n[/pvp start area]",
		"[pvp practice start area]\n10 20 100 80\n[/pvp practice start area]",
	} {
		xMin, xMax, yMin, yMax, ok = townMapMovableBounds(body)
		if ok || xMin != 0 || xMax != 0 || yMin != 0 || yMax != 0 {
			t.Fatalf("non-town area produced coordinates: %d..%d/%d..%d ok=%t", xMin, xMax, yMin, yMax, ok)
		}
	}
}

func TestExtractMapListNeverFabricatesAreasOrCoordinates(t *testing.T) {
	a := &pvfArchive{files: map[string]*pvfFile{
		"town/town.lst": {Data: []byte("1 `Example.twn`")},
		"town/example.twn": {Data: []byte("[name]\n`Example`\n[limit level]\n10\n" +
			"[area]\n0 `Example/Ready.map`\n`[normal]`\n[/area]\n" +
			"[area]\n1 `Example/Virtual.map`\n`[normal]`\n[/area]\n" +
			"[area]\n2 `Example/Missing.map`\n`[normal]`\n[/area]\n" +
			"[area]\n3 `Example/Gate.map`\n`[gate]`\n474 234\n[/area]\n" +
			"[area]\n4 `Example/MissingGate.map`\n`[gate]`\n474 234\n[/area]\n[end]\n")},
		"map/example/ready.map":   {Data: []byte("[town movable area]\n10 20 30 100 1 0\n[/town movable area]\n[virtual movable area]\n10 20 300 100\n[/virtual movable area]\n")},
		"map/example/virtual.map": {Data: []byte("[virtual movable area]\n10 20 300 100\n[/virtual movable area]\n")},
		"map/example/gate.map":    {Data: []byte("[virtual movable area]\n50 60 400 200\n[/virtual movable area]\n")},
	}}
	maps := extractMapList(a, "town/town.lst", "town/")
	if len(maps) != 5 {
		t.Fatalf("maps=%+v", maps)
	}
	if !maps[0].Use || maps[0].XMin != 10 || maps[0].XMax != 310 || maps[0].YMin != 20 || maps[0].YMax != 120 || len(maps[0].Rectangles) != 1 {
		t.Fatalf("ready map=%+v", maps[0])
	}
	if maps[0].NormalEligible == nil || !*maps[0].NormalEligible || maps[0].StoreEligible == nil || !*maps[0].StoreEligible || maps[0].StoreProbe == nil || *maps[0].StoreProbe {
		t.Fatalf("normal map eligibility was not exported: %+v", maps[0])
	}
	if !maps[1].Use || maps[1].XMin != 10 || maps[1].XMax != 310 || maps[1].YMin != 20 || maps[1].YMax != 120 || len(maps[1].Rectangles) != 1 {
		t.Fatalf("virtual-only map was not usable: %+v", maps[1])
	}
	if maps[2].Use || maps[2].XMin != 0 || maps[2].XMax != 0 || maps[2].YMin != 0 || maps[2].YMax != 0 {
		t.Fatalf("missing map fabricated coordinates: %+v", maps[2])
	}
	if !maps[3].Gate || !maps[3].Use || maps[3].XMin != 50 || maps[3].XMax != 450 {
		t.Fatalf("gate map with virtual geometry was not exported: %+v", maps[3])
	}
	if maps[3].NormalEligible == nil || !*maps[3].NormalEligible || maps[3].StoreEligible == nil || *maps[3].StoreEligible || maps[3].StoreProbe == nil || *maps[3].StoreProbe {
		t.Fatalf("gate eligibility was not split: %+v", maps[3])
	}
	if !maps[4].Gate || maps[4].Use {
		t.Fatalf("gate map without virtual geometry became usable: %+v", maps[4])
	}

	if areas := parseTownAreas("[name]\n`No Areas`"); len(areas) != 0 {
		t.Fatalf("missing area block fabricated areas: %+v", areas)
	}
}

func TestExtractMapListUsesRegionalMapReplacementOnlyWhenOriginalIsMissing(t *testing.T) {
	a := &pvfArchive{files: map[string]*pvfFile{
		"town/town.lst": {Data: []byte("1 `Example.twn`")},
		"town/example.twn": {Data: []byte(
			"[name]\n`Example`\n" +
				"[area]\n0 `Example/Original.map`\n`[normal]`\n[/area]\n" +
				"[area]\n1 `Example/Regional.map`\n`[normal]`\n[/area]\n[end]\n")},
		"map/example/original.map":    {Data: []byte("[virtual movable area]\n10 20 30 40\n[/virtual movable area]\n")},
		"map/example/(r)original.map": {Data: []byte("[virtual movable area]\n110 120 30 40\n[/virtual movable area]\n")},
		"map/example/(r)regional.map": {Data: []byte("[virtual movable area]\n210 220 30 40\n[/virtual movable area]\n")},
	}}
	maps := extractMapList(a, "town/town.lst", "town/")
	if len(maps) != 2 {
		t.Fatalf("maps=%+v", maps)
	}
	if !maps[0].Use || maps[0].XMin != 10 || maps[0].YMin != 20 {
		t.Fatalf("original map was not preferred: %+v", maps[0])
	}
	if !maps[1].Use || maps[1].XMin != 210 || maps[1].YMin != 220 {
		t.Fatalf("regional replacement was not used: %+v", maps[1])
	}
}

func TestExtractMapListDerivesEligibilityFromPVFAreaKind(t *testing.T) {
	geometry := []byte("[virtual movable area]\n10 20 300 100\n[/virtual movable area]\n")
	a := &pvfArchive{files: map[string]*pvfFile{
		"town/town.lst": {Data: []byte("1 `Example.twn`")},
		"town/example.twn": {Data: []byte(
			"[name]\n`Example`\n[area]\n0 `Example/Normal.map`\n`[normal]`\n[/area]\n" +
				"[area]\n1 `Example/Waiting.map`\n[/area]\n" +
				"[area]\n2 `Example/PVP.map`\n`[pvp]`\n[/area]\n[end]\n")},
		"map/example/normal.map":  {Data: geometry},
		"map/example/waiting.map": {Data: geometry},
		"map/example/pvp.map":     {Data: geometry},
	}}
	maps := extractMapList(a, "town/town.lst", "town/")
	if len(maps) != 3 {
		t.Fatalf("maps=%+v", maps)
	}
	if !*maps[0].NormalEligible || !*maps[0].StoreEligible || *maps[0].StoreProbe {
		t.Fatalf("normal eligibility=%+v", maps[0])
	}
	if !*maps[1].NormalEligible || *maps[1].StoreEligible || !*maps[1].StoreProbe {
		t.Fatalf("waiting eligibility=%+v", maps[1])
	}
	if *maps[2].NormalEligible || *maps[2].StoreEligible || *maps[2].StoreProbe {
		t.Fatalf("pvp eligibility=%+v", maps[2])
	}
}
