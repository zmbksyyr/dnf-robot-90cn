package s4a21

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// FollowAccountLocator resolves the last played village of a follow account
// from the game database, so the scheduler can spawn robots near that
// character.
type FollowAccountLocator struct {
	DatabasePath string
}

func (l FollowAccountLocator) FollowAccountVillageLastPlayed(ctx context.Context, account string) (int, bool, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return 0, false, fmt.Errorf("S4A21 follow account is empty")
	}
	if strings.TrimSpace(l.DatabasePath) == "" {
		return 0, false, fmt.Errorf("S4A21 follow account database path is required")
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(l.DatabasePath))
	if err != nil {
		return 0, false, fmt.Errorf("open S4A21 follow account database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return 0, false, fmt.Errorf("configure S4A21 follow account database: %w", err)
	}
	var village int
	err = db.QueryRowContext(ctx, `SELECT c.town_id FROM accounts a
JOIN characters c ON c.account_id=a.account_id
WHERE a.m_id=? AND c.delete_flag=0
ORDER BY c.updated_at DESC, c.character_id DESC LIMIT 1`, account).Scan(&village)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query S4A21 follow account %s: %w", account, err)
	}
	return village, village > 0, nil
}
