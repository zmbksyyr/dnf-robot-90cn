package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/shared"
)

type backendSessionStub struct {
	opened      []shared.OpenSessionRequest
	closed      []int
	closeErrors []error
}

func (s *backendSessionStub) Open(_ context.Context, uid int, request shared.OpenSessionRequest) error {
	s.opened = append(s.opened, request)
	_ = uid
	return nil
}

func (s *backendSessionStub) Close(uid int) error {
	s.closed = append(s.closed, uid)
	if len(s.closeErrors) > 0 {
		err := s.closeErrors[0]
		s.closeErrors = s.closeErrors[1:]
		return err
	}
	return nil
}

func TestRobotRuntimeForceCloseUsesBackendSessionTransport(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	backend := &backendSessionStub{closeErrors: []error{errors.New("socket close failed")}}
	m.SetBackendSessionTransport(backend)
	runtime := NewRobotRuntime(m)

	if runtime.ForceClose(17000001) {
		t.Fatal("first backend close error was reported as success")
	}
	if !runtime.ForceClose(17000001) {
		t.Fatal("idempotent backend close retry did not confirm release")
	}
	if len(backend.closed) != 2 || backend.closed[0] != 17000001 || backend.closed[1] != 17000001 {
		t.Fatalf("backend close calls=%v", backend.closed)
	}
}

type offlineSessionRepository struct {
	missingSchemaRepository
	calls int
}

func (r *offlineSessionRepository) AccountOnline(int) (bool, error) {
	r.calls++
	return false, nil
}

func (*offlineSessionRepository) Stats() sql.DBStats { return sql.DBStats{} }

func (*offlineSessionRepository) PingContext(context.Context) error { return nil }

func (*offlineSessionRepository) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return &sql.Row{}
}

func TestSessionReloginWaitsForSameUID(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = 40 * time.Millisecond
	m.markSessionLogout(17000001, time.Now())

	started := time.Now()
	m.waitSessionRelogin([]shared.RuntimeOnlineUser{{UID: 17000001}})
	if elapsed := time.Since(started); elapsed < 30*time.Millisecond {
		t.Fatalf("same uid relogin waited %s, want at least 30ms", elapsed)
	}
}

func TestBackendSessionTransportBypassesNativeOnlinePath(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	backend := &backendSessionStub{}
	m.SetBackendSessionTransport(backend)
	users := []shared.RuntimeOnlineUser{{
		UID: 17000001, AccountName: "acct", PasswordHash: "hash", CharacterSlot: 2,
		BirthVillage: 3, BirthArea: 4, BirthX: 120, BirthY: 240,
	}}
	if err := (sessionActionEnv{manager: m}).SendOnline(users); err != nil {
		t.Fatal(err)
	}
	if len(backend.opened) != 1 || backend.opened[0].AccountName != "acct" || backend.opened[0].CharacterSlot != 2 {
		t.Fatalf("opened=%+v", backend.opened)
	}
	opened := backend.opened[0]
	if !opened.InitialTownKnown || opened.InitialVillage != 3 || opened.InitialArea != 4 || opened.InitialX != 120 || opened.InitialY != 240 {
		t.Fatalf("initial town metadata=%+v", opened)
	}
	if err := (sessionActionEnv{manager: m}).SendLogout(users[0].UID); err != nil {
		t.Fatal(err)
	}
	if len(backend.closed) != 1 || backend.closed[0] != users[0].UID {
		t.Fatalf("closed=%v", backend.closed)
	}
}

func TestSimulatorOnlineReappliesFixedTownToExistingRobot(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendSessionTransport(&backendSessionStub{})
	m.SetTownMapCatalog([]shared.MapCatalogItem{{
		Village: 3, Area: 4, Level: 1, Use: true,
		Rectangles: []shared.MapRectangle{{XMin: 400, XMax: 500, YMin: 200, YMax: 260}},
	}})
	rc := m.loadRobotConfig()
	rc.SpawnFixed = true
	rc.SpawnVillage = 3
	rc.SpawnArea = 4
	rc.SpawnXMin, rc.SpawnXMax = 400, 500
	rc.SpawnYMin, rc.SpawnYMax = 200, 260
	prepared, err := (sessionActionEnv{manager: m}).PrepareOnlineRobot(
		robotcap.Info{UID: 17000001, Level: 85, Village: 1, Area: 0, X: 10, Y: 20}, rc)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Village != 3 || prepared.Area != 4 || prepared.X < 400 || prepared.X > 500 || prepared.Y < 200 || prepared.Y > 260 {
		t.Fatalf("prepared fixed town = %+v", prepared)
	}
}

