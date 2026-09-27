package pvf

import (
	"reflect"
	"testing"
)

func TestProjectQuestGatesMergesWorldMapsAndDungeonConnections(t *testing.T) {
	archive := testTextArchive{
		"worldmap/worldmap.lst":       "1 `Granfloris.wdm`",
		"worldmap/granfloris.wdm":     "[dungeon]\r\n101 1790\r\n102 [in progress] 1791\r\n103 1792\r\n104 [in\r\nprogress] 1793\r\n[/dungeon]\r\n",
		"dungeon/dungeon.lst":         "101 `granfloris/act1.dgn` 102 `quest/epic.dgn`",
		"dungeon/granfloris/act1.dgn": "[name] `x`\r\n[quest connection]\r\n1 1790 0\r\n[maze info]\r\n[quest connection]\r\n0 501 2\r\n",
		"dungeon/quest/epic.dgn":      "[quest connection]\r\n0 500 0\r\n",
	}
	gates := ProjectQuestGates(archive)
	// 1790 is both persistent and an active connection, so active wins.
	wantActive := []int{500, 501, 1790, 1791, 1793}
	wantCompleted := []int{1792}
	if !reflect.DeepEqual(gates.ActiveQuestIDs, wantActive) {
		t.Fatalf("active quests=%v want %v", gates.ActiveQuestIDs, wantActive)
	}
	if !reflect.DeepEqual(gates.CompletedQuestIDs, wantCompleted) {
		t.Fatalf("completed quests=%v want %v", gates.CompletedQuestIDs, wantCompleted)
	}
}

func TestProjectQuestGatesAddsReferenceQuestsForUnconnectedQuestDungeons(t *testing.T) {
	archive := testTextArchive{
		"worldmap/worldmap.lst":   "1 `Granfloris.wdm`",
		"worldmap/granfloris.wdm": "[dungeon]\r\n101 1790\r\n[/dungeon]\r\n",
		"dungeon/dungeon.lst": "201 `quest/epic.dgn` 202 `quest/connected.dgn` " +
			"203 `granfloris/act1.dgn` 204 `dungeon/quest/deep.dgn`",
		"dungeon/quest/epic.dgn":      "[name] `epic`\r\n[map info]\r\n",
		"dungeon/quest/connected.dgn": "[quest connection]\r\n0 700 0\r\n",
		"dungeon/granfloris/act1.dgn": "[name] `act1`\r\n",
		"dungeon/quest/deep.dgn":      "[name] `deep`\r\n",
		"n_quest/quest.lst":           "100 `epic/step1.qst` 101 `deep/step1.qst` 102 `unrelated.qst`",
		"n_quest/epic/step1.qst":      "[dungeon info]\r\n201 0\r\n[/dungeon info]\r\n",
		"n_quest/deep/step1.qst":      "[dungeon info]\r\n204 -1\r\n",
		"n_quest/unrelated.qst":       "[dungeon info]\r\n999 0\r\n",
	}
	gates := ProjectQuestGates(archive)
	// 201 and 204 are quest-asset dungeons without a connection; 202 keeps its
	// connection quest. 1790 stays a completed persistent world map gate.
	wantActive := []int{100, 101, 700}
	if !reflect.DeepEqual(gates.ActiveQuestIDs, wantActive) {
		t.Fatalf("active quests=%v want %v", gates.ActiveQuestIDs, wantActive)
	}
	if !reflect.DeepEqual(gates.CompletedQuestIDs, []int{1790}) {
		t.Fatalf("completed quests=%v", gates.CompletedQuestIDs)
	}
}

func TestProjectQuestGatesHandlesMissingData(t *testing.T) {
	if gates := ProjectQuestGates(nil); !gates.Empty() {
		t.Fatalf("nil archive gates=%+v", gates)
	}
	gates := ProjectQuestGates(testTextArchive{"worldmap/worldmap.lst": "1 `Missing.wdm`"})
	if !gates.Empty() {
		t.Fatalf("missing wdm gates=%+v", gates)
	}
}
