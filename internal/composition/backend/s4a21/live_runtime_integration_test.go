package s4a21

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

func TestLiveS4A21RuntimeActions(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" {
		t.Skip("S4A21_TEST_ADDR is not set")
	}
	count := 2
	if raw := os.Getenv("S4A21_TEST_SESSION_COUNT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 50 {
			t.Fatalf("invalid S4A21_TEST_SESSION_COUNT=%q", raw)
		}
		count = parsed
	}
	prefix := "live" + strconv.FormatInt(time.Now().UnixNano()%1000000, 10)
	store, err := robotstate.OpenFileStore(filepath.Join(t.TempDir(), "robot_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	creator := RobotCreator{
		Provisioner: Provisioner{Address: address, Timeout: 20 * time.Second},
		BatchStore:  store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 0, SpawnXMin: 400, SpawnXMax: 500, SpawnYMin: 200, SpawnYMax: 260,
			NameASCIIFallback: true, NameASCIIPrefix: prefix,
		},
		Names: robottemplate.NameTemplates{}, AccountPrefix: prefix, IDStart: 19500000,
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: count})
	if err != nil {
		t.Fatal(err)
	}
	factory := SessionFactory{Address: address, Timeout: 20 * time.Second}
	transport := NewActionTransport(factory)
	for _, robot := range robots {
		if err := transport.Open(context.Background(), robot.UID, shared.OpenSessionRequest{AccountName: fmt.Sprintf("%s%d", prefix, robot.UID), CharacterSlot: 0}); err != nil {
			_ = transport.CloseAll()
			t.Fatalf("open uid=%d: %v", robot.UID, err)
		}
	}
	defer transport.CloseAll()
	for _, robot := range robots {
		if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: robot.UID, Village: robot.Village, Area: robot.Area, X: robot.X + 20, Y: robot.Y + 10, MoveType: 5, Speed: 100}); err != nil {
			t.Fatalf("move uid=%d: %v", robot.UID, err)
		}
		if err := transport.ShoutLocal(context.Background(), shared.RuntimeShoutCommand{UID: robot.UID, Message: "live runtime test"}); err != nil {
			t.Fatalf("shout uid=%d: %v", robot.UID, err)
		}
	}
	statuses := transport.RuntimeStatusMap()
	for _, robot := range robots {
		status := statuses[robot.UID]
		if status.X != robot.X+20 || status.Y != robot.Y+10 || status.Village != robot.Village || status.Area != robot.Area {
			t.Fatalf("status uid=%d = %+v", robot.UID, status)
		}
	}
}
