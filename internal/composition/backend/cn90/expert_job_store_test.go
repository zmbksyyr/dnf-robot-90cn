package cn90

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

func TestSessionTracksExpertJobStoreState(t *testing.T) {
	session := &Session{selfUID: 0x1234}
	session.handleExpertJobStorePacket(protocol.Packet{Type: protocol.NotiCreateExpertJobStore, Body: []byte{0x02}})
	if _, sent, _, active, _ := session.ExpertJobStoreState(); sent || active {
		t.Fatal("foreign create notification was applied")
	}
	create := make([]byte, 3)
	create[0] = protocol.ExpertJobStoreKindDisjointMachine
	create[1], create[2] = 0x34, 0x12
	session.handleExpertJobStorePacket(protocol.Packet{Type: protocol.NotiCreateExpertJobStore, Body: create})
	kind, sent, directAck, active, lastError := session.ExpertJobStoreState()
	if kind != shared.ExpertJobStoreDisjoint || !sent || !directAck || !active || lastError != 0 {
		t.Fatalf("create notification state = %d/%t/%t/%t/%d", kind, sent, directAck, active, lastError)
	}
	closeBody := make([]byte, 4)
	closeBody[0], closeBody[1] = 0x34, 0x12
	session.handleExpertJobStorePacket(protocol.Packet{Type: protocol.NotiCloseExpertJobStore, Body: closeBody})
	if kind, sent, directAck, active, lastError := session.ExpertJobStoreState(); kind != shared.ExpertJobStoreNone || sent || directAck || active || lastError != 0 {
		t.Fatalf("close notification state = %d/%t/%t/%t/%d", kind, sent, directAck, active, lastError)
	}
}

func TestSessionTracksEnchantStoreNotificationKind(t *testing.T) {
	session := &Session{selfUID: 0x1234}
	create := make([]byte, 3)
	create[0] = protocol.ExpertJobStoreKindEnchantShop
	create[1], create[2] = 0x34, 0x12
	session.handleExpertJobStorePacket(protocol.Packet{Type: protocol.NotiCreateExpertJobStore, Body: create})
	kind, _, _, active, _ := session.ExpertJobStoreState()
	if kind != shared.ExpertJobStoreEnchant || !active {
		t.Fatalf("enchant notification state = %d active=%t", kind, active)
	}
}

func TestSessionAppliesExpertJobStoreDirectAck(t *testing.T) {
	session := &Session{selfUID: 0x1234}
	session.store.kind = shared.ExpertJobStoreDisjoint
	session.handleExpertJobStorePacket(protocol.Packet{Type: protocol.CmdCreateExpertJobStore, Body: []byte{1}})
	_, _, directAck, active, lastError := session.ExpertJobStoreState()
	if !directAck || active || lastError != 0 {
		t.Fatalf("success ack state = %t/%t/%d", directAck, active, lastError)
	}
	session.handleExpertJobStorePacket(protocol.Packet{Type: protocol.CmdCreateExpertJobStore, Body: []byte{0, 19}})
	_, _, _, _, lastError = session.ExpertJobStoreState()
	if lastError != 19 {
		t.Fatalf("error ack state lastError=%d", lastError)
	}
}

func TestSessionOpenExpertJobStoreRequiresIdentity(t *testing.T) {
	session := &Session{}
	if err := session.OpenExpertJobStore(context.Background(), shared.ExpertJobStoreEnchant, 500, 1, 2, 0); err == nil {
		t.Fatal("open without identity unexpectedly succeeded")
	}
	if err := (&Session{}).CloseExpertJobStore(context.Background()); err == nil {
		t.Fatal("close without client unexpectedly succeeded")
	}
}

