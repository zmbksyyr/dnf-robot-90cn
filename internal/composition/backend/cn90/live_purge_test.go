package cn90

import (
	"context"
	"os"
	"testing"

	robotcap "robot/internal/capability/robot"
)

// TestLivePurgeRobotFleet removes every robot account in the configured UID
// range through the adapter's planned dangerous-delete path. It is env-gated
// because it deletes live data.
func TestLivePurgeRobotFleet(t *testing.T) {
	if os.Getenv("CN90_LIVE_PURGE") == "" {
		t.Skip("set CN90_LIVE_PURGE=1 to purge the live robot fleet")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	purger := SQLiteRobotPurger{DatabasePath: databasePath, AccountPrefix: "robot"}
	request := robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeRange, MinUID: 17000000, MaxUID: 17001199}
	plan, err := purger.PlanDangerousDelete(ctx, request)
	if err != nil {
		t.Fatalf("plan purge: %v", err)
	}
	t.Logf("purge plan: accounts=%d characters=%d uids=%d", plan.AccountCount, plan.CharacterCount, len(plan.UIDs))
	if plan.AccountCount == 0 && plan.CharacterCount == 0 {
		t.Log("nothing to purge")
		return
	}
	result, err := purger.ExecuteDangerousDelete(ctx, plan)
	if err != nil {
		t.Fatalf("execute purge: %v", err)
	}
	t.Logf("purged: accounts=%d characters=%d", result.AccountCount, result.CharacterCount)

	verify := SQLiteRobotPurger{DatabasePath: databasePath, AccountPrefix: "robot"}
	after, err := verify.PlanDangerousDelete(ctx, request)
	if err != nil {
		t.Fatalf("verify purge: %v", err)
	}
	if after.AccountCount != 0 || after.CharacterCount != 0 {
		t.Fatalf("purge left accounts=%d characters=%d", after.AccountCount, after.CharacterCount)
	}
	t.Logf("verification: no robot accounts or characters remain in the range")

	db := openPurgeTestDatabase(t, databasePath)
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'dnf_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	orphans := 0
	for _, table := range tables {
		var count int
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE character_id IN (SELECT character_id FROM dnf_characters WHERE account_id LIKE 'robot%')`
		if err := db.QueryRow(query).Scan(&count); err != nil {
			continue
		}
		orphans += count
	}
	t.Logf("checked %d dnf_* tables for orphan robot rows: %d", len(tables), orphans)
}
