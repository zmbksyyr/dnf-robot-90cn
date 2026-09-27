package s4a21

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
)

// SeedRobotQuestGates rewrites each robot's quest state so no quest-based
// dungeon gate can block it: accumulated quest rows are cleared and replaced
// with the fixed gate set (persistent gates completed, in-progress and quest
// connection gates active). Already matching robots are left untouched.
func SeedRobotQuestGates(ctx context.Context, databasePath string, robots []robotcap.Info, gates capabilitypvf.QuestGates) (int, error) {
	if strings.TrimSpace(databasePath) == "" || len(robots) == 0 || gates.Empty() {
		return 0, nil
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return 0, fmt.Errorf("open S4A21 quest gate database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return 0, fmt.Errorf("configure S4A21 quest gate database: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin S4A21 quest gate seed: %w", err)
	}
	defer tx.Rollback()
	changedRobots := 0
	for index := range robots {
		if robots[index].CID <= 0 {
			continue
		}
		changed, err := applyQuestGatesTx(ctx, tx, robots[index].CID, gates)
		if err != nil {
			return changedRobots, err
		}
		if changed {
			changedRobots++
		}
	}
	if changedRobots == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit S4A21 quest gate seed: %w", err)
	}
	return changedRobots, nil
}

func applyQuestGates(ctx context.Context, db *sql.DB, characterID int, gates capabilitypvf.QuestGates) error {
	if gates.Empty() {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed, err := applyQuestGatesTx(ctx, tx, characterID, gates)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return tx.Commit()
}

// applyQuestGatesTx makes the character's quest tables hold exactly the gate
// set and reports whether any row changed. Gate quests fill slots from 0 in
// ascending quest order. The server reads every row for admission and maze
// selection, while the fixed 30-slot client projection is filtered by the
// server's own presentation planner (world-map task cards), so slot overflow
// neither blocks entry nor floods the client.
func applyQuestGatesTx(ctx context.Context, tx *sql.Tx, characterID int, gates capabilitypvf.QuestGates) (bool, error) {
	if gates.Empty() {
		return false, nil
	}
	satisfied, err := questGatesSatisfied(ctx, tx, characterID, gates)
	if err != nil {
		return false, fmt.Errorf("inspect S4A21 quest gates id=%d: %w", characterID, err)
	}
	if satisfied {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_active_quests WHERE character_id=?`, characterID); err != nil {
		return false, fmt.Errorf("clear S4A21 active quests id=%d: %w", characterID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_quest_completions WHERE character_id=?`, characterID); err != nil {
		return false, fmt.Errorf("clear S4A21 quest completions id=%d: %w", characterID, err)
	}
	for _, questID := range gates.CompletedQuestIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO character_quest_completions(character_id,quest_id,completion_value) VALUES(?,?,1)`,
			characterID, questID); err != nil {
			return false, fmt.Errorf("seed S4A21 quest completion id=%d quest=%d: %w", characterID, questID, err)
		}
	}
	for index, questID := range gates.ActiveQuestIDs {
		activation, err := newQuestActivationID()
		if err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO character_active_quests(character_id,slot,quest_id,trigger_value,version,activation_id) VALUES(?,?,?,0,0,?)`,
			characterID, index, questID, activation); err != nil {
			return false, fmt.Errorf("seed S4A21 active quest id=%d quest=%d: %w", characterID, questID, err)
		}
	}
	return true, nil
}

func questGatesSatisfied(ctx context.Context, tx *sql.Tx, characterID int, gates capabilitypvf.QuestGates) (bool, error) {
	completed, err := queryIDSet(ctx, tx, `SELECT quest_id FROM character_quest_completions WHERE character_id=? AND completion_value != 0`, characterID)
	if err != nil {
		return false, fmt.Errorf("read S4A21 quest completions id=%d: %w", characterID, err)
	}
	active, err := queryIDSet(ctx, tx, `SELECT quest_id FROM character_active_quests WHERE character_id=?`, characterID)
	if err != nil {
		return false, fmt.Errorf("read S4A21 active quests id=%d: %w", characterID, err)
	}
	return sameIDSet(completed, gates.CompletedQuestIDs) && sameIDSet(active, gates.ActiveQuestIDs), nil
}

func queryIDSet(ctx context.Context, tx *sql.Tx, query string, args ...any) (map[int]struct{}, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]struct{})
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func sameIDSet(existing map[int]struct{}, want []int) bool {
	if len(existing) != len(want) {
		return false
	}
	for _, id := range want {
		if _, ok := existing[id]; !ok {
			return false
		}
	}
	return true
}

// newQuestActivationID matches the server's QuestActivationId storage shape:
// a random GUID in "N" format (32 lowercase hex characters).
func newQuestActivationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate S4A21 quest activation id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
