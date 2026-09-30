package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ExpertJobProfessionWriter prepares a robot character for an expert-job stall
// while its account is offline.
//
// The 90CN server checks Subtype0Tail.ExpertJobType when it validates
// CREATE_EXPERT_JOB_STORE and reads the machine state from character_expert_job.
// Both values are projected at character select, so the writes only take effect
// on the next login; the store workflow logs the account out first.
type ExpertJobProfessionWriter struct {
	DatabasePath string
}

const (
	// cn90DisjointerExpertJobType / cn90EnchanterExpertJobType mirror
	// ExpertJobStateCodec.DisjointerType / EnchanterType.
	cn90DisjointerExpertJobType = 3
	cn90EnchanterExpertJobType  = 1
	// cn90DisjointerExpertJobExp keeps the profession above the PVF
	// [expertness exp] thresholds. A higher existing value is preserved.
	cn90DisjointerExpertJobExp = 800
	// cn90EnchanterExpertJobExp reaches the last [expertness exp] threshold
	// (1195) so every rareness card qualification from [rarity recipe] is
	// available to the stall.
	cn90EnchanterExpertJobExp = 1195
	// cn90DisjointMachineGrade/Endurance are the PVF initial machine state
	// ([endurance initial value] = 300). Restoring them gives every open store
	// the full durability instead of a stale partially used machine.
	cn90DisjointMachineGrade     = 1
	cn90DisjointMachineEndurance = 300
	// cn90DisjointMachineMaxGrade mirrors the PVF [endurance repair cost] rule
	// count the server accepts as the machine grade ceiling.
	cn90DisjointMachineMaxGrade = 11
	// cn90DisjointMachineRefreshEndurance is the floor below which a store
	// attempt refreshes the machine offline. Above it the profession probe lets
	// the robot open the next machine on its existing session.
	cn90DisjointMachineRefreshEndurance = 100
	// cn90EnchanterEndurance is the PVF enchanter [endurance initial value].
	cn90EnchanterEndurance = 300
	// cn90EnchanterRefreshEndurance is the enchanter refresh floor, mirroring
	// the disassembler machine probe.
	cn90EnchanterRefreshEndurance = 100

	expertJobProfessionWriteTimeout = 6 * time.Second
)

// EnsureDisjointProfession writes the disjointer identity and resets the
// machine state for one character. It returns an error when the character does
// not exist or when the writes cannot be verified.
func (w ExpertJobProfessionWriter) EnsureDisjointProfession(cid int) error {
	return w.ensureExpertJob(cid, cn90DisjointerExpertJobType, cn90DisjointerExpertJobExp, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO character_expert_job(character_id, disjoint_machine_grade, disjoint_machine_endurance, updated_at)
			VALUES(?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(character_id) DO UPDATE SET
				disjoint_machine_grade=excluded.disjoint_machine_grade,
				disjoint_machine_endurance=excluded.disjoint_machine_endurance,
				updated_at=CURRENT_TIMESTAMP`,
			cid, cn90DisjointMachineGrade, cn90DisjointMachineEndurance)
		if err != nil {
			return fmt.Errorf("write 90CN disjoint machine state cid=%d: %w", cid, err)
		}
		return nil
	}, func(ctx context.Context, db *sql.DB) error {
		var expertJobType, expertJobExp, grade, endurance int
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0),
			       COALESCE(e.disjoint_machine_grade, 0), COALESCE(e.disjoint_machine_endurance, 0)
			FROM characters c
			LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id
			LEFT JOIN character_expert_job e ON e.character_id=c.character_id
			WHERE c.character_id=?`, cid).Scan(&expertJobType, &expertJobExp, &grade, &endurance); err != nil {
			return fmt.Errorf("verify 90CN disjoint profession cid=%d: %w", cid, err)
		}
		if expertJobType != cn90DisjointerExpertJobType || expertJobExp < cn90DisjointerExpertJobExp ||
			grade != cn90DisjointMachineGrade || endurance != cn90DisjointMachineEndurance {
			return fmt.Errorf("verify 90CN disjoint profession mismatch cid=%d type=%d exp=%d grade=%d endurance=%d",
				cid, expertJobType, expertJobExp, grade, endurance)
		}
		return nil
	})
}

// EnsureEnchantProfession writes the enchanter identity and resets the stall
// endurance for one character.
func (w ExpertJobProfessionWriter) EnsureEnchantProfession(cid int) error {
	return w.ensureExpertJob(cid, cn90EnchanterExpertJobType, cn90EnchanterExpertJobExp, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO character_expert_job(character_id, enchanter_endurance, updated_at)
			VALUES(?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(character_id) DO UPDATE SET
				enchanter_endurance=excluded.enchanter_endurance,
				updated_at=CURRENT_TIMESTAMP`,
			cid, cn90EnchanterEndurance)
		if err != nil {
			return fmt.Errorf("write 90CN enchanter state cid=%d: %w", cid, err)
		}
		return nil
	}, func(ctx context.Context, db *sql.DB) error {
		var expertJobType, expertJobExp, endurance int
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0),
			       COALESCE(e.enchanter_endurance, 0)
			FROM characters c
			LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id
			LEFT JOIN character_expert_job e ON e.character_id=c.character_id
			WHERE c.character_id=?`, cid).Scan(&expertJobType, &expertJobExp, &endurance); err != nil {
			return fmt.Errorf("verify 90CN enchanter profession cid=%d: %w", cid, err)
		}
		if expertJobType != cn90EnchanterExpertJobType || expertJobExp < cn90EnchanterExpertJobExp ||
			endurance != cn90EnchanterEndurance {
			return fmt.Errorf("verify 90CN enchanter profession mismatch cid=%d type=%d exp=%d endurance=%d",
				cid, expertJobType, expertJobExp, endurance)
		}
		return nil
	})
}

