package s4a21

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

func TestSessionTracksDisjointStoreState(t *testing.T) {
	session := &Session{selfUID: 0x1234}
	session.handleDisjointStorePacket(protocol.Packet{Type: protocol.NotiCreateExpertJobStore, Body: []byte{0x02}})
	if sent, _, active, _ := session.DisjointStoreState(); sent || active {
		t.Fatal("foreign create notification was applied")
	}
	create := make([]byte, 3)
	create[0] = protocol.ExpertJobStoreKindDisjointMachine
	create[1], create[2] = 0x34, 0x12
	session.handleDisjointStorePacket(protocol.Packet{Type: protocol.NotiCreateExpertJobStore, Body: create})
	sent, directAck, active, lastError := session.DisjointStoreState()
	if !sent || !directAck || !active || lastError != 0 {
		t.Fatalf("create notification state = %t/%t/%t/%d", sent, directAck, active, lastError)
	}
	closeBody := make([]byte, 4)
	closeBody[0], closeBody[1] = 0x34, 0x12
	session.handleDisjointStorePacket(protocol.Packet{Type: protocol.NotiCloseExpertJobStore, Body: closeBody})
	if sent, directAck, active, lastError := session.DisjointStoreState(); sent || directAck || active || lastError != 0 {
		t.Fatalf("close notification state = %t/%t/%t/%d", sent, directAck, active, lastError)
	}
}

func TestSessionAppliesDisjointStoreDirectAck(t *testing.T) {
	session := &Session{selfUID: 0x1234}
	session.handleDisjointStorePacket(protocol.Packet{Type: protocol.CmdCreateExpertJobStore, Body: []byte{1}})
	_, directAck, active, lastError := session.DisjointStoreState()
	if !directAck || active || lastError != 0 {
		t.Fatalf("success ack state = %t/%t/%d", directAck, active, lastError)
	}
	session.handleDisjointStorePacket(protocol.Packet{Type: protocol.CmdCreateExpertJobStore, Body: []byte{0, 19}})
	_, _, _, lastError = session.DisjointStoreState()
	if lastError != 19 {
		t.Fatalf("error ack state lastError=%d", lastError)
	}
}

func TestSessionOpenDisjointStoreRequiresIdentity(t *testing.T) {
	session := &Session{}
	if err := session.OpenDisjointStore(context.Background(), 500, 1, 2, 0); err == nil {
		t.Fatal("open without identity unexpectedly succeeded")
	}
	if err := (&Session{}).CloseDisjointStore(context.Background()); err == nil {
		t.Fatal("close without client unexpectedly succeeded")
	}
}

type disjointActionTestSession struct {
	actionTestSession
	openCost  int32
	openX     int16
	openY     int16
	openErr   error
	closeRuns int
	sent      bool
	directAck bool
	active    bool
	lastError byte
}

func (s *disjointActionTestSession) OpenDisjointStore(_ context.Context, cost uint32, x, y int16, _ int16) error {
	if s.openErr != nil {
		return s.openErr
	}
	s.openCost = int32(cost)
	s.openX, s.openY = x, y
	s.sent = true
	return nil
}

func (s *disjointActionTestSession) CloseDisjointStore(context.Context) error {
	s.closeRuns++
	s.sent, s.active = false, false
	return nil
}

func (s *disjointActionTestSession) DisjointStoreState() (bool, bool, bool, byte) {
	return s.sent, s.directAck, s.active, s.lastError
}

func TestActionTransportProjectsDisjointStoreState(t *testing.T) {
	session := &disjointActionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.RobotType != 0 || status.DisjointActive {
		t.Fatalf("initial disjoint status = %+v", status)
	}
	session.sent, session.directAck, session.active = true, true, true
	status := transport.RuntimeStatusMap()[7]
	if status.RobotType != 3 || !status.DisjointCreateSent || !status.DisjointDirectAck || !status.DisjointActive {
		t.Fatalf("active disjoint status = %+v", status)
	}
	session.sent, session.directAck, session.active = true, false, false
	session.lastError = 82
	status = transport.RuntimeStatusMap()[7]
	if status.RobotType != 3 || status.LastDisjointError != 82 || status.DisjointActive {
		t.Fatalf("failed disjoint status = %+v", status)
	}
}

func TestActionTransportStartsDisjointStoreAtKnownPosition(t *testing.T) {
	session := &disjointActionTestSession{}
	transport := NewActionTransport(actionTestFactory{session: session})
	if err := transport.Open(context.Background(), 7, shared.OpenSessionRequest{
		AccountName:      "acct",
		InitialTownKnown: true,
		InitialVillage:   1,
		InitialArea:      2,
		InitialX:         100,
		InitialY:         200,
	}); err != nil {
		t.Fatal(err)
	}
	if !transport.StartDisjointStore(7, 500) {
		t.Fatal("start disjoint store failed")
	}
	if session.openCost != 500 || session.openX != 100 || session.openY != 200 {
		t.Fatalf("open intent cost=%d pos=%d/%d", session.openCost, session.openX, session.openY)
	}
	if !transport.CloseDisjointStore(7) || session.closeRuns != 1 {
		t.Fatalf("close runs = %d", session.closeRuns)
	}
}

