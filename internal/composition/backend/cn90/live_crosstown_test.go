package cn90

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// TestLiveCrossTownSpawn proves whether a robot can stand in a town other than
// its home town. It logs in at the home location, requests a foreign town with
// the portal request shape and checks the server's commit.
func TestLiveCrossTownSpawn(t *testing.T) {
	if os.Getenv("CN90_LIVE_CROSSTOWN") == "" {
		t.Skip("set CN90_LIVE_CROSSTOWN=1 to run the cross-town spawn probe")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	adminAddress, adminToken, err := serverLayout.adminEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	channels, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !liveListener(t, channels) {
		t.Skipf("no live 90CN game listener among %v", channels.ports)
	}
	pvfPath, _, err := serverLayout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	const uid = 17009999
	const account = "robot17009999"
	const characterName = "跨城镇测试"
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	defer func() {
		purger := SQLiteRobotPurger{DatabasePath: databasePath, AccountPrefix: "robot"}
		request := robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeRange, MinUID: uid, MaxUID: uid}
		if plan, err := purger.PlanDangerousDelete(context.Background(), request); err == nil && (plan.AccountCount > 0 || plan.CharacterCount > 0) {
			if _, err := purger.ExecuteDangerousDelete(context.Background(), plan); err == nil {
				t.Logf("test account purged: accounts=%d characters=%d", plan.AccountCount, plan.CharacterCount)
			}
		}
	}()

	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}
	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: characterName, Job: 2, RobotUID: uid,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	applier, err := NewSQLiteLoadoutApplier(ctx, databasePath, robotconfig.Default(), catalogs.Equipment, pvfPath, nil)
	if err != nil {
		t.Fatalf("loadout applier: %v", err)
	}
	defer applier.Close()
	applier.LevelThresholds = catalogs.LevelThresholds
	applier.QuestGates = mergeTownNeedQuests(catalogs.QuestGates, catalogs.TownMaps)
	info := robotcap.Info{UID: uid, Name: result.CharacterName}
	leveled, err := applier.InitializeCharacter(ctx, account, info, 70, 0)
	if err != nil {
		t.Fatalf("initialize character: %v", err)
	}

	var target shared.MapCatalogItem
	for _, mp := range catalogs.TownMaps {
		if !mp.Use || mp.Village == 38 || mp.Level > 70 {
			continue
		}
		if mp.NormalEligible != nil && !*mp.NormalEligible {
			continue
		}
		if target.Village == 0 || (mp.Village == 3 && target.Village != 3) {
			target = mp
		}
	}
	if target.Village == 0 {
		t.Skip("no eligible foreign town in the catalog")
	}
	t.Logf("target foreign town: village=%d (%s) area=%d", target.Village, target.VillageName, target.Area)

	logPath := filepath.Join(root, projectRuntimeDirName, "logs", "packet_log.txt")
	var offset int64
	if fileInfo, err := os.Stat(logPath); err == nil {
		offset = fileInfo.Size()
	}

	// The home login route is pinned to town 38/area 1, which is also what a
	// freshly created character persists.
	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	session, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: 38, InitialArea: 1, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	liveSession, _ := session.(*Session)
	if liveSession == nil {
		t.Fatal("unexpected session type")
	}
	defer func() {
		exitCtx, cancelExit := context.WithTimeout(context.Background(), 5*time.Second)
		_ = liveSession.client.Exit(exitCtx)
		cancelExit()
		_ = liveSession.Close()
	}()

	// Cross-town transition with the portal request shape.
	targetX, targetY := int16(target.XMin+30), int16(target.YMin+30)
	moveCtx, cancelMove := context.WithTimeout(ctx, 20*time.Second)
	moveErr := liveSession.MoveTownArea(moveCtx, shared.TownAreaMoveIntent{
		Village: target.Village, Area: target.Area, X: targetX, Y: targetY, SourceVillage: 38,
	})
	cancelMove()
	if moveErr != nil {
		t.Logf("portal move reported: %v", moveErr)
	}
	time.Sleep(2 * time.Second)

	data, readErr := os.ReadFile(logPath)
	if readErr != nil || int64(len(data)) <= offset {
		t.Fatalf("packet log unreadable: %v", readErr)
	}
	tail := string(data[offset:])
	committed := false
	blocked := ""
	for _, line := range strings.Split(tail, "\n") {
		if !strings.Contains(line, "char_id="+strconv.Itoa(leveled.CID)) {
			continue
		}
		if strings.Contains(line, "town-set-user-area-committed") && strings.Contains(line, "town_id="+strconv.Itoa(target.Village)) {
			committed = true
		}
		if strings.Contains(line, "town-set-user-area-blocked") && blocked == "" {
			blocked = shortenLine(line, 240)
		}
	}
	switch {
	case committed && moveErr == nil:
		t.Logf("CROSS-TOWN RESULT: ACCEPTED with the portal shape: robot moved to town=%d (%s) area=%d", target.Village, target.VillageName, target.Area)
	case committed:
		t.Logf("CROSS-TOWN RESULT: committed despite client error %v (town=%d area=%d)", moveErr, target.Village, target.Area)
	default:
		if blocked != "" {
			t.Logf("CROSS-TOWN RESULT: BLOCKED town=%d; server line: %s", target.Village, blocked)
		} else {
			t.Logf("CROSS-TOWN RESULT: NO COMMIT for town=%d area=%d", target.Village, target.Area)
		}
		if moveErr != nil {
			t.Logf("client move error: %v", moveErr)
		}
	}
}

func shortenLine(line string, limit int) string {
	line = strings.TrimSpace(line)
	if len(line) <= limit {
		return line
	}
	return line[:limit]
}
