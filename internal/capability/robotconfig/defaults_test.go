package robotconfig

import "testing"

func TestDefaultOnlineCapacitySupportsLargeSimulatorPopulations(t *testing.T) {
	rc := Default()
	if rc.MaxOnlineRobots != 10000 || rc.MaxOnlinePerCommand != 1000 {
		t.Fatalf("online defaults = capacity %d command %d", rc.MaxOnlineRobots, rc.MaxOnlinePerCommand)
	}
}

func TestDefaultConfigAcceptsTwoThousandOnlineTarget(t *testing.T) {
	rc, err := Parse("[auto]\nauto_target_online_count = 2000\n")
	if err != nil {
		t.Fatal(err)
	}
	if rc.AutoTargetOnlineCount != 2000 || rc.MaxOnlineRobots != 10000 {
		t.Fatalf("online config = target %d capacity %d", rc.AutoTargetOnlineCount, rc.MaxOnlineRobots)
	}
}

func TestNormalizeKeepsPVFDefinedVillageAboveLegacyRange(t *testing.T) {
	rc := Default()
	rc.SpawnVillage = 26
	Normalize(&rc)
	if rc.SpawnVillage != 26 {
		t.Fatalf("spawn village=%d, want 26", rc.SpawnVillage)
	}
}

func TestNormalizeStoreEquipmentIntensifyRange(t *testing.T) {
	rc := Default()
	rc.StoreEquipmentIntensifyMin = 40
	rc.StoreEquipmentIntensifyMax = 6
	Normalize(&rc)
	if rc.StoreEquipmentIntensifyMin != 6 || rc.StoreEquipmentIntensifyMax != 31 {
		t.Fatalf("store equipment intensify=%d..%d, want 6..31", rc.StoreEquipmentIntensifyMin, rc.StoreEquipmentIntensifyMax)
	}
}

func TestNormalizeStoreEquipmentPriceWeights(t *testing.T) {
	rc := Default()
	rc.StoreEquipmentLevelWeight = -1
	rc.StoreEquipmentRarityWeight = 8
	rc.StoreEquipmentIntensifyWeight = 5
	Normalize(&rc)
	if rc.StoreEquipmentLevelWeight != 0 || rc.StoreEquipmentRarityWeight != 8 || rc.StoreEquipmentIntensifyWeight != 5 {
		t.Fatalf("store equipment weights=%d/%d/%d", rc.StoreEquipmentLevelWeight, rc.StoreEquipmentRarityWeight, rc.StoreEquipmentIntensifyWeight)
	}
	rc.StoreEquipmentRarityWeight = 0
	rc.StoreEquipmentIntensifyWeight = 0
	Normalize(&rc)
	if rc.StoreEquipmentLevelWeight != 35 || rc.StoreEquipmentRarityWeight != 40 || rc.StoreEquipmentIntensifyWeight != 25 {
		t.Fatalf("default store equipment weights=%d/%d/%d", rc.StoreEquipmentLevelWeight, rc.StoreEquipmentRarityWeight, rc.StoreEquipmentIntensifyWeight)
	}
}

func TestDefaultPetLoadoutIsPartial(t *testing.T) {
	rc := Default()
	if !rc.PetEnabled || !rc.PetArtifactEnabled {
		t.Fatalf("pet defaults disabled: enabled=%t artifacts=%t", rc.PetEnabled, rc.PetArtifactEnabled)
	}
	if rc.PetProbabilityPercent != 80 {
		t.Fatalf("pet probability default = %d, want 80", rc.PetProbabilityPercent)
	}
	if len(rc.PetArtifactSlots) != 3 || rc.MinPetArtifactSlots != 1 || rc.MaxPetArtifactSlots != 2 {
		t.Fatalf("pet artifact defaults = slots %v range %d..%d", rc.PetArtifactSlots, rc.MinPetArtifactSlots, rc.MaxPetArtifactSlots)
	}
}

func TestNormalizeClampsPetProbability(t *testing.T) {
	rc := Default()
	rc.PetProbabilityPercent = -1
	Normalize(&rc)
	if rc.PetProbabilityPercent != 0 {
		t.Fatalf("negative pet probability normalized to %d, want 0", rc.PetProbabilityPercent)
	}
	rc.PetProbabilityPercent = 101
	Normalize(&rc)
	if rc.PetProbabilityPercent != 100 {
		t.Fatalf("excess pet probability normalized to %d, want 100", rc.PetProbabilityPercent)
	}
}