// ensureExpertJob writes the shared expert-job identity plus one stall-specific
// state row and verifies both in the same offline window.
func (w ExpertJobProfessionWriter) ensureExpertJob(cid, expertJobType, expertJobExp int, writeState func(context.Context, *sql.Tx) error, verify func(context.Context, *sql.DB) error) error {
	if strings.TrimSpace(w.DatabasePath) == "" {
		return fmt.Errorf("90CN expert job database path is empty")
	}
	if cid <= 0 {
		return fmt.Errorf("90CN expert job character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertJobProfessionWriteTimeout)
	defer cancel()
	db, err := sql.Open("sqlite", w.DatabasePath)
	if err != nil {
		return fmt.Errorf("open 90CN expert job database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("configure 90CN expert job database: %w", err)
	}
	var deleteFlag int
	if err := db.QueryRowContext(ctx, `SELECT delete_flag FROM characters WHERE character_id=?`, cid).Scan(&deleteFlag); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("90CN expert job character %d does not exist", cid)
		}
		return fmt.Errorf("read 90CN expert job character %d: %w", cid, err)
	}
	if deleteFlag != 0 {
		return fmt.Errorf("90CN expert job character %d is deleted", cid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin 90CN expert job write: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO character_subtype0_fields(character_id, expert_job_type, expert_job_exp)
		VALUES(?, ?, ?)
		ON CONFLICT(character_id) DO UPDATE SET
			expert_job_type=excluded.expert_job_type,
			expert_job_exp=MAX(COALESCE(character_subtype0_fields.expert_job_exp, 0), excluded.expert_job_exp)`,
		cid, expertJobType, expertJobExp); err != nil {
		return fmt.Errorf("write 90CN expert job subtype0 cid=%d: %w", cid, err)
	}
	if err := writeState(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit 90CN expert job cid=%d: %w", cid, err)
	}
	return verify(ctx, db)
}

// DisjointProfessionReady reports whether the character can open another
// disassembler machine without an offline refresh. The read is used to skip
// the logout, profession write and login cycle while the machine still has
// durability.
func (w ExpertJobProfessionWriter) DisjointProfessionReady(cid int) (bool, error) {
	var expertJobType, grade, endurance int
	ok, err := w.readExpertJob(cid, `
		SELECT COALESCE(f.expert_job_type, 0),
		       COALESCE(e.disjoint_machine_grade, 0), COALESCE(e.disjoint_machine_endurance, 0)`,
		&expertJobType, &grade, &endurance)
	if err != nil || !ok {
		return false, err
	}
	return expertJobType == cn90DisjointerExpertJobType &&
		grade >= 1 && grade <= cn90DisjointMachineMaxGrade &&
		endurance >= cn90DisjointMachineRefreshEndurance, nil
}

// EnchantProfessionReady reports whether the character can open another
// enchanter stall without an offline refresh.
func (w ExpertJobProfessionWriter) EnchantProfessionReady(cid int) (bool, error) {
	var expertJobType, expertJobExp, endurance int
	ok, err := w.readExpertJob(cid, `
		SELECT COALESCE(f.expert_job_type, 0), COALESCE(f.expert_job_exp, 0),
		       COALESCE(e.enchanter_endurance, 0)`,
		&expertJobType, &expertJobExp, &endurance)
	if err != nil || !ok {
		return false, err
	}
	return expertJobType == cn90EnchanterExpertJobType &&
		expertJobExp >= cn90EnchanterExpertJobExp &&
		endurance >= cn90EnchanterRefreshEndurance, nil
}

// readExpertJob loads one character's expert-job projection. found=false means
// the character is missing or deleted.
func (w ExpertJobProfessionWriter) readExpertJob(cid int, query string, dest ...interface{}) (found bool, err error) {
	if strings.TrimSpace(w.DatabasePath) == "" {
		return false, fmt.Errorf("90CN expert job database path is empty")
	}
	if cid <= 0 {
		return false, fmt.Errorf("90CN expert job character id=%d is invalid", cid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertJobProfessionWriteTimeout)
	defer cancel()
	db, err := sql.Open("sqlite", w.DatabasePath)
	if err != nil {
		return false, fmt.Errorf("open 90CN expert job database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return false, fmt.Errorf("configure 90CN expert job read: %w", err)
	}
	args := []interface{}{cid}
	row := db.QueryRowContext(ctx, query+`
		FROM characters c
		LEFT JOIN character_subtype0_fields f ON f.character_id=c.character_id
		LEFT JOIN character_expert_job e ON e.character_id=c.character_id
		WHERE c.character_id=? AND c.delete_flag=0`, args...)
	if err := row.Scan(dest...); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("read 90CN expert job cid=%d: %w", cid, err)
	}
	return true, nil
}
