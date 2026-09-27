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

func TestProjectQuestGatesHandlesMissingData(t *testing.T) {
	if gates := ProjectQuestGates(nil); !gates.Empty() {
		t.Fatalf("nil archive gates=%+v", gates)
	}
	gates := ProjectQuestGates(testTextArchive{"worldmap/worldmap.lst": "1 `Missing.wdm`"})
	if !gates.Empty() {
		t.Fatalf("missing wdm gates=%+v", gates)
	}
}
