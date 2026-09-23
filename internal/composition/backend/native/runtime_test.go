package native

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"robot/internal/shared"

	_ "modernc.org/sqlite"
)

type announcementSenderRecorder struct {
	kind    string
	message string
	name    string
	id      uint16
}

func (s *announcementSenderRecorder) SendMonitorAnnouncement(kind, message, name string, senderID uint16) error {
	s.kind, s.message, s.name, s.id = kind, message, name, senderID
	return nil
}

func TestSystemAnnouncerOwnsNativeQueriesAndDelivery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"ATTACH ':memory:' AS taiwan_login",
		"CREATE TABLE taiwan_login.login_account_3 (login_status INTEGER)",
		"INSERT INTO taiwan_login.login_account_3 VALUES (1), (1), (0)",
		"ATTACH ':memory:' AS taiwan_cain_auction_gold",
		"CREATE TABLE taiwan_cain_auction_gold.auction_main (item_id INTEGER)",
		"INSERT INTO taiwan_cain_auction_gold.auction_main VALUES (10), (10), (20)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	sender := &announcementSenderRecorder{}
	at := time.Date(2026, 9, 23, 20, 0, 0, 0, time.Local)
	result, err := (SystemAnnouncer{Database: db, Sender: sender}).Announce(context.Background(), shared.SystemAnnouncementRequest{
		Kind: "notice", SenderName: "system", SenderID: 1, At: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Sent || result.Online != 2 || result.AuctionKinds != 2 || sender.kind != "notice" || sender.message != result.Message {
		t.Fatalf("result=%+v sender=%+v", result, sender)
	}
}
