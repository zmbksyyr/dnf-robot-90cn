package s4a21

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	robotcap "robot/internal/capability/robot"
	robotlifecycle "robot/internal/capability/robotlifecycle"
)

// ReconcileRobotGrowth fills the transfer state of robots provisioned before
// growth writes existed. Only untransferred characters (grow_type low nibble 0)
// whose job has a released PVF transfer branch are touched; already transferred
// characters, including deliberate operator assignments, are left alone. The
// robot slice is updated in place so the in-memory directory matches the
// database.
func ReconcileRobotGrowth(ctx context.Context, databasePath string, robots []robotcap.Info, growTypes []int, jobGrows map[int][]int, randIntn func(int) int) (int, error) {
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
		if info.CID <= 0 || info.Grow&0x0F != 0 {
			continue
		}
		first, grow := robotlifecycle.SelectJobGrowth(info.Job, jobGrows, growTypes, randIntn)
		if first == 0 {
			continue
		}
		if err := updateCharacterGrow(ctx, tx, info.CID, grow); err != nil {
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