type expertStoreActionTestSession struct {
	actionTestSession
	openKind  shared.ExpertJobStoreKind
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

func (s *expertStoreActionTestSession) OpenExpertJobStore(_ context.Context, kind shared.ExpertJobStoreKind, cost uint32, x, y int16, _ int16) error {
	if s.openErr != nil {
		return s.openErr
	}
	s.openKind = kind
	s.openCost = int32(cost)
	s.openX, s.openY = x, y
	s.sent = true
	return nil
}

func (s *expertStoreActionTestSession) CloseExpertJobStore(context.Context) error {
	s.closeRuns++
	s.sent, s.active = false, false
	return nil
}

func (s *expertStoreActionTestSession) ExpertJobStoreState() (shared.ExpertJobStoreKind, bool, bool, bool, byte) {
	return s.openKind, s.sent, s.directAck, s.active, s.lastError
}

func TestActionTransportProjectsExpertJobStoreState(t *testing.T) {
	session := &expertStoreActionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.RobotType != 0 || status.DisjointActive || status.EnchantActive {
		t.Fatalf("initial store status = %+v", status)
	}
	session.openKind, session.sent, session.directAck, session.active = shared.ExpertJobStoreDisjoint, true, true, true
	status := transport.RuntimeStatusMap()[7]
	if status.RobotType != 3 || !status.DisjointCreateSent || !status.DisjointDirectAck || !status.DisjointActive || status.EnchantActive {
		t.Fatalf("active disjoint status = %+v", status)
	}
	session.openKind, session.sent, session.directAck, session.active = shared.ExpertJobStoreEnchant, true, true, true
	status = transport.RuntimeStatusMap()[7]
	if status.RobotType != 3 || !status.EnchantCreateSent || !status.EnchantDirectAck || !status.EnchantActive || status.DisjointActive {
		t.Fatalf("active enchant status = %+v", status)
	}
	session.sent, session.directAck, session.active = true, false, false
	session.lastError = 82
	status = transport.RuntimeStatusMap()[7]
	if status.RobotType != 3 || status.LastEnchantError != 82 || status.EnchantActive {
		t.Fatalf("failed enchant status = %+v", status)
	}
}

func TestActionTransportStartsExpertJobStoreAtKnownPosition(t *testing.T) {
	session := &expertStoreActionTestSession{}
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
	if !transport.StartExpertJobStore(7, shared.ExpertJobStoreEnchant, 500) {
		t.Fatal("start enchant store failed")
	}
	if session.openKind != shared.ExpertJobStoreEnchant || session.openCost != 500 || session.openX != 100 || session.openY != 200 {
		t.Fatalf("open intent kind=%d cost=%d pos=%d/%d", session.openKind, session.openCost, session.openX, session.openY)
	}
	if !transport.CloseExpertJobStore(7) || session.closeRuns != 1 {
		t.Fatalf("close runs = %d", session.closeRuns)
	}
}

func TestActionTransportRejectsExpertJobStoreWithoutPosition(t *testing.T) {
	session := &expertStoreActionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if transport.StartExpertJobStore(7, shared.ExpertJobStoreDisjoint, 500) {
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

func createExpertJobTestSchema(t *testing.T, path string, statements string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(statements); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

const expertJobTestSchema = `PRAGMA foreign_keys=ON;
CREATE TABLE characters(character_id INTEGER PRIMARY KEY, delete_flag INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_subtype0_fields(character_id INTEGER PRIMARY KEY, expert_job_type INTEGER NOT NULL DEFAULT 0, expert_job_exp INTEGER NOT NULL DEFAULT 0);
CREATE TABLE character_expert_job(character_id INTEGER PRIMARY KEY, giveup_count INTEGER NOT NULL DEFAULT 0, disjoint_machine_grade INTEGER NOT NULL DEFAULT 0, disjoint_machine_endurance INTEGER NOT NULL DEFAULT 0, enchanter_endurance INTEGER NOT NULL DEFAULT 0, updated_at TEXT);`

func TestExpertJobProfessionWriterPreparesStalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := createExpertJobTestSchema(t, path, expertJobTestSchema+`
INSERT INTO characters(character_id, delete_flag) VALUES(9, 0), (10, 1);`)
	db.Close()

	writer := ExpertJobProfessionWriter{DatabasePath: path}
	if err := writer.EnsureDisjointProfession(9); err != nil {
		t.Fatal(err)
	}
	db = createExpertJobTestSchema(t, path, `PRAGMA busy_timeout=5000;`)
	defer db.Close()
	var jobType, jobExp, grade, endurance, enchanterEndurance int
	if err := db.QueryRow(`SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0), COALESCE(e.disjoint_machine_grade, 0), COALESCE(e.disjoint_machine_endurance, 0), COALESCE(e.enchanter_endurance, 0)
		FROM characters c LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id LEFT JOIN character_expert_job e ON e.character_id=c.character_id WHERE c.character_id=9`).
		Scan(&jobType, &jobExp, &grade, &endurance, &enchanterEndurance); err != nil {
		t.Fatal(err)
	}
	if jobType != cn90DisjointerExpertJobType || jobExp < cn90DisjointerExpertJobExp || grade != cn90DisjointMachineGrade || endurance != cn90DisjointMachineEndurance {
		t.Fatalf("machine state = type %d exp %d grade %d endurance %d", jobType, jobExp, grade, endurance)
	}

	// Switching the same character to the enchanter profession must update the
	// shared profession fields and the stall state.
	if err := writer.EnsureEnchantProfession(9); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0), COALESCE(e.enchanter_endurance, 0)
		FROM characters c LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id LEFT JOIN character_expert_job e ON e.character_id=c.character_id WHERE c.character_id=9`).
		Scan(&jobType, &jobExp, &enchanterEndurance); err != nil {
		t.Fatal(err)
	}
	if jobType != cn90EnchanterExpertJobType || jobExp < cn90EnchanterExpertJobExp || enchanterEndurance != cn90EnchanterEndurance {
		t.Fatalf("enchanter state = type %d exp %d endurance %d", jobType, jobExp, enchanterEndurance)
	}

	if err := writer.EnsureDisjointProfession(404); err == nil {
		t.Fatal("unknown character unexpectedly prepared")
	}
	if err := writer.EnsureEnchantProfession(10); err == nil {
		t.Fatal("deleted character unexpectedly prepared")
	}
}

func TestExpertJobProfessionReadyProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := createExpertJobTestSchema(t, path, expertJobTestSchema+`
INSERT INTO characters(character_id, delete_flag) VALUES(9, 0), (10, 0), (11, 0), (12, 0), (13, 0), (14, 1);
INSERT INTO character_subtype0_fields(character_id, expert_job_type, expert_job_exp) VALUES(10, 3, 800), (11, 3, 800), (12, 1, 1195), (13, 1, 100), (14, 3, 800);
INSERT INTO character_expert_job(character_id, disjoint_machine_grade, disjoint_machine_endurance, enchanter_endurance) VALUES(10, 1, 300, 0), (11, 1, 5, 0), (12, 0, 0, 300), (13, 0, 0, 300), (14, 1, 300, 0);`)
	db.Close()
	writer := ExpertJobProfessionWriter{DatabasePath: path}
	for cid, want := range map[int]bool{9: false, 10: true, 11: false, 12: false, 13: false, 14: false} {
		ready, err := writer.DisjointProfessionReady(cid)
		if err != nil {
			t.Fatalf("cid=%d probe err=%v", cid, err)
		}
		if ready != want {
			t.Fatalf("disjoint cid=%d ready=%t want=%t", cid, ready, want)
		}
	}
	for cid, want := range map[int]bool{9: false, 10: false, 11: false, 12: true, 13: false, 14: false} {
		ready, err := writer.EnchantProfessionReady(cid)
		if err != nil {
			t.Fatalf("cid=%d enchant probe err=%v", cid, err)
		}
		if ready != want {
			t.Fatalf("enchant cid=%d ready=%t want=%t", cid, ready, want)
		}
	}
	if _, err := writer.DisjointProfessionReady(0); err == nil {
		t.Fatal("invalid character id unexpectedly probed")
	}
	if _, err := writer.EnchantProfessionReady(0); err == nil {
		t.Fatal("invalid character id unexpectedly probed")
	}
}