func TestActionTransportRejectsDisjointStoreWithoutPosition(t *testing.T) {
	session := &disjointActionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if transport.StartDisjointStore(7, 500) {
		t.Fatal("start without a known position unexpectedly succeeded")
	}
}

func TestActionTransportSetAreaFromDelegates(t *testing.T) {
	session := &areaActionTestSession{}
	transport := NewActionTransport(actionTestFactory{session: session})
	if err := transport.Open(context.Background(), 7, shared.OpenSessionRequest{
		AccountName:      "acct",
		InitialTownKnown: true,
		InitialVillage:   2,
		InitialArea:      4,
		InitialX:         10,
		InitialY:         20,
	}); err != nil {
		t.Fatal(err)
	}
	if !transport.SetAreaFrom(7, 2, 4, 120, 240, 2, 4) {
		t.Fatal("same-area set_area_from failed")
	}
	if session.town != (shared.TownMoveIntent{X: 120, Y: 240}) {
		t.Fatalf("same-area move intent = %+v", session.town)
	}
	if !transport.SetAreaFrom(7, 3, 5, 130, 250, 2, 4) {
		t.Fatal("cross-area set_area_from failed")
	}
	if session.area != (shared.TownAreaMoveIntent{Village: 3, Area: 5, X: 130, Y: 250}) {
		t.Fatalf("cross-area move intent = %+v", session.area)
	}
}

func TestDisjointProfessionWriterPreparesMachine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE characters(character_id INTEGER PRIMARY KEY, delete_flag INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_subtype0_fields(character_id INTEGER PRIMARY KEY, expert_job_type INTEGER NOT NULL DEFAULT 0, expert_job_exp INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_expert_job(character_id INTEGER PRIMARY KEY, giveup_count INTEGER NOT NULL DEFAULT 0, disjoint_machine_grade INTEGER NOT NULL DEFAULT 0, disjoint_machine_endurance INTEGER NOT NULL DEFAULT 0, enchanter_endurance INTEGER NOT NULL DEFAULT 0, updated_at TEXT);
INSERT INTO characters(character_id, delete_flag) VALUES(9, 0), (10, 1);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	writer := DisjointProfessionWriter{DatabasePath: path}
	if err := writer.EnsureDisjointProfession(9); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var jobType, jobExp, grade, endurance int
	if err := db.QueryRow(`SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0), COALESCE(e.disjoint_machine_grade, 0), COALESCE(e.disjoint_machine_endurance, 0)
		FROM characters c LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id LEFT JOIN character_expert_job e ON e.character_id=c.character_id WHERE c.character_id=9`).
		Scan(&jobType, &jobExp, &grade, &endurance); err != nil {
		t.Fatal(err)
	}
	if jobType != s4a21DisjointerExpertJobType || jobExp < s4a21DisjointerExpertJobExp || grade != s4a21DisjointMachineGrade || endurance != s4a21DisjointMachineEndurance {
		t.Fatalf("machine state = type %d exp %d grade %d endurance %d", jobType, jobExp, grade, endurance)
	}
	if err := writer.EnsureDisjointProfession(404); err == nil {
		t.Fatal("unknown character unexpectedly prepared")
	}
	if err := writer.EnsureDisjointProfession(10); err == nil {
		t.Fatal("deleted character unexpectedly prepared")
	}
}

func TestDisjointProfessionReadyProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE characters(character_id INTEGER PRIMARY KEY, delete_flag INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_subtype0_fields(character_id INTEGER PRIMARY KEY, expert_job_type INTEGER NOT NULL DEFAULT 0, expert_job_exp INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_expert_job(character_id INTEGER PRIMARY KEY, giveup_count INTEGER NOT NULL DEFAULT 0, disjoint_machine_grade INTEGER NOT NULL DEFAULT 0, disjoint_machine_endurance INTEGER NOT NULL DEFAULT 0, enchanter_endurance INTEGER NOT NULL DEFAULT 0, updated_at TEXT);
INSERT INTO characters(character_id, delete_flag) VALUES(9, 0), (10, 0), (11, 0), (12, 1);
INSERT INTO character_subtype0_fields(character_id, expert_job_type, expert_job_exp) VALUES(10, 3, 800), (11, 3, 800), (12, 3, 800);
INSERT INTO character_expert_job(character_id, disjoint_machine_grade, disjoint_machine_endurance) VALUES(10, 1, 300), (11, 1, 5), (12, 1, 300);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	writer := DisjointProfessionWriter{DatabasePath: path}
	for cid, want := range map[int]bool{9: false, 10: true, 11: false, 12: false} {
		ready, err := writer.DisjointProfessionReady(cid)
		if err != nil {
			t.Fatalf("cid=%d probe err=%v", cid, err)
		}
		if ready != want {
			t.Fatalf("cid=%d ready=%t want=%t", cid, ready, want)
		}
	}
	if _, err := writer.DisjointProfessionReady(0); err == nil {
		t.Fatal("invalid character id unexpectedly probed")
	}
}