func TestBackendSessionTransportOptsS4A21FollowersFromFollowAccount(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[follow]\nfollow_account = leader\n")
	backend := &backendSessionStub{}
	m.SetBackendSessionTransport(backend)
	if err := (sessionActionEnv{manager: m}).SendOnline([]shared.RuntimeOnlineUser{{
		UID: 17000001, AccountName: "acct", PasswordHash: "hash",
	}}); err != nil {
		t.Fatal(err)
	}
	if len(backend.opened) != 1 || !backend.opened[0].EnablePartyDungeonFollower {
		t.Fatalf("opened=%+v, want explicit dungeon follower", backend.opened)
	}
}

func TestBackendSessionTransportRequiresAccount(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	backend := &backendSessionStub{}
	m.SetBackendSessionTransport(backend)
	if err := (sessionActionEnv{manager: m}).SendOnline([]shared.RuntimeOnlineUser{{UID: 17000001}}); err == nil {
		t.Fatal("missing backend account unexpectedly succeeded")
	}
}

func TestBackendSessionTransportResolvesRobotOwnedIdentity(t *testing.T) {
	store := robotstate.NewMemoryStore([]robotcap.Info{{UID: 17000001, Name: "sim-robot"}})
	slot := uint16(3)
	if err := store.RegisterIdentity(context.Background(), robotstate.Identity{
		Backend: shared.BackendS4A21, Account: "robot17000001", CharacterName: "sim-robot", Slot: &slot,
	}); err != nil {
		t.Fatal(err)
	}
	m := testRobotManagerWithConfig(t, "")
	m.SetRobotStateDirectory(store)
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	backend := &backendSessionStub{}
	m.SetBackendSessionTransport(backend)
	if err := (sessionActionEnv{manager: m}).SendOnline([]shared.RuntimeOnlineUser{{UID: 17000001}}); err != nil {
		t.Fatal(err)
	}
	if len(backend.opened) != 1 || backend.opened[0].AccountName != "robot17000001" || backend.opened[0].CharacterSlot != slot {
		t.Fatalf("opened=%+v", backend.opened)
	}
}

func TestSessionReloginDoesNotDelayOtherUID(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = time.Second
	m.markSessionLogout(17000001, time.Now())

	started := time.Now()
	m.waitSessionRelogin([]shared.RuntimeOnlineUser{{UID: 17000002}})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("unrelated uid relogin waited %s", elapsed)
	}
}

func TestGateAreaForVillageUsesCurrentPVFCatalog(t *testing.T) {
	maps := []shared.MapCatalogItem{
		{Village: 2, Area: 9, Use: true},
		{Village: 2, Area: 7, Use: true, Gate: true},
		{Village: 3, Area: 4, Use: false, Gate: true},
	}
	if area, ok := gateAreaForVillage(maps, 2); !ok || area != 7 {
		t.Fatalf("gate area=%d ok=%t, want current PVF area 7", area, ok)
	}
	if _, ok := gateAreaForVillage(maps, 3); ok {
		t.Fatal("unusable gate area was accepted")
	}
}

func TestSessionReleaseRemainingUsesLogoutSafetyWindow(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = 40 * time.Millisecond
	m.markSessionLogout(17000001, time.Now().Add(-sessionWriteSafetyMargin))

	if remaining := m.sessionReleaseRemaining(17000001); remaining <= 0 {
		t.Fatalf("remaining=%s, want active safety window", remaining)
	}
	time.Sleep(45 * time.Millisecond)
	if remaining := m.sessionReleaseRemaining(17000001); remaining != 0 {
		t.Fatalf("remaining=%s, want released session", remaining)
	}
}

func TestClosedSessionInvalidationClearsLogoutSafetyWindow(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = time.Second
	m.markSessionLogout(17000001, time.Now())
	m.characterCacheInvalidate = func(uid int) error {
		if uid != 17000001 {
			t.Fatalf("invalidate uid=%d", uid)
		}
		return nil
	}

	if err := m.invalidateClosedCharacterCache(17000001); err != nil {
		t.Fatal(err)
	}
	if remaining := m.sessionReleaseRemaining(17000001); remaining != 0 {
		t.Fatalf("remaining=%s, want cleared safety window", remaining)
	}
}

