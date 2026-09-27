package pvf

import (
	"reflect"
	"testing"
)

func TestProjectJobGrowCatalogReadsReleasedBranches(t *testing.T) {
	archive := testTextArchive{
		"character/character.lst":         "0 `Swordman/Swordman.chr` 1 `Fighter/Fighter.chr` 2 `Gunner/Gunner.chr`",
		"character/swordman/swordman.chr": "[name]\r\n`swordman`\r\n[growtype name]\r\n`鬼剑士` `剑魂` `鬼泣` `//阿修罗` `狂战士`\r\n[growtype 1]\r\n`skill`\r\n",
		"character/fighter/fighter.chr":   "[growtype name] `格斗家` `气功师` `散打`\r\n",
		"character/gunner/gunner.chr":     "[name]\r\n`gunner`\r\n",
	}

	got := ProjectJobGrowCatalog(archive)
	want := map[int][]int{0: {1, 2, 4}, 1: {1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("job grow catalog = %v, want %v", got, want)
	}
}

func TestParseJobGrowBranchesIgnoresMissingAndMalformedNames(t *testing.T) {
	if branches := parseJobGrowBranches("[name]\r\n`base`\r\n"); len(branches) != 0 {
		t.Fatalf("missing tag branches = %v", branches)
	}
	if branches := parseJobGrowBranches("[growtype name] `base` `` `//soon`"); len(branches) != 0 {
		t.Fatalf("placeholder-only branches = %v", branches)
	}
	// Values above the server's first-grow guard must not be offered.
	body := "[growtype name] `base` `a` `b` `c` `d` `e` `f`"
	if branches := parseJobGrowBranches(body); !reflect.DeepEqual(branches, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("guarded branches = %v", branches)
	}
}
