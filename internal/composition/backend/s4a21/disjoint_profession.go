package s4a21

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DisjointProfessionWriter prepares a robot character for the disassembler
// machine while its account is offline.
//
// The S4A21 server checks Subtype0Tail.ExpertJobType == 3 when it validates
// CREATE_EXPERT_JOB_STORE and reads the machine grade/endurance from
// character_expert_job. Both values are projected at character select, so the
// writes only take effect on the next login; the disjoint workflow logs the
// account out before calling this port.
type DisjointProfessionWriter struct {
	DatabasePath string
}

const (
	// s4a21DisjointerExpertJobType is ExpertJobStateCodec.DisjointerType.
	s4a21DisjointerExpertJobType = 3
	// s4a21DisjointerExpertJobExp keeps the profession above the first PVF
	// [expertness exp] threshold (20) with the same 800 value the reference
	// robot used. A higher existing value is preserved.
	s4a21DisjointerExpertJobExp = 800
	// s4a21DisjointMachineGrade/Endurance are the PVF initial machine state
	// ([endurance initial value] = 300). Restoring them gives every open store
	// the full durability instead of a stale partially used machine.
	s4a21DisjointMachineGrade     = 1
	s4a21DisjointMachineEndurance = 300
	// s4a21DisjointMachineMaxGrade mirrors the PVF [endurance repair cost] rule
	// count the server accepts as the machine grade ceiling.
	s4a21DisjointMachineMaxGrade = 11
	// s4a21DisjointMachineRefreshEndurance is the floor below which a store
	// attempt refreshes the machine offline. Above it the profession probe lets
	// the robot open the next machine on its existing session.
	s4a21DisjointMachineRefreshEndurance = 100

	disjointProfessionWriteTimeout = 6 * time.Second
)

// EnsureDisjointProfession writes the disjointer identity and resets the
// machine state for one character. It returns an error when the character does
// not exist or when the writes cannot be verified.
func (w DisjointProfessionWriter) EnsureDisjointProfession(cid int) error {
	if strings.TrimSpace(w.DatabasePath) == "" {
		return fmt.Errorf("S4A21 disjoint profession database path is empty")
	}
	if cid <= 0 {
		return fmt.Errorf("S4A21 disjoint profession character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), disjointProfessionWriteTimeout)
	defer cancel()
	db, err := sql.Open("sqlite", w.DatabasePath)
	if err != nil {
		return fmt.Errorf("open S4A21 disjoint profession database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("configure S4A21 disjoint profession database: %w", err)
	}
	var deleteFlag int
	if err := db.QueryRowContext(ctx, `SELECT delete_flag FROM characters WHERE character_id=?`, cid).Scan(&deleteFlag); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("S4A21 disjoint profession character %d does not exist", cid)
		}
		return fmt.Errorf("read S4A21 disjoint profession character %d: %w", cid, err)
	}
	if deleteFlag != 0 {
		return fmt.Errorf("S4A21 disjoint profession character %d is deleted", cid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin S4A21 disjoint profession write: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO character_subtype0_fields(character_id, expert_job_type, expert_job_exp)
		VALUES(?, ?, ?)
		ON CONFLICT(character_id) DO UPDATE SET
			expert_job_type=excluded.expert_job_type,
			expert_job_exp=MAX(COALESCE(character_subtype0_fields.expert_job_exp, 0), excluded.expert_job_exp)`,
		cid, s4a21DisjointerExpertJobType, s4a21DisjointerExpertJobExp); err != nil {
		return fmt.Errorf("write S4A21 disjoint profession subtype0 cid=%d: %w", cid, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO character_expert_job(character_id, disjoint_machine_grade, disjoint_machine_endurance, updated_at)
		VALUES(?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(character_id) DO UPDATE SET
			disjoint_machine_grade=excluded.disjoint_machine_grade,
			disjoint_machine_endurance=excluded.disjoint_machine_endurance,
			updated_at=CURRENT_TIMESTAMP`,
		cid, s4a21DisjointMachineGrade, s4a21DisjointMachineEndurance); err != nil {
		return fmt.Errorf("write S4A21 disjoint machine state cid=%d: %w", cid, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit S4A21 disjoint profession cid=%d: %w", cid, err)
	}
	var expertJobType, expertJobExp, grade, endurance int
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0),
		       COALESCE(e.disjoint_machine_grade, 0), COALESCE(e.disjoint_machine_endurance, 0)
		FROM characters c
		LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id
		LEFT JOIN character_expert_job e ON e.character_id=c.character_id
		WHERE c.character_id=?`, cid).Scan(&expertJobType, &expertJobExp, &grade, &endurance); err != nil {
		return fmt.Errorf("verify S4A21 disjoint profession cid=%d: %w", cid, err)
	}
	if expertJobType != s4a21DisjointerExpertJobType || expertJobExp < s4a21DisjointerExpertJobExp ||
		grade != s4a21DisjointMachineGrade || endurance != s4a21DisjointMachineEndurance {
		return fmt.Errorf("verify S4A21 disjoint profession mismatch cid=%d type=%d exp=%d grade=%d endurance=%d",
			cid, expertJobType, expertJobExp, grade, endurance)
	}
	return nil
}

// DisjointProfessionReady reports whether the character can open another
// machine without an offline refresh. The read is used to skip the logout,
// profession write and login cycle while the machine still has durability.
func (w DisjointProfessionWriter) DisjointProfessionReady(cid int) (bool, error) {
	if strings.TrimSpace(w.DatabasePath) == "" {
		return false, fmt.Errorf("S4A21 disjoint profession database path is empty")
	}
	if cid <= 0 {
		return false, fmt.Errorf("S4A21 disjoint profession character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), disjointProfessionWriteTimeout)
	defer cancel()
	db, err := sql.Open("sqlite", w.DatabasePath)
	if err != nil {
		return false, fmt.Errorf("open S4A21 disjoint profession database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return false, fmt.Errorf("configure S4A21 disjoint profession read: %w", err)
	}
	var jobType, grade, endurance int
	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(f.expert_job_type, 0),
		       COALESCE(e.disjoint_machine_grade, 0), COALESCE(e.disjoint_machine_endurance, 0)
		FROM characters c
		LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id
		LEFT JOIN character_expert_job e ON e.character_id=c.character_id
		WHERE c.character_id=? AND c.delete_flag=0`, cid).Scan(&jobType, &grade, &endurance)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("read S4A21 disjoint profession cid=%d: %w", cid, err)
	}
	return jobType == s4a21DisjointerExpertJobType &&
		grade >= 1 && grade <= s4a21DisjointMachineMaxGrade &&
		endurance >= s4a21DisjointMachineRefreshEndurance, nil
}
