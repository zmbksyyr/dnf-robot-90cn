package cn90

import (
	"testing"

	"robot/internal/shared"
)

func TestApplyChannelSpawnPolicyMarksSpecialTownsIneligible(t *testing.T) {
	normal := true
	maps := []shared.MapCatalogItem{
		{Village: 2, Area: 0, Use: true, NormalEligible: &normal},
		{Village: channelPolicyPvpTownID, Area: 1, Use: true, NormalEligible: &normal},
		{Village: channelPolicyChannel100TownID, Area: 1, Use: true, NormalEligible: &normal},
	}
	out := ApplyChannelSpawnPolicy(maps)
	if out[0].NormalEligible == nil || !*out[0].NormalEligible {
		t.Fatal("regular town must stay spawn-eligible")
	}
	for _, index := range []int{1, 2} {
		if out[index].NormalEligible == nil || *out[index].NormalEligible {
			t.Fatalf("special town %d must be spawn-ineligible: %+v", out[index].Village, out[index])
		}
	}
	if maps[1].NormalEligible == nil || !*maps[1].NormalEligible {
		t.Fatal("input catalog must not be mutated")
	}
}
