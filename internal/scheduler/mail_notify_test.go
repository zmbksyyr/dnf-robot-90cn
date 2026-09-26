package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
)

type schedulerMailNotifier struct {
	calls        int
	err          error
	done         chan struct{}
	block        chan struct{}
	ignoreCancel bool
}

func (n *schedulerMailNotifier) PollOnce(ctx context.Context, _ time.Time) error {
	n.calls++
	if n.done != nil {
		n.done <- struct{}{}
	}
	if n.block != nil {
		if n.ignoreCancel {
			<-n.block
			return n.err
		}
		select {
		case <-n.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return n.err
}

func waitMailPoll(t *testing.T, notifier *schedulerMailNotifier) {
	t.Helper()
	select {
	case <-notifier.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mail poll")
	}
}

func waitMailIdle(t *testing.T, manager *RobotManager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manager.autoMu.Lock()
		running := manager.mailNotifyRunning
		manager.autoMu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for mail poll cleanup")
}

func TestMailNotifyFollowsAutoSchedulerAndConfig(t *testing.T) {
	notifier := &schedulerMailNotifier{done: make(chan struct{}, 4)}
	m := &RobotManager{autoEnabled: true}
	m.SetMailNotifier(notifier)
	rc := robotconfig.RuntimeConfig{AutoActions: true, AutoMailNotify: true, SystemActorPollMS: 3000}
	now := time.Now()
	m.pollMailNotifications(now, rc)
	waitMailPoll(t, notifier)
	waitMailIdle(t, m)
	m.pollMailNotifications(now.Add(time.Second), rc)
	if notifier.calls != 1 {
		t.Fatalf("mail poll calls = %d, want one throttled call", notifier.calls)
	}
	m.pollMailNotifications(now.Add(3*time.Second), rc)
	waitMailPoll(t, notifier)
	waitMailIdle(t, m)
	if notifier.calls != 2 {
		t.Fatalf("mail poll calls = %d, want second scheduled call", notifier.calls)
	}
	rc.AutoMailNotify = false
	m.pollMailNotifications(now.Add(6*time.Second), rc)
	if notifier.calls != 2 {
		t.Fatalf("disabled mail refresh polled %d times", notifier.calls)
	}
	rc.AutoMailNotify = true
	m.autoEnabled = false
	m.pollMailNotifications(now.Add(9*time.Second), rc)
	if notifier.calls != 2 {
		t.Fatalf("stopped auto polled mail %d times", notifier.calls)
	}
}

func TestMailNotifyFailureDoesNotDisableScheduler(t *testing.T) {
	notifier := &schedulerMailNotifier{err: errors.New("monitor unavailable"), done: make(chan struct{}, 1)}
	m := &RobotManager{autoEnabled: true}
	m.SetMailNotifier(notifier)
	rc := robotconfig.RuntimeConfig{AutoActions: true, AutoMailNotify: true, SystemActorPollMS: 1000}
	m.pollMailNotifications(time.Now(), rc)
	waitMailPoll(t, notifier)
	waitMailIdle(t, m)
	if notifier.calls != 1 || !m.autoEnabled {
		t.Fatalf("failure changed scheduler state: calls=%d enabled=%t", notifier.calls, m.autoEnabled)
	}
}

func TestMailNotifyDoesNotBlockSchedulerTick(t *testing.T) {
	notifier := &schedulerMailNotifier{done: make(chan struct{}, 1), block: make(chan struct{})}
	m := &RobotManager{autoEnabled: true}
	m.SetMailNotifier(notifier)
	rc := robotconfig.RuntimeConfig{AutoActions: true, AutoMailNotify: true, SystemActorPollMS: 1000}
	started := time.Now()
	m.pollMailNotifications(started, rc)
	if time.Since(started) > 50*time.Millisecond {
		t.Fatal("mail polling blocked the scheduler call")
	}
	waitMailPoll(t, notifier)
	m.pollMailNotifications(started.Add(2*time.Second), rc)
	if notifier.calls != 1 {
		t.Fatalf("overlapping mail poll calls = %d, want 1", notifier.calls)
	}
	close(notifier.block)
	waitMailIdle(t, m)
}

func TestWaitMailNotificationsCancelsRunningPoll(t *testing.T) {
	notifier := &schedulerMailNotifier{done: make(chan struct{}, 1), block: make(chan struct{})}
	m := &RobotManager{autoEnabled: true}
	m.SetMailNotifier(notifier)
	rc := robotconfig.RuntimeConfig{AutoActions: true, AutoMailNotify: true, SystemActorPollMS: 1000}
	m.pollMailNotifications(time.Now(), rc)
	waitMailPoll(t, notifier)

	finished := make(chan struct{})
	go func() {
		m.waitMailNotifications()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("mail poll did not stop after cancellation")
	}
}

func TestWaitMailNotificationsHasShutdownDeadline(t *testing.T) {
	previousTimeout := mailNotifyShutdownTimeout
	mailNotifyShutdownTimeout = 20 * time.Millisecond
	defer func() { mailNotifyShutdownTimeout = previousTimeout }()
	notifier := &schedulerMailNotifier{done: make(chan struct{}, 1), block: make(chan struct{}), ignoreCancel: true}
	m := &RobotManager{autoEnabled: true}
	m.SetMailNotifier(notifier)
	rc := robotconfig.RuntimeConfig{AutoActions: true, AutoMailNotify: true, SystemActorPollMS: 1000}
	m.pollMailNotifications(time.Now(), rc)
	waitMailPoll(t, notifier)
	started := time.Now()
	m.waitMailNotifications()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("mail shutdown deadline took %s", elapsed)
	}
	close(notifier.block)
	waitMailIdle(t, m)
}
