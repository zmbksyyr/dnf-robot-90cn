package pvf

import (
	"reflect"
	"testing"

	"robot/internal/shared"
)

func TestExtractSkillStateCatalogIndexesResolvedPVFStates(t *testing.T) {
	archive := &pvfArchive{files: map[string]*pvfFile{
		"sqr/character/new_atmage_load_state.nut": {
			Data: []byte(`
IRDSQRCharacter.pushScriptFiles("character/atmage/header.nut");
IRDSQRCharacter.pushState(ENUM_CHARACTERJOB_AT_MAGE, "character/atmage/safe/safe.nut", "safe", STATE_SAFE, SKILL_SAFE);
IRDSQRCharacter.pushState(ENUM_CHARACTERJOB_AT_MAGE, "character/atmage/data/data.nut", "data", 22, 3);
IRDSQRCharacter.pushState(ENUM_CHARACTERJOB_AT_MAGE, "character/atmage/native/native.nut", "native", 23, 4);
`),
		},
		"sqr/character/atmage/header.nut": {
			Data: []byte("STATE_SAFE <- 20;\nSKILL_SAFE <- 1;\n"),
		},
		"sqr/character/atmage/safe/safe.nut": {
			Data: []byte("function checkExecutableSkill_safe(obj) { return true; }"),
		},
		"sqr/character/atmage/data/data.nut": {
			Data: []byte("function onAfterSetState_data(obj, state, datas, reset) { return obj.sq_GetVectorData(datas, 0); }"),
		},
		"sqr/character/atmage/native/native.nut": {
			Data: []byte("function onSetState_native(obj, state, datas, reset) { obj.setCurrentAnimation(2); }"),
		},
	}}

	want := []SkillState{
		{Job: 8, SkillIndex: 1, State: 20, ScriptPath: "sqr/character/atmage/safe/safe.nut"},
		{Job: 8, SkillIndex: 3, State: 22, ScriptPath: "sqr/character/atmage/data/data.nut"},
		{Job: 8, SkillIndex: 4, State: 23, ScriptPath: "sqr/character/atmage/native/native.nut"},
	}
	if got := extractSkillStateCatalog(archive); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill state catalog = %+v, want %+v", got, want)
	}
}

func TestExtractSkillStateCatalogKeepsDistinctPathsForSameState(t *testing.T) {
	archive := &pvfArchive{files: map[string]*pvfFile{
		"sqr/character/new_mage_load_state.nut": {
			Data: []byte(`
IRDSQRCharacter.pushState(ENUM_CHARACTERJOB_MAGE, "character/mage/a.nut", "a", 44, 7);
IRDSQRCharacter.pushState(ENUM_CHARACTERJOB_MAGE, "character/mage/b.nut", "b", 44, 7);
`),
		},
		"sqr/character/mage/a.nut": {Data: []byte("function a() { return true; }")},
		"sqr/character/mage/b.nut": {Data: []byte("function b() { return true; }")},
	}}

	want := []SkillState{
		{Job: 3, SkillIndex: 7, State: 44, ScriptPath: "sqr/character/mage/a.nut"},
		{Job: 3, SkillIndex: 7, State: 44, ScriptPath: "sqr/character/mage/b.nut"},
	}
	if got := extractSkillStateCatalog(archive); !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicate state paths = %+v, want %+v", got, want)
	}
}

func TestExtractSkillStateCatalogRejectsMissingAndInvalidReferences(t *testing.T) {
	archive := &pvfArchive{files: map[string]*pvfFile{
		"sqr/character/new_swordman_load_state.nut": {
			Data: []byte(`
IRDSQRCharacter.pushState(0, "character/swordman/valid.nut", "valid", 13, 18);
IRDSQRCharacter.pushState(0, "character/swordman/missing.nut", "missing", 14, 19);
IRDSQRCharacter.pushState(0, "character/swordman/invalid.nut", "invalid", 300, 0);
`),
		},
		"sqr/character/swordman/valid.nut":   {Data: []byte("function valid() { return true; }")},
		"sqr/character/swordman/invalid.nut": {Data: []byte("function invalid() { return true; }")},
	}}

	want := []SkillState{{Job: 0, SkillIndex: 18, State: 13, ScriptPath: "sqr/character/swordman/valid.nut"}}
	if got := extractSkillStateCatalog(archive); !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered skill states = %+v, want %+v", got, want)
	}
}

func TestSkillStatesForJobReturnsCopy(t *testing.T) {
	setSkillStateCatalog([]SkillState{{Job: 1, SkillIndex: 2, State: 3}, {Job: 2, SkillIndex: 4, State: 5}})
	got := shared.SkillStatesForJob(1)
	if len(got) != 1 || got[0].SkillIndex != 2 {
		t.Fatalf("job catalog = %+v", got)
	}
	got[0].SkillIndex = 99
	again := shared.SkillStatesForJob(1)
	if len(again) != 1 || again[0].SkillIndex != 2 {
		t.Fatalf("catalog mutated through returned slice: %+v", again)
	}
}
