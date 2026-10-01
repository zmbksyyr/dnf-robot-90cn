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

	// Cross-town transitions with the portal request shape, chained through
	// several towns: home 38 -> target 3 -> 1 -> 2.
	foreign := make([]shared.MapCatalogItem, 0, 3)
	for _, village := range []int{target.Village, 1, 2} {
		for _, mp := range catalogs.TownMaps {
			if mp.Village != village || !mp.Use || mp.Level > 70 {
				continue
			}
			if mp.NormalEligible != nil && !*mp.NormalEligible {
				continue
			}
			foreign = append(foreign, mp)
			break
		}
	}
	sourceVillage := 38
	commits := make([]string, 0, len(foreign))
	for _, destination := range foreign {
		moveCtx, cancelMove := context.WithTimeout(ctx, 20*time.Second)
		moveErr := liveSession.MoveTownArea(moveCtx, shared.TownAreaMoveIntent{
			Village: destination.Village, Area: destination.Area,
			X: int16(destination.XMin + 30), Y: int16(destination.YMin + 30),
			SourceVillage: sourceVillage,
		})
		cancelMove()
		if moveErr != nil {
			t.Logf("portal move %d->%d reported: %v", sourceVillage, destination.Village, moveErr)
		}
		time.Sleep(2 * time.Second)
		sourceVillage = destination.Village
		commits = append(commits, strconv.Itoa(sourceVillage))
	}

	data, readErr := os.ReadFile(logPath)
	if readErr != nil || int64(len(data)) <= offset {
		t.Fatalf("packet log unreadable: %v", readErr)
	}
	tail := string(data[offset:])
	committedTowns := map[int]bool{}
	blocked := ""
	for _, line := range strings.Split(tail, "\n") {
		if !strings.Contains(line, "char_id="+strconv.Itoa(leveled.CID)) {
			continue
		}
		if strings.Contains(line, "town-set-user-area-committed") {
			for _, destination := range foreign {
				if strings.Contains(line, "town_id="+strconv.Itoa(destination.Village)) &&
					strings.Contains(line, "town_cross_town_portal_request=true") {
					committedTowns[destination.Village] = true
				}
			}
		}
		if strings.Contains(line, "town-set-user-area-blocked") && blocked == "" {
			blocked = shortenLine(line, 240)
		}
	}
	if len(committedTowns) == len(foreign) {
		t.Logf("CROSS-TOWN RESULT: ACCEPTED chained portal moves through towns %v", commits)
	} else if blocked != "" {
		t.Logf("CROSS-TOWN RESULT: BLOCKED at some town; server line: %s", blocked)
	} else {
		t.Logf("CROSS-TOWN RESULT: committed towns %v, expected %v (client errors above)", committedTowns, commits)
	}

	// Phase 2: assigned-town login. Close the session, point the character at a
	// different town offline and log in with the home town hint: the presence
	// init must use the portal shape so the robot appears in the assigned town
	// without a separate move. The server now considers the last committed
	// town (the final chained destination) as current.
	liveSession.client.Close()
	time.Sleep(2 * time.Second)
	assigned := shared.MapCatalogItem{}
	for _, village := range []int{1, 3, 2} {
		for _, mp := range catalogs.TownMaps {
			if mp.Village != village || !mp.Use || mp.Level > 70 {
				continue
			}
			if mp.NormalEligible != nil && !*mp.NormalEligible {
				continue
			}
			assigned = mp
			break
		}
		if assigned.Village != 0 {
			break
		}
	}
	if assigned.Village == 0 {
		t.Fatal("no assigned town candidate")
	}
	assignedX, assignedY := assigned.XMin+30, assigned.YMin+30
	db := openPurgeTestDatabase(t, databasePath)
	if _, err := db.Exec(`UPDATE dnf_characters SET town_id=?, area_id=?, pos_x=?, pos_y=? WHERE character_id=?`,
		assigned.Village, assigned.Area, assignedX, assignedY, leveled.CID); err != nil {
		t.Fatalf("write assigned location: %v", err)
	}
	db.Close()
	var assignOffset int64
	if fileInfo, err := os.Stat(logPath); err == nil {
		assignOffset = fileInfo.Size()
	}
	assignedSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: assigned.Village, InitialArea: assigned.Area,
		InitialX: assignedX, InitialY: assignedY, HomeVillage: 38,
	})
	if err != nil {
		t.Fatalf("open assigned-town session: %v", err)
	}
	if assignSession, ok := assignedSession.(*Session); ok {
		time.Sleep(3 * time.Second)
		exitCtx, cancelExit := context.WithTimeout(context.Background(), 5*time.Second)
		_ = assignSession.client.Exit(exitCtx)
		cancelExit()
		_ = assignSession.Close()
	}
	assignData, assignErr := os.ReadFile(logPath)
	if assignErr != nil || int64(len(assignData)) <= assignOffset {
		t.Fatalf("packet log unreadable after assigned login: %v", assignErr)
	}
	assignedTail := string(assignData[assignOffset:])
	loginCommitted := false
	for _, line := range strings.Split(assignedTail, "\n") {
		if !strings.Contains(line, "char_id="+strconv.Itoa(leveled.CID)) {
			continue
		}
		if strings.Contains(line, "town-set-user-area-committed") &&
			strings.Contains(line, "town_id="+strconv.Itoa(assigned.Village)) &&
			strings.Contains(line, "town_cross_town_portal_request=true") {
			loginCommitted = true
		}
	}
	if loginCommitted {
		t.Logf("ASSIGNED-TOWN LOGIN RESULT: ACCEPTED: presence landed directly in town=%d (%s) area=%d", assigned.Village, assigned.VillageName, assigned.Area)
	} else {
		t.Errorf("ASSIGNED-TOWN LOGIN RESULT: no portal commit for town=%d area=%d", assigned.Village, assigned.Area)
	}
}

func shortenLine(line string, limit int) string {
	line = strings.TrimSpace(line)
	if len(line) <= limit {
		return line
	}
	return line[:limit]
}
