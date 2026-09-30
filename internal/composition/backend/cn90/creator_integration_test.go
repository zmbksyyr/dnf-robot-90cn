package cn90

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
)

func TestLiveRobotCreatorThroughProtocol(t *testing.T) {
	address := os.Getenv("CN90_TEST_ADDR")
	pvfPath := os.Getenv("CN90_TEST_PVF")
	if address == "" || pvfPath == "" {
		t.Skip("CN90_TEST_ADDR and CN90_TEST_PVF are not set")
	}
	maps, err := (TownMapCatalogProvider{PVFPath: pvfPath}).TownMapCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := robotstate.NewMemoryStore(nil)
	prefix := "r" + time.Now().Format("150405")
	creator := RobotCreator{
		Provisioner: Provisioner{Address: address, Timeout: 20 * time.Second},
		BatchStore:  store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: -1, SpawnXMin: 400, SpawnXMax: 500, SpawnYMin: 200, SpawnYMax: 260,
			NameASCIIFallback: true, NameASCIIPrefix: prefix,
		},
		Names: robottemplate.NameTemplates{}, Maps: maps, AccountPrefix: prefix, IDStart: 19000000,
	}
	count := 2
	if raw := os.Getenv("CN90_TEST_COUNT"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed <= 0 || parsed > 1000 {
			t.Fatalf("invalid CN90_TEST_COUNT=%q", raw)
		}
		count = parsed
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: count})
	if err != nil {
		t.Fatal(err)
	}
	if len(robots) != count {
		t.Fatalf("created %d robots, want %d", len(robots), count)
	}
	selected, err := store.SelectRobots(context.Background(), robotcap.CommandRequest{Count: count})
	if err != nil || len(selected) != count {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
}