func TestClosedSessionInvalidationFailureKeepsLogoutSafetyWindow(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = time.Second
	m.markSessionLogout(17000001, time.Now())
	want := errors.New("game udp unavailable")
	m.characterCacheInvalidate = func(int) error { return want }

	if err := m.invalidateClosedCharacterCache(17000001); !errors.Is(err, want) {
		t.Fatalf("invalidate error=%v, want %v", err, want)
	}
	if remaining := m.sessionReleaseRemaining(17000001); remaining <= 0 {
		t.Fatalf("remaining=%s, want preserved safety window", remaining)
	}
}

func TestMarkSessionLogoutPrunesExpiredEntries(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = time.Second
	m.sessionLastLogout[17000001] = time.Now().Add(-time.Hour)
	m.markSessionLogout(17000002, time.Now())

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if _, ok := m.sessionLastLogout[17000001]; ok {
		t.Fatal("expired logout entry was not pruned")
	}
	if _, ok := m.sessionLastLogout[17000002]; !ok {
		t.Fatal("current logout entry was not retained")
	}
}

func TestMarkSessionLogoutRateLimitsExpiredEntryScans(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.sessionReloginDelay = time.Second
	now := time.Now()
	m.sessionLastLogout[17000001] = now.Add(-time.Hour)
	m.sessionLogoutCleanupAt = now.Add(time.Minute)

	m.markSessionLogout(17000002, now)
	if _, ok := m.sessionLastLogout[17000001]; !ok {
		t.Fatal("rate-limited mark unexpectedly scanned the logout table")
	}

	m.sessionLogoutCleanupAt = time.Time{}
	m.markSessionLogout(17000003, now)
	if _, ok := m.sessionLastLogout[17000001]; ok {
		t.Fatal("due cleanup did not prune the expired logout entry")
	}
	for _, uid := range []int{17000002, 17000003} {
		if _, ok := m.sessionLastLogout[uid]; !ok {
			t.Fatalf("current logout uid=%d was not retained", uid)
		}
	}
}

func BenchmarkMarkSessionLogoutRateLimited(b *testing.B) {
	now := time.Now()
	m := &RobotManager{
		sessionReloginDelay:    15 * time.Second,
		sessionLastLogout:      make(map[int]time.Time, 10_256),
		sessionLogoutCleanupAt: now.Add(time.Hour),
	}
	for uid := 1; uid <= 10_000; uid++ {
		m.sessionLastLogout[uid] = now.Add(-time.Hour)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.markSessionLogout(20_000+(i&255), now)
	}
}

func TestWaitAccountOfflineCompletesDelayedNoCacheBoundary(t *testing.T) {
	repo := &offlineSessionRepository{}
	m := testRobotManagerWithConfig(t, "")
	m.database = repo
	m.sessionReloginDelay = 10 * time.Second
	m.markSessionLogout(17000001, time.Now())
	invalidations := 0
	m.characterCacheInvalidate = func(uid int) error {
		if uid != 17000001 {
			t.Fatalf("invalidate uid=%d", uid)
		}
		invalidations++
		return nil
	}

	started := time.Now()
	cancelled, err := m.waitAccountOffline(17000001, nil)
	if err != nil || cancelled {
		t.Fatalf("waitAccountOffline cancelled=%v err=%v", cancelled, err)
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatalf("offline boundary took %s, want NoCache fast path", elapsed)
	}
	if invalidations != 1 || repo.calls < 2 {
		t.Fatalf("invalidations=%d account checks=%d, want one invalidation and stable offline confirmation", invalidations, repo.calls)
	}
	if remaining := m.sessionReleaseRemaining(17000001); remaining != 0 {
		t.Fatalf("remaining=%s, want cleared safety window", remaining)
	}
}

func TestWaitAccountOfflineDoesNotRepeatCompletedNoCacheBoundary(t *testing.T) {
	repo := &offlineSessionRepository{}
	m := testRobotManagerWithConfig(t, "")
	m.database = repo
	m.sessionReloginDelay = 10 * time.Second
	m.markSessionLogout(17000001, time.Now())
	invalidations := 0
	m.characterCacheInvalidate = func(uid int) error {
		if uid != 17000001 {
			t.Fatalf("invalidate uid=%d", uid)
		}
		invalidations++
		return nil
	}
	if err := m.invalidateClosedCharacterCache(17000001); err != nil {
		t.Fatal(err)
	}

	cancelled, err := m.waitAccountOffline(17000001, nil)
	if err != nil || cancelled {
		t.Fatalf("waitAccountOffline cancelled=%v err=%v", cancelled, err)
	}
	if invalidations != 1 || repo.calls < 2 {
		t.Fatalf("invalidations=%d account checks=%d, want no duplicate invalidation and stable offline confirmation", invalidations, repo.calls)
	}
}
