package mailnotify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeEventSource struct {
	letterID  uint64
	postalID  uint64
	characNos []uint32
	err       error
}

func (s fakeEventSource) currentCursor(context.Context) (uint64, uint64, error) {
	return s.letterID, s.postalID, s.err
}

func (s fakeEventSource) eventsAfter(context.Context, uint64, uint64, int) (uint64, uint64, []uint32, error) {
	return s.letterID, s.postalID, append([]uint32(nil), s.characNos...), s.err
}

type fakeSender struct {
	chars []uint32
	err   error
}

func (s *fakeSender) NotifyNewMail(characNo uint32) error {
	if s.err != nil {
		return s.err
	}
	s.chars = append(s.chars, characNo)
	return nil
}

func TestCurrentCursorEstablishesBaselineWithoutPendingMail(t *testing.T) {
	n := &Notifier{source: fakeEventSource{letterID: 12, postalID: 34}}
	state, err := n.currentCursor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.LetterID != 12 || state.PostalID != 34 || len(state.Pending) != 0 {
		t.Fatalf("baseline = %+v", state)
	}
}

func TestPollCoalescesLetterAndPostalForOneCharacter(t *testing.T) {
	sender := &fakeSender{}
	n := &Notifier{
		source:      fakeEventSource{letterID: 11, postalID: 21, characNos: []uint32{100, 100}},
		sender:      sender,
		statePath:   filepath.Join(t.TempDir(), stateFileName),
		settleDelay: 0,
	}
	state := cursorState{LetterID: 10, PostalID: 20, Pending: make(map[string]int64)}
	if err := n.poll(context.Background(), &state, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(sender.chars) != 1 || sender.chars[0] != 100 {
		t.Fatalf("notifications = %v, want [100]", sender.chars)
	}
	if len(state.Pending) != 0 || state.LetterID != 11 || state.PostalID != 21 {
		t.Fatalf("state = %+v", state)
	}
}

func TestPollOnceKeepsCursorInMemoryAfterBaseline(t *testing.T) {
	source := &fakeEventSource{letterID: 10, postalID: 20}
	sender := &fakeSender{}
	n := &Notifier{
		source: source, sender: sender, statePath: filepath.Join(t.TempDir(), stateFileName), settleDelay: 0,
	}
	if err := n.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(n.statePath); err != nil {
		t.Fatal(err)
	}
	source.letterID = 11
	source.postalID = 21
	source.characNos = []uint32{100}
	if err := n.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(sender.chars) != 1 || sender.chars[0] != 100 {
		t.Fatalf("notifications = %v, want [100]", sender.chars)
	}
}

func TestPollKeepsFailedNotificationPending(t *testing.T) {
	sender := &fakeSender{err: errors.New("monitor unavailable")}
	n := &Notifier{
		source:      fakeEventSource{letterID: 2, postalID: 3, characNos: []uint32{200}},
		sender:      sender,
		statePath:   filepath.Join(t.TempDir(), stateFileName),
		settleDelay: 0,
	}
	state := cursorState{LetterID: 1, PostalID: 2, Pending: make(map[string]int64)}
	if err := n.poll(context.Background(), &state, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Pending["200"]; !ok {
		t.Fatalf("failed notification was not retained: %+v", state)
	}
}

func TestPollBoundsPendingAndDeliveryWork(t *testing.T) {
	chars := make([]uint32, maxPendingMails+100)
	for index := range chars {
		chars[index] = uint32(index + 1)
	}
	sender := &fakeSender{}
	n := &Notifier{
		source:    fakeEventSource{letterID: 2, postalID: 3, characNos: chars},
		sender:    sender,
		statePath: filepath.Join(t.TempDir(), stateFileName),
	}
	state := cursorState{LetterID: 1, PostalID: 2, Pending: make(map[string]int64)}
	if err := n.poll(context.Background(), &state, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(sender.chars) != maxMailsPerPoll {
		t.Fatalf("delivered %d notifications, want per-poll limit %d", len(sender.chars), maxMailsPerPoll)
	}
	if len(state.Pending) != maxPendingMails-maxMailsPerPoll {
		t.Fatalf("pending notifications = %d, want %d", len(state.Pending), maxPendingMails-maxMailsPerPoll)
	}
}

func TestPollCancellationPreservesPending(t *testing.T) {
	sender := &fakeSender{}
	n := &Notifier{
		source:    fakeEventSource{},
		sender:    sender,
		statePath: filepath.Join(t.TempDir(), stateFileName),
	}
	state := cursorState{Pending: map[string]int64{"100": time.Now().Add(-time.Second).UnixMilli()}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.poll(ctx, &state, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll error = %v, want context cancellation", err)
	}
	if len(sender.chars) != 0 || len(state.Pending) != 1 {
		t.Fatalf("cancelled poll changed delivery state: sent=%v pending=%v", sender.chars, state.Pending)
	}
}

func TestPrunePendingMailsRemovesExpiredEntries(t *testing.T) {
	now := time.Now()
	pending := map[string]int64{
		"old":   now.Add(-pendingMailTTL - time.Second).UnixMilli(),
		"fresh": now.UnixMilli(),
	}
	if !prunePendingMails(pending, now) {
		t.Fatal("expired pending mail was not reported as changed")
	}
	if len(pending) != 1 || pending["fresh"] == 0 {
		t.Fatalf("pruned pending mail = %v", pending)
	}
}
