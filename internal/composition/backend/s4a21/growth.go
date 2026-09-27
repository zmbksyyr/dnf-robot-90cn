package s4a21

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	robotlifecycle "robot/internal/capability/robotlifecycle"
)

// ReconcileRobotGrowth fills the transfer and awakening state of robots
// provisioned before those writes existed. Untransferred characters (grow_type
// low nibble 0) whose job has a released PVF transfer branch receive a branch;
// when reconcileAwakening is set, transferred characters still at awakening
// stage 0 receive a configured positive stage. Already matching characters,
// including deliberate operator assignments, are left alone. The robot slice is
// updated in place so the in-memory directory matches the database.
func ReconcileRobotGrowth(ctx context.Context, databasePath string, robots []robotcap.Info, growTypes []int, jobGrows map[int][]int, statTables map[int]capabilitypvf.CharacterStatTables, reconcileAwakening bool, randIntn func(int) int) (int, error) {
	if strings.TrimSpace(databasePath) == "" || len(robots) == 0 || len(jobGrows) == 0 {
		return 0, nil
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return 0, fmt.Errorf("open S4A21 growth reconcile database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return 0, fmt.Errorf("configure S4A21 growth reconcile database: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin S4A21 growth reconcile: %w", err)
	}
	defer tx.Rollback()
	changed := 0
	for index := range robots {
		info := &robots[index]
		if info.CID <= 0 {
			continue
		}
		grow, ok := reconciledGrow(info.Job, info.Grow, growTypes, jobGrows, reconcileAwakening, randIntn)
		if !ok {
			continue
		}
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT level FROM characters WHERE character_id=? AND delete_flag=0`, info.CID).Scan(&level); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return changed, fmt.Errorf("read S4A21 growth reconcile level id=%d: %w", info.CID, err)
		}
		info.Level = level
		if err := updateCharacterGrow(ctx, tx, info.CID, grow); err != nil {
			return changed, err
		}
		if err := writeCombatStats(ctx, tx, info.CID, info.Job, info.Level, grow, statTables); err != nil {
			return changed, err
		}
		if err := resetCharacterSkills(ctx, tx, info.CID); err != nil {
			return changed, err
		}
		info.Grow = grow
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit S4A21 growth reconcile: %w", err)
	}
	return changed, nil
}

// reconciledGrow returns the target grow byte for one stored robot. It fills a
// missing transfer branch for untransferred robots and, when reconcileAwakening
// is set, a positive awakening stage for transferred robots that still carry
// stage 0. Characters whose stored value already satisfies the configuration
// (including jobs without released branches) are reported as unchanged.
func reconciledGrow(job, grow int, growTypes []int, jobGrows map[int][]int, reconcileAwakening bool, randIntn func(int) int) (int, bool) {
	first := grow & 0x0F
	second := (grow >> 4) & 0x0F
	if first == 0 {
		_, target := robotlifecycle.SelectJobGrowth(job, jobGrows, growTypes, randIntn)
		if target == 0 || target == grow {
			return 0, false
		}
		return target, true
	}
	if second == 0 && reconcileAwakening {
		stage, ok := robotlifecycle.SelectAwakeningStage(growTypes, randIntn)
		if !ok {
			return 0, false
		}
		return (stage << 4) | (first & 0x0F), true
	}
	return 0, false
}
