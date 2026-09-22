package scheduler

import (
	robotcap "robot/internal/capability/robot"
	robotaction "robot/internal/capability/robotaction"
	robotconfig "robot/internal/capability/robotconfig"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

func (m *RobotManager) shoutService() robotaction.ShoutService {
	return robotaction.ShoutService{Env: shoutActionEnv{manager: m}}
}

type shoutActionEnv struct {
	manager *RobotManager
}

func (e shoutActionEnv) AppendShout(uid int, channel string, typ int, msg string) error {
	return e.AppendShoutDetail(uid, channel, typ, msg, "normal")
}

func (e shoutActionEnv) AppendShoutDetail(uid int, channel string, typ int, msg, source string) error {
	if channel == "" {
		channel = "world"
	}
	_ = msg
	robotLogf("[Shout] source=%s uid=%d channel=%s type=%d\n", source, uid, channel, typ)
	return nil
}

func (e shoutActionEnv) Config() robotconfig.RuntimeConfig {
	return e.manager.loadRobotConfig()
}

func (e shoutActionEnv) LookupRobotName(uid int) string {
	name, _ := e.manager.schemaRepo().RobotCharacterName(uid)
	return name
}

func (e shoutActionEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e shoutActionEnv) RandIntn(n int) int {
	return e.manager.randIntn(n)
}

func (e shoutActionEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	return e.manager.runtimeStatusMap()
}

func (e shoutActionEnv) SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.manager.selectRobots(req)
}

func (e shoutActionEnv) SendLocalShout(_ string, uid int, msg string, msgType int) error {
	return e.manager.doll.Shout(shared.RuntimeShoutCommand{
		UID:     uid,
		Message: msg,
		Type:    msgType,
	})
}

func (e shoutActionEnv) SendWorldShout(msg, name string, senderID uint16) error {
	return e.manager.worldShout.SendWorldShout(msg, name, senderID)
}

func (e shoutActionEnv) Templates() robottemplate.ShoutTemplates {
	return e.manager.loadShoutTemplates()
}
