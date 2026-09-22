package s4a21

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
)

func TestLiveRobotCreatorThroughProtocol(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	pvfPath := os.Getenv("S4A21_TEST_PVF")
	if address == "" || pvfPath == "" {
		t.Skip("S4A21_TEST_ADDR and S4A21_TEST_PVF are not set")
	}
	maps, err := (TownMapCatalogProvider{PVFPath: pvfPath}).TownMapCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store, err := robotstate.OpenFileStore(filepath.Join(t.TempDir(), "robot_state.json"))
	if err != nil {
		t.Fatal(err)
	}
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
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(robots) != 2 {
		t.Fatalf("created %d robots, want 2", len(robots))
	}
	selected, err := store.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 2})
	if err != nil || len(selected) != 2 {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
}
