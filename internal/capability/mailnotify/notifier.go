package mailnotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"robot/internal/foundation/atomicfile"
	foundationconfig "robot/internal/foundation/config"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
)

const (
	defaultQueryLimit = 1000
	stateFileName     = "mail_notify_cursor.json"
	maxPendingMails   = 4096
	maxMailsPerPoll   = 64
	maxStateFileBytes = 1 << 20
	pendingMailTTL    = 24 * time.Hour
)

type Sender interface {
	NotifyNewMail(characNo uint32) error
}

type eventSource interface {
	currentCursor(ctx context.Context) (uint64, uint64, error)
	eventsAfter(ctx context.Context, letterID, postalID uint64, limit int) (uint64, uint64, []uint32, error)
}

type sqlEventSource struct {
	db *sql.DB
}

type cursorState struct {
	LetterID uint64           `json:"letter_id"`
	PostalID uint64           `json:"postal_id"`
	Pending  map[string]int64 `json:"pending,omitempty"`
}

type Notifier struct {
	source      eventSource
	sender      Sender
	statePath   string
	settleDelay time.Duration
	stateMu     lockhub.Locker
	state       cursorState
	stateLoaded bool
}

func New(db *sql.DB, sender Sender, configDir string) *Notifier {
	return &Notifier{
		source:    sqlEventSource{db: db},
		sender:    sender,
		statePath: filepath.Join(configDir, stateFileName),
	}
}

