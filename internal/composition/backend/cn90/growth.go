package cn90

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
		return 0, fmt.Errorf("open 90CN growth reconcile database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return 0, fmt.Errorf("configure 90CN growth reconcile database: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin 90CN growth reconcile: %w", err)
	}
	defer tx.Rollback()
	changed := 0
	for index := range robots {
		info := &robots[index]
		if info.CID <= 0 {
			continue
		}
		grow, ok := reconciledGrow(info.Job, info.Grow, growTypes, jobGrows[info.Job], statTables, reconcileAwakening, randIntn)
		if !ok {
			continue
		}
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT level FROM characters WHERE character_id=? AND delete_flag=0`, info.CID).Scan(&level); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return changed, fmt.Errorf("read 90CN growth reconcile level id=%d: %w", info.CID, err)
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
		return 0, fmt.Errorf("commit 90CN growth reconcile: %w", err)
	}
	return changed, nil
}

// reconciledGrow returns the target grow byte for one stored robot.
// Untransferred, unreleased-branch and malformed values are re-picked from the
// PVF catalog; a released branch at stage 0 is awakened when enabled and the
// branch's .chr file actually contains a configured awakening row. States that
// already satisfy the configuration are reported as unchanged.
func reconciledGrow(job, grow int, growTypes []int, branches []int, statTables map[int]capabilitypvf.CharacterStatTables, reconcileAwakening bool, randIntn func(int) int) (int, bool) {
	first := grow & 0x0F
	second := (grow >> 4) & 0x0F
	if !releasedBranch(branches, first) {
		pick, target := robotlifecycle.SelectBranchGrowth(branches, growTypes, randIntn)
		targetSecond := (target >> 4) & 0x0F
		if targetSecond > 0 && !statTables[job].AwakenSet[pick+1][targetSecond] {
			if stage, ok := availableAwakening(statTables[job], pick, growTypes, randIntn); ok {
				target = (stage << 4) | (pick & 0x0F)
			} else {
				target = pick & 0x0F
			}
		}
		if target == grow {
			return 0, false
		}
		return target, true
	}
	if second > 2 {
		// Corrupt awakening nibble on a released branch: keep the branch and
		// re-pick a configured stage the .chr table actually has.
		if stage, ok := availableAwakening(statTables[job], first, growTypes, randIntn); ok {
			target := (stage << 4) | (first & 0x0F)
			if target == grow {
				return 0, false
			}
			return target, true
		}
		target := first & 0x0F
		if target == grow {
			return 0, false
		}
		return target, true
	}
	if second != 0 || !reconcileAwakening {
		return 0, false
	}
	stage, ok := availableAwakening(statTables[job], first, growTypes, randIntn)
	if !ok {
		return 0, false
	}
	target := (stage << 4) | (first & 0x0F)
	if target == grow {
		return 0, false
	}
	return target, true
}

// releasedBranch reports whether the job's PVF catalog contains the transfer
// branch value.
func releasedBranch(branches []int, first int) bool {
	if first <= 0 {
		return false
	}
	for _, branch := range branches {
		if branch == first {
			return true
		}
	}
	return false
}

// availableAwakening picks a configured awakening stage whose [awakening N]
// row exists in the branch's .chr table, so stat computation cannot fail on a
// placeholder branch.
func availableAwakening(tables capabilitypvf.CharacterStatTables, first int, growTypes []int, randIntn func(int) int) (int, bool) {
	candidates := make([]int, 0, len(growTypes))
	for _, stage := range growTypes {
		if stage > 0 && stage <= 2 && tables.AwakenSet[first+1][stage] {
			candidates = append(candidates, stage)
		}
	}
	if len(candidates) == 0 {
		return 0, false
	}
	pick := 0
	if randIntn != nil {
		if index := randIntn(len(candidates)); index > 0 && index < len(candidates) {
			pick = index
		}
	}
	return candidates[pick], true
}
