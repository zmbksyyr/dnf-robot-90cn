package scheduler

// actorOnlineGate mirrors the actor package's optional attempt gate. The
// compile-time assertion keeps the runtime side of the contract wired: an
// earlier change added the gate to RobotManager only, the actor type assertion
// silently failed, and every retry bypassed the adaptive budget.
type actorOnlineGate interface {
	TryAcquireOnlineAttempt() bool
	ReleaseOnlineAttempt()
}

var _ actorOnlineGate = (*RobotRuntime)(nil)
