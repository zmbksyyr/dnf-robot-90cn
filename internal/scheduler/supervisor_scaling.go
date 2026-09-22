package scheduler

import (
	"sort"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

const actorStopWait = 5 * time.Second

func (s *RobotSupervisor) stopAutoActors() {
	actors := s.ledger.BeginDrainAutoActors()
	s.stopDrainingActors(actors, actorStopWait)
}

func (s *RobotSupervisor) stopSomeAutoActors(limit, floor int) {
	if limit <= 0 {
		return
	}
	s.pressureMu.Lock()
	if s.pressureRunning {
		s.pressureMu.Unlock()
		return
	}
	s.pressureRunning = true
	pressureDone := make(chan struct{})
	s.pressureDone = pressureDone
	s.pressureMu.Unlock()

	status := s.manager.runtimeStatusMap()
	actors := s.ledger.BeginDrainSomeAutoActors(status, limit, floor)
	if len(actors) == 0 {
		s.pressureMu.Lock()
		s.pressureRunning = false
		if s.pressureDone == pressureDone {
			s.pressureDone = nil
		}
		s.pressureMu.Unlock()
		close(pressureDone)
		return
	}
	robotLogf("[RobotSupervisor] pressure_release actors=%d floor=%d\n", len(actors), floor)
	go func() {
		defer func() {
			s.pressureMu.Lock()
			s.pressureRunning = false
			if s.pressureDone == pressureDone {
				s.pressureDone = nil
			}
			s.pressureMu.Unlock()
			close(pressureDone)
		}()
		s.stopDrainingActors(actors, actorStopWait)
	}()
}

func (s *RobotSupervisor) stopDrainingActors(actors []*actormodel.Actor, wait time.Duration) []*actormodel.Actor {
	requestActorStops(actors)
	if len(actors) == 0 {
		return nil
	}
	return s.waitForDrainingActorsUntil(actors, time.Now().Add(wait))
}

func requestActorStops(actors []*actormodel.Actor) {
	for _, actor := range actors {
		actor.RequestStop()
	}
}

func (s *RobotSupervisor) waitForDrainingActorsUntil(actors []*actormodel.Actor, deadline time.Time) []*actormodel.Actor {
	pending := append([]*actormodel.Actor(nil), actors...)
	for len(pending) > 0 {
		next := make([]*actormodel.Actor, 0, len(pending))
		for _, actor := range pending {
			if !s.ledger.ReapActor(actor) {
				next = append(next, actor)
			}
		}
		pending = next
		if len(pending) == 0 || !time.Now().Before(deadline) {
			return pending
		}
		wait := time.Until(deadline)
		if wait > 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		time.Sleep(wait)
	}
	return nil
}

func (s *RobotSupervisor) assignIdleAutoActors(rc robotconfig.RuntimeConfig) {
	idle := s.idleAutoActors()
	if len(idle) == 0 {
		return
	}
	sort.Slice(idle, func(i, j int) bool {
		return idle[i].SlotIDValue() < idle[j].SlotIDValue()
	})
	limit := robotconfig.OnlineStartRateForNeed(len(idle), rc)
	if limit > len(idle) {
		limit = len(idle)
	}
	pairs := s.acquireUIDs(rc, idle[:limit])
	for _, pair := range pairs {
		if pair.actor.AssignAndWait(pair.uid, 10*time.Second) {
			continue
		}
		s.ledger.UnleaseUID(pair.uid, pair.actor)
		robotLogf("[RobotSupervisor] assign_failed slot=%d uid=%d\n", pair.actor.SlotIDValue(), pair.uid)
	}
}

func (s *RobotSupervisor) idleAutoActors() []*actormodel.Actor {
	return s.ledger.IdleAutoActors()
}

type actorLease struct {
	actor *actormodel.Actor
	uid   int
}

func (s *RobotSupervisor) acquireUIDs(rc robotconfig.RuntimeConfig, actors []*actormodel.Actor) []actorLease {
	if len(actors) == 0 {
		return nil
	}
	robots, err := s.manager.selectRobots(robotcap.CommandRequest{Count: rc.MaxOnlineRobots})
	if err != nil {
		robotLogf("[RobotSupervisor] select_robots_failed err=%v\n", err)
		return nil
	}
	out := make([]actorLease, 0, len(actors))
	nextActor := 0
	for _, robot := range robots {
		if nextActor >= len(actors) {
			return out
		}
		actor := actors[nextActor]
		if s.ledger.TryLeaseUID(robot.UID, actor) {
			out = append(out, actorLease{actor: actor, uid: robot.UID})
			nextActor++
		}
	}
	need := len(actors) - nextActor
	if need <= 0 {
		return out
	}
	target := robotconfig.TargetCapacity(rc)
	createRoom := robotconfig.CreateRoom(rc, len(robots))
	if createRoom <= 0 {
		robotLogf("[RobotSupervisor] create_blocked_by_target existing=%d target=%d need=%d blocked=%d\n", len(robots), target, need, s.ledger.BlockedCount())
		return out
	}
	if need > createRoom {
		need = createRoom
	}
	if !s.createNext.IsZero() && time.Now().Before(s.createNext) {
		return out
	}
	created, err := s.manager.CreateRobots(robotcap.CreateRequest{Count: need})
	if err != nil {
		s.createFailures++
		delays := [...]time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}
		index := s.createFailures - 1
		if index >= len(delays) {
			index = len(delays) - 1
		}
		s.createNext = time.Now().Add(delays[index])
		robotLogf("[RobotSupervisor] create_failed count=%d err=%v\n", need, err)
		return out
	}
	s.createFailures = 0
	s.createNext = time.Time{}
	if len(created) > 0 {
		s.manager.addAutoCreated(len(created))
	}
	for _, robot := range created {
		if nextActor >= len(actors) {
			break
		}
		actor := actors[nextActor]
		if s.ledger.TryLeaseUID(robot.UID, actor) {
			out = append(out, actorLease{actor: actor, uid: robot.UID})
			nextActor++
		}
	}
	return out
}

func (s *RobotSupervisor) maintainTarget(rc robotconfig.RuntimeConfig) {
	if err := s.manager.repo().EnsureSchema(); err != nil {
		robotLogf("[RobotSupervisor] ensure_schema_failed err=%v\n", err)
		return
	}
	s.ensureAutoActorSlots(rc, robotconfig.TargetCapacity(rc))
}

func (s *RobotSupervisor) ensureAutoActorSlots(rc robotconfig.RuntimeConfig, target int) {
	status := s.manager.runtimeStatusMap()
	extra := s.ledger.EnsureAutoActorSlots(s.runtime, rc, target, status)
	s.stopDrainingActors(extra, actorStopWait)
}
