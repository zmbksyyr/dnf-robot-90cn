package robotaction

import (
	"errors"
	"strings"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type directLogoutEnv struct {
	selectCalls   int
	statusCalls   int
	freshCalls    int
	logoutUIDs    []int
	invalidated   []int
	invalidateErr error
	events        []string
	freshStatus   map[int]robotcap.RuntimeStatus
}

func (*directLogoutEnv) CountRuntimeRunning() int                    { return 0 }
func (*directLogoutEnv) EnsureWorldHornByCID(int) error              { return nil }
func (*directLogoutEnv) RobotConnectIP() string                      { return "127.0.0.1" }
func (*directLogoutEnv) RobotInnerIP() string                        { return "10.0.0.1" }
func (*directLogoutEnv) RobotGamePort() int                          { return 20011 }
func (*directLogoutEnv) SendOnline([]shared.RuntimeOnlineUser) error { return nil }

func TestOnlinePayloadSeparatesGameAndLoginIPs(t *testing.T) {
	service := SessionService{Env: &directLogoutEnv{}}
	payload := service.onlinePayload(robotcap.Info{UID: 17000001, CID: 900001}, 0, robotconfig.RuntimeConfig{})
	if payload.IP != "127.0.0.1" || payload.LoginIP != "10.0.0.1" {
		t.Fatalf("game IP=%q login IP=%q", payload.IP, payload.LoginIP)
	}
}

func (e *directLogoutEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	e.statusCalls++
	return map[int]robotcap.RuntimeStatus{17000001: {UID: 17000001}}
}

func (e *directLogoutEnv) RuntimeStatusMapFresh() map[int]robotcap.RuntimeStatus {
	e.freshCalls++
	e.events = append(e.events, "status")
	return e.freshStatus
}

func (e *directLogoutEnv) SelectRobots(robotcap.CommandRequest) ([]robotcap.Info, error) {
	e.selectCalls++
	return nil, nil
}

func (e *directLogoutEnv) SendLogout(uid int) error {
	e.logoutUIDs = append(e.logoutUIDs, uid)
	e.events = append(e.events, "logout")
	return nil
}

func (e *directLogoutEnv) InvalidateCharacterCache(uid int) error {
	e.invalidated = append(e.invalidated, uid)
	e.events = append(e.events, "invalidate")
	return e.invalidateErr
}

func TestLogoutUIDBypassesRepositoryAndCachedStatus(t *testing.T) {
	env := &directLogoutEnv{}
	result, err := (SessionService{Env: env}).LogoutUID(17000001)
	if err != nil {
		t.Fatal(err)
	}
	if env.selectCalls != 0 {
		t.Fatalf("direct logout selected robots %d times", env.selectCalls)
	}
	if env.statusCalls != 0 || env.freshCalls != 1 {
		t.Fatalf("status calls cached=%d fresh=%d", env.statusCalls, env.freshCalls)
	}
	if len(env.logoutUIDs) != 1 || env.logoutUIDs[0] != 17000001 {
		t.Fatalf("logout uids = %v", env.logoutUIDs)
	}
	if len(env.invalidated) != 1 || env.invalidated[0] != 17000001 {
		t.Fatalf("invalidated uids = %v", env.invalidated)
	}
	if got, want := strings.Join(env.events, ","), "logout,status,invalidate"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if result.Confirmed != 1 || len(result.Robots) != 1 || !result.Robots[0].OK || result.Robots[0].State != robotcap.ActionStateClosed {
		t.Fatalf("logout result = %+v", result)
	}
}

func TestLogoutUIDKeepsClosedSessionUnconfirmedWhenInvalidationFails(t *testing.T) {
	env := &directLogoutEnv{invalidateErr: errors.New("game udp unavailable")}
	result, err := (SessionService{Env: env}).LogoutUID(17000001)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Confirmed != 0 || result.Failed != 1 || len(result.Robots) != 1 {
		t.Fatalf("logout result = %+v", result)
	}
	item := result.Robots[0]
	if item.OK || item.State != robotcap.ActionStateFailed || !strings.Contains(item.Message, "game udp unavailable") {
		t.Fatalf("logout item = %+v", item)
	}
}

func TestLogoutUIDDoesNotInvalidateWhileRuntimeStillExists(t *testing.T) {
	env := &directLogoutEnv{
		freshStatus: map[int]robotcap.RuntimeStatus{17000001: {UID: 17000001, StateName: shared.RuntimeStateRunning}},
	}
	result, err := (SessionService{Env: env}).LogoutUID(17000001)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.invalidated) != 0 {
		t.Fatalf("invalidated uids = %v, want none", env.invalidated)
	}
	if got, want := strings.Join(env.events, ","), "logout,status"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if result.Accepted != 1 || result.Confirmed != 0 || result.Failed != 0 || len(result.Robots) != 1 {
		t.Fatalf("logout result = %+v", result)
	}
	if result.Robots[0].State != robotcap.ActionStatePending {
		t.Fatalf("logout item = %+v", result.Robots[0])
	}
}

func TestLogoutUIDConfirmsStoppedRuntimeEntry(t *testing.T) {
	// S4A21 keeps a "stop" entry after Close instead of deleting the UID. A
	// stopped runtime is not a live connection, so logout must be confirmed.
	env := &directLogoutEnv{
		freshStatus: map[int]robotcap.RuntimeStatus{17000001: {UID: 17000001, StateName: shared.RuntimeStateStop}},
	}
	result, err := (SessionService{Env: env}).LogoutUID(17000001)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Confirmed != 1 || result.Failed != 0 {
		t.Fatalf("logout result = %+v", result)
	}
	if !result.Robots[0].OK || result.Robots[0].State != robotcap.ActionStateClosed {
		t.Fatalf("logout item = %+v", result.Robots[0])
	}
}