func (n *Notifier) PollOnce(ctx context.Context, now time.Time) error {
	if n == nil {
		return errors.New("mail notifier is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	n.stateMu.Lock()
	defer n.stateMu.Unlock()
	initialized := n.stateLoaded
	if !initialized {
		state, loaded, err := n.loadState()
		if err != nil {
			foundationlog.Robotf("[MAIL_NOTIFY] state_error err=%v\n", err)
		} else if loaded {
			n.state = state
			n.stateLoaded = true
			initialized = true
		}
	}
	if !initialized {
		state, err := n.currentCursor(ctx)
		if err != nil {
			return fmt.Errorf("establish mail notification baseline: %w", err)
		}
		if err := n.saveState(state); err != nil {
			return fmt.Errorf("save mail notification baseline: %w", err)
		}
		n.state = state
		n.stateLoaded = true
		foundationlog.Robotf("[MAIL_NOTIFY] baseline letter_id=%d postal_id=%d\n", state.LetterID, state.PostalID)
		return nil
	}
	if n.state.Pending == nil {
		n.state.Pending = make(map[string]int64)
	}
	return n.poll(ctx, &n.state, now)
}

func (n *Notifier) poll(ctx context.Context, state *cursorState, now time.Time) error {
	if n.source == nil || n.sender == nil {
		return errors.New("mail notifier is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	changed := prunePendingMails(state.Pending, now)
	if len(state.Pending) < maxPendingMails {
		collected, err := n.collect(ctx, state, now)
		if err != nil {
			return err
		}
		changed = changed || collected
	}

	delivered := 0
	attempted := 0
	failed := 0
	var firstNotifyErr error
	var pollErr error
	for key, readyAt := range state.Pending {
		if err := ctx.Err(); err != nil {
			pollErr = err
			break
		}
		if now.UnixMilli() < readyAt {
			continue
		}
		if attempted >= maxMailsPerPoll {
			break
		}
		attempted++
		value, err := strconv.ParseUint(key, 10, 32)
		if err != nil || value == 0 {
			delete(state.Pending, key)
			changed = true
			continue
		}
		if err := n.sender.NotifyNewMail(uint32(value)); err != nil {
			failed++
			if firstNotifyErr == nil {
				firstNotifyErr = err
			}
			continue
		}
		delete(state.Pending, key)
		changed = true
		delivered++
	}
	if changed {
		if err := n.saveState(*state); err != nil {
			return err
		}
	}
	if delivered > 0 {
		foundationlog.Robotf("[MAIL_NOTIFY] delivered=%d pending=%d letter_id=%d postal_id=%d\n", delivered, len(state.Pending), state.LetterID, state.PostalID)
	}
	if failed > 0 {
		foundationlog.Robotf("[MAIL_NOTIFY] notify_failed count=%d attempted=%d pending=%d first_err=%v\n", failed, attempted, len(state.Pending), firstNotifyErr)
	}
	return pollErr
}

func (n *Notifier) collect(ctx context.Context, state *cursorState, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	letterID, postalID, characNos, err := n.source.eventsAfter(ctx, state.LetterID, state.PostalID, defaultQueryLimit)
	if err != nil {
		return false, err
	}
	changed := false
	readyAt := now.Add(n.settleDelay).UnixMilli()
	dropped := 0
	for _, characNo := range characNos {
		if characNo == 0 {
			continue
		}
		key := strconv.FormatUint(uint64(characNo), 10)
		if _, exists := state.Pending[key]; !exists {
			if len(state.Pending) >= maxPendingMails {
				dropped++
				continue
			}
			state.Pending[key] = readyAt
			changed = true
		}
	}
	if dropped > 0 {
		foundationlog.Robotf("[MAIL_NOTIFY] pending_full dropped=%d limit=%d\n", dropped, maxPendingMails)
		// Do not acknowledge either source cursor until every event returned by
		// this query is durably represented in Pending. Re-reading accepted
		// events is harmless because Pending is keyed by character number.
		return changed, nil
	}
	if letterID != state.LetterID || postalID != state.PostalID {
		state.LetterID = letterID
		state.PostalID = postalID
		changed = true
	}
	return changed, nil
}

func queryEvents(ctx context.Context, db *sql.DB, query string, cursor uint64, limit int) (uint64, []uint32, error) {
	rows, err := db.QueryContext(ctx, query, cursor, limit)
	if err != nil {
		return cursor, nil, err
	}
	defer rows.Close()
	chars := make([]uint32, 0)
	for rows.Next() {
		var id uint64
		var characNo uint32
		if err := rows.Scan(&id, &characNo); err != nil {
			return cursor, nil, err
		}
		if id > cursor {
			cursor = id
		}
		chars = append(chars, characNo)
	}
	return cursor, chars, rows.Err()
}

func (n *Notifier) currentCursor(ctx context.Context) (cursorState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state := cursorState{Pending: make(map[string]int64)}
	var err error
	state.LetterID, state.PostalID, err = n.source.currentCursor(ctx)
	if err != nil {
		return state, err
	}
	return state, nil
}

func (s sqlEventSource) currentCursor(ctx context.Context) (uint64, uint64, error) {
	if s.db == nil {
		return 0, 0, errors.New("mail database is not configured")
	}
	var letterID, postalID uint64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(letter_id),0) FROM taiwan_cain_2nd.letter").Scan(&letterID); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(postal_id),0) FROM taiwan_cain_2nd.postal").Scan(&postalID); err != nil {
		return 0, 0, err
	}
	return letterID, postalID, nil
}

func (s sqlEventSource) eventsAfter(ctx context.Context, letterID, postalID uint64, limit int) (uint64, uint64, []uint32, error) {
	if s.db == nil {
		return letterID, postalID, nil, errors.New("mail database is not configured")
	}
	nextLetterID, letterChars, err := queryEvents(ctx, s.db,
		"SELECT letter_id,IF(send_charac_no=0,charac_no,0) FROM taiwan_cain_2nd.letter WHERE letter_id>? ORDER BY letter_id LIMIT ?",
		letterID, limit)
	if err != nil {
		return letterID, postalID, nil, fmt.Errorf("query letter events: %w", err)
	}
	nextPostalID, postalChars, err := queryEvents(ctx, s.db,
		"SELECT postal_id,IF(COALESCE(send_charac_no,0)=0,receive_charac_no,0) FROM taiwan_cain_2nd.postal WHERE postal_id>? ORDER BY postal_id LIMIT ?",
		postalID, limit)
	if err != nil {
		return letterID, postalID, nil, fmt.Errorf("query postal events: %w", err)
	}
	return nextLetterID, nextPostalID, append(letterChars, postalChars...), nil
}

func (n *Notifier) loadState() (cursorState, bool, error) {
	state := cursorState{Pending: make(map[string]int64)}
	file, err := os.Open(n.statePath)
	if os.IsNotExist(err) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	defer file.Close()
	if err := foundationconfig.DecodeJSONLimit(file, maxStateFileBytes, &state); err != nil {
		return cursorState{Pending: make(map[string]int64)}, false, err
	}
	if state.Pending == nil {
		state.Pending = make(map[string]int64)
	}
	trimPendingMails(state.Pending, maxPendingMails)
	return state, true, nil
}

func (n *Notifier) saveState(state cursorState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(n.statePath, append(data, '\n'), 0644)
}

func prunePendingMails(pending map[string]int64, now time.Time) bool {
	if len(pending) == 0 {
		return false
	}
	cutoff := now.Add(-pendingMailTTL).UnixMilli()
	changed := false
	for key, readyAt := range pending {
		if readyAt <= 0 || readyAt < cutoff {
			delete(pending, key)
			changed = true
		}
	}
	return changed
}

func trimPendingMails(pending map[string]int64, limit int) {
	if len(pending) <= limit || limit <= 0 {
		return
	}
	type pendingMail struct {
		key     string
		readyAt int64
	}
	entries := make([]pendingMail, 0, len(pending))
	for key, readyAt := range pending {
		entries = append(entries, pendingMail{key: key, readyAt: readyAt})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].readyAt != entries[j].readyAt {
			return entries[i].readyAt < entries[j].readyAt
		}
		return entries[i].key < entries[j].key
	})
	for _, entry := range entries[limit:] {
		delete(pending, entry.key)
	}
}
