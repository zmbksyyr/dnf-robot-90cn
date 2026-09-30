package cn90

import (
	"context"
	"os"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/charset"
	"robot/internal/shared"
)

// TestLiveExpertJobStoreTransport drives one complete stall lifecycle through
// the scheduler's transport port: open, verify the runtime status the
// scheduler waits for, hold, close and verify the status is cleared.
func TestLiveExpertJobStoreTransport(t *testing.T) {
	if os.Getenv("CN90_LIVE_STORE") == "" {
		t.Skip("set CN90_LIVE_STORE=1 to run the live expert-job store test")
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
	const account = "robot17000002"
	const characterName = "自动机器人乙"
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}
	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: characterName, Job: 2, RobotUID: 17000002,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !result.Created || result.BackendSlot == nil {
		t.Fatalf("provision result = %+v", result)
	}
	applier, err := NewSQLiteLoadoutApplier(ctx, databasePath, robotconfig.Default(), catalogs.Equipment, pvfPath, nil)
	if err != nil {
		t.Fatalf("loadout applier: %v", err)
	}
	defer applier.Close()
	applier.LevelThresholds = catalogs.LevelThresholds
	info := robotcap.Info{UID: 17000002, Name: result.CharacterName}
	leveled, err := applier.InitializeCharacter(ctx, account, info, 70, 0)
	if err != nil {
		t.Fatalf("initialize character: %v", err)
	}
	if err := applier.EnsureDisjointProfession(leveled.CID); err != nil {
		t.Fatalf("ensure disjointer profession: %v", err)
	}

	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	transport := NewActionTransport(factory)
	defer transport.CloseAll()
	if err := transport.Open(ctx, 17000002, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: 38, InitialArea: 1, InitialX: 450, InitialY: 234,
	}); err != nil {
		t.Fatalf("open transport session: %v", err)
	}
	if !transport.StartExpertJobStore(17000002, shared.ExpertJobStoreDisjoint, 500) {
		t.Fatal("transport refused to open the stall")
	}
	status := transport.RuntimeStatusMap()[17000002]
	if status.RobotType != 3 || !status.DisjointActive || !status.DisjointCreateSent || !status.DisjointDirectAck {
		t.Fatalf("store status after open = %+v", status)
	}
	t.Logf("stall open: robot_type=%d disjoint_active=%v store_created=%v", status.RobotType, status.DisjointActive, status.StoreCreated)

	// Hold the stall briefly so the server-side store is observably active.
	holdCtx, cancelHold := context.WithTimeout(ctx, 8*time.Second)
	<-holdCtx.Done()
	cancelHold()

	if !transport.CloseExpertJobStore(17000002) {
		t.Fatal("transport refused to close the stall")
	}
	status = transport.RuntimeStatusMap()[17000002]
	if status.RobotType != 0 || status.DisjointActive || status.DisjointCreateSent {
		t.Fatalf("store status after close = %+v", status)
	}
	t.Logf("stall closed: robot_type=%d disjoint_active=%v", status.RobotType, status.DisjointActive)

	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("post-clean: %v", err)
	}
}

// TestLiveExpertJobStore opens and closes a disassembler stall against the
// running DNF90 server. It is env-gated because it mutates the live database
// and requires a game listener.
func TestLiveExpertJobStore(t *testing.T) {
	if os.Getenv("CN90_LIVE_STORE") == "" {
		t.Skip("set CN90_LIVE_STORE=1 to run the live expert-job store test")
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
	const account = "robot17000002"
	const characterName = "自动机器人乙"
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}
	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: characterName, Job: 2, RobotUID: 17000002,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !result.Created || result.BackendSlot == nil {
		t.Fatalf("provision result = %+v", result)
	}
	applier, err := NewSQLiteLoadoutApplier(ctx, databasePath, robotconfig.Default(), catalogs.Equipment, pvfPath, nil)
	if err != nil {
		t.Fatalf("loadout applier: %v", err)
	}
	defer applier.Close()
	applier.LevelThresholds = catalogs.LevelThresholds
	info := robotcap.Info{UID: 17000002, Name: result.CharacterName}
	leveled, err := applier.InitializeCharacter(ctx, account, info, 70, 0)
	if err != nil {
		t.Fatalf("initialize character: %v", err)
	}
	if leveled.CID <= 0 {
		t.Fatalf("character id was not resolved: %+v", leveled)
	}
	if err := applier.EnsureDisjointProfession(leveled.CID); err != nil {
		t.Fatalf("ensure disjointer profession: %v", err)
	}
	ready, err := applier.DisjointProfessionReady(leveled.CID)
	if err != nil {
		t.Fatalf("disjointer profession probe: %v", err)
	}
	if !ready {
		t.Fatal("disjointer profession was not persisted")
	}
	t.Logf("disjointer profession ready cid=%d", leveled.CID)

	// The server reads the profession from the database at login time, so the
	// session must be opened after the offline write.
	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	session, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: 38, InitialArea: 1, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	liveSession, ok := session.(*Session)
	if !ok {
		t.Fatal("unexpected session type")
	}
	defer func() {
		exitCtx, cancelExit := context.WithTimeout(context.Background(), 5*time.Second)
		_ = liveSession.client.Exit(exitCtx)
		cancelExit()
		_ = liveSession.Close()
	}()
	name, err := charset.EncodeGBKString("分解机")
	if err != nil {
		t.Fatal(err)
	}
	storeCtx, cancelStore := context.WithTimeout(ctx, 20*time.Second)
	defer cancelStore()
	if err := liveSession.StartExpertJobStore(storeCtx, shared.ExpertJobStoreDisjoint, 500, name, 450, 234); err != nil {
		t.Fatalf("start expert store: %v", err)
	}
	t.Logf("expert store opened (disjoint) at 38/1 450,234")
	if err := liveSession.CloseExpertJobStore(storeCtx); err != nil {
		t.Fatalf("close expert store: %v", err)
	}
	t.Logf("expert store closed")

	// Clean up the test character.
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("post-clean: %v", err)
	}
}
