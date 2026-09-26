package s4a21

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
)

type purgeState interface {
	robotstate.Directory
	robotstate.RobotRemover
}

// SQLiteRobotPurger is the explicit maintenance path for stale S4A21 robot
// data that is not visible through the runtime state table. Normal cleanup
// continues to use the game protocol.
type SQLiteRobotPurger struct {
	DatabasePath  string
	AccountPrefix string
	State         purgeState
	Sessions      sessionCloser
}

type purgeAccount struct {
	id         int
	uid        int
	totalChars int
	cids       []int
}

func (p SQLiteRobotPurger) PlanDangerousDelete(ctx context.Context, request robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error) {
	if err := p.validate(request); err != nil {
		return robotcap.DangerousDeletePlan{}, err
	}
	db, err := p.open(ctx)
	if err != nil {
		return robotcap.DangerousDeletePlan{}, err
	}
	defer db.Close()
	return p.plan(ctx, db, request)
}

func (p SQLiteRobotPurger) ExecuteDangerousDelete(ctx context.Context, requested robotcap.DangerousDeletePlan) (robotcap.DangerousDeleteResult, error) {
	request := robotcap.DangerousDeleteRequest{
		Mode: requested.Mode, UID: requested.UID, CID: requested.CID,
		MinUID: requested.MinUID, MaxUID: requested.MaxUID,
	}
	if err := p.validate(request); err != nil {
		return robotcap.DangerousDeleteResult{}, err
	}
	for _, uid := range requested.RegistryUIDs {
		if p.Sessions != nil {
			if err := p.Sessions.Close(uid); err != nil {
				return purgeResult(requested, false), fmt.Errorf("close S4A21 robot session uid=%d: %w", uid, err)
			}
		}
	}
	db, err := p.open(ctx)
	if err != nil {
		return purgeResult(requested, false), err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return purgeResult(requested, false), fmt.Errorf("connect S4A21 purge database: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return purgeResult(requested, false), fmt.Errorf("configure S4A21 purge database: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return purgeResult(requested, false), fmt.Errorf("begin S4A21 purge: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	actual, accounts, err := p.planOnConn(ctx, conn, request)
	if err != nil {
		return purgeResult(requested, false), err
	}
	// The database may have changed between the preview and this transaction
	// (new logins, registrations or character changes). Deleting a different
	// set than the operator confirmed is never acceptable.
	if !purgePlanMatches(requested, actual) {
		return purgeResult(requested, false), fmt.Errorf(
			"S4A21 delete plan changed since preview: previewed accounts=%d characters=%d, current accounts=%d characters=%d; re-run the preview",
			requested.AccountCount, requested.CharacterCount, actual.AccountCount, actual.CharacterCount)
	}
	if err := executePurge(ctx, conn, request.Mode, accounts); err != nil {
		return purgeResult(actual, false), err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return purgeResult(actual, false), fmt.Errorf("commit S4A21 purge: %w", err)
	}
	committed = true
	if len(actual.RegistryUIDs) > 0 && p.State != nil {
		if err := p.State.RemoveRobots(ctx, actual.RegistryUIDs); err != nil {
			return purgeResult(actual, true), fmt.Errorf("remove purged S4A21 robot state: %w", err)
		}
	}
	return purgeResult(actual, actual.AccountCount > 0 || actual.CharacterCount > 0), nil
}

func (p SQLiteRobotPurger) open(ctx context.Context) (*sql.DB, error) {
	if strings.TrimSpace(p.DatabasePath) == "" {
		return nil, fmt.Errorf("S4A21 purge database path is required")
	}
	db, err := sql.Open("sqlite", p.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open S4A21 purge database: %w", err)
	}
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure S4A21 purge database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping S4A21 purge database: %w", err)
	}
	return db, nil
}

func (p SQLiteRobotPurger) validate(request robotcap.DangerousDeleteRequest) error {
	if strings.TrimSpace(p.AccountPrefix) == "" {
		return fmt.Errorf("S4A21 purge account prefix is required")
	}
	switch request.Mode {
	case robotcap.DangerousDeleteModeCID:
		if request.CID <= 0 {
			return fmt.Errorf("S4A21 purge CID must be positive")
		}
	case robotcap.DangerousDeleteModeUID:
		if request.UID <= 0 {
			return fmt.Errorf("S4A21 purge UID must be positive")
		}
	case robotcap.DangerousDeleteModeRange:
		if request.MinUID <= 0 || request.MaxUID < request.MinUID {
			return fmt.Errorf("S4A21 purge UID range is invalid")
		}
	default:
		return fmt.Errorf("unsupported S4A21 purge mode %q", request.Mode)
	}
	return nil
}

type purgeQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (p SQLiteRobotPurger) plan(ctx context.Context, query purgeQuery, request robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error) {
	plan, _, err := p.planOnConn(ctx, query, request)
	return plan, err
}

func (p SQLiteRobotPurger) planOnConn(ctx context.Context, query purgeQuery, request robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, []purgeAccount, error) {
	accounts, err := p.selectAccounts(ctx, query, request)
	if err != nil {
		return robotcap.DangerousDeletePlan{}, nil, err
	}
	plan := robotcap.DangerousDeletePlan{
		Mode: request.Mode, UID: request.UID, CID: request.CID,
		MinUID: request.MinUID, MaxUID: request.MaxUID,
	}
	for _, account := range accounts {
		plan.UIDs = append(plan.UIDs, account.uid)
		plan.CIDs = append(plan.CIDs, account.cids...)
	}
	if request.Mode == robotcap.DangerousDeleteModeCID && len(accounts) == 1 {
		plan.UID = accounts[0].uid
	}
	plan.CharacterCount = len(plan.CIDs)
	if request.Mode != robotcap.DangerousDeleteModeCID {
		plan.AccountCount = len(accounts)
	} else if len(accounts) == 1 && accounts[0].totalChars == 1 {
		plan.AccountCount = 1
	}
	plan.RegistryUIDs, err = p.registryUIDs(ctx, plan.UIDs)
	if err != nil {
		return robotcap.DangerousDeletePlan{}, nil, err
	}
	plan.RegistryCount = len(plan.RegistryUIDs)
	return plan, accounts, nil
}

func (p SQLiteRobotPurger) selectAccounts(ctx context.Context, query purgeQuery, request robotcap.DangerousDeleteRequest) ([]purgeAccount, error) {
	rows, err := query.QueryContext(ctx, `SELECT a.account_id,a.m_id,c.character_id,
(SELECT COUNT(*) FROM characters cx WHERE cx.account_id=a.account_id)
FROM accounts a LEFT JOIN characters c ON c.account_id=a.account_id
WHERE a.m_id LIKE ? ORDER BY a.account_id,c.character_id`, p.AccountPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("scan S4A21 purge candidates: %w", err)
	}
	defer rows.Close()
	byID := make(map[int]*purgeAccount)
	ordered := make([]*purgeAccount, 0)
	for rows.Next() {
		var accountID int
		var accountName string
		var cid sql.NullInt64
		var totalChars int
		if err := rows.Scan(&accountID, &accountName, &cid, &totalChars); err != nil {
			return nil, fmt.Errorf("read S4A21 purge candidate: %w", err)
		}
		uid, owned := strictRobotUID(accountName, p.AccountPrefix)
		if !owned || !purgeRequestMatches(request, uid, cid) {
			continue
		}
		account := byID[accountID]
		if account == nil {
			account = &purgeAccount{id: accountID, uid: uid, totalChars: totalChars}
			byID[accountID] = account
			ordered = append(ordered, account)
		}
		if cid.Valid {
			account.cids = append(account.cids, int(cid.Int64))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan S4A21 purge candidates: %w", err)
	}
	accounts := make([]purgeAccount, 0, len(ordered))
	for _, account := range ordered {
		accounts = append(accounts, *account)
	}
	return accounts, nil
}

// purgePlanMatches reports whether the transaction re-read selects exactly the
// accounts, characters and registry identities the operator previewed. The
// comparison is order-independent because both plans come from the same
// deterministic query but could be assembled in different orders.
func purgePlanMatches(requested, actual robotcap.DangerousDeletePlan) bool {
	if requested.AccountCount != actual.AccountCount || requested.CharacterCount != actual.CharacterCount {
		return false
	}
	return equalIntSets(requested.UIDs, actual.UIDs) &&
		equalIntSets(requested.CIDs, actual.CIDs) &&
		equalIntSets(requested.RegistryUIDs, actual.RegistryUIDs)
}

func equalIntSets(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]int(nil), left...)
	rightCopy := append([]int(nil), right...)
	sort.Ints(leftCopy)
	sort.Ints(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func purgeRequestMatches(request robotcap.DangerousDeleteRequest, uid int, cid sql.NullInt64) bool {
	switch request.Mode {
	case robotcap.DangerousDeleteModeCID:
		return cid.Valid && int(cid.Int64) == request.CID
	case robotcap.DangerousDeleteModeUID:
		return uid == request.UID
	default:
		return uid >= request.MinUID && uid <= request.MaxUID
	}
}

func strictRobotUID(account, prefix string) (int, bool) {
	if !strings.HasPrefix(account, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(account, prefix)
	uid, err := strconv.Atoi(suffix)
	return uid, err == nil && uid > 0 && account == prefix+strconv.Itoa(uid)
}

func (p SQLiteRobotPurger) registryUIDs(ctx context.Context, selected []int) ([]int, error) {
	if p.State == nil || len(selected) == 0 {
		return nil, nil
	}
	robots, err := p.State.SelectRobots(ctx, robotcap.CommandRequest{Count: int(^uint(0) >> 1)})
	if err != nil {
		return nil, fmt.Errorf("read S4A21 robot state for purge: %w", err)
	}
	wanted := make(map[int]struct{}, len(selected))
	for _, uid := range selected {
		wanted[uid] = struct{}{}
	}
	result := make([]int, 0)
	for _, robot := range robots {
		if _, ok := wanted[robot.UID]; ok {
			result = append(result, robot.UID)
		}
	}
	sort.Ints(result)
	return result, nil
}

func executePurge(ctx context.Context, conn *sql.Conn, mode string, accounts []purgeAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	if mode == robotcap.DangerousDeleteModeCID {
		account := accounts[0]
		if len(account.cids) != 1 {
			return fmt.Errorf("S4A21 CID purge candidate is inconsistent")
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM characters WHERE account_id=? AND character_id=?`, account.id, account.cids[0]); err != nil {
			return fmt.Errorf("delete S4A21 character cid=%d: %w", account.cids[0], err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM accounts WHERE account_id=? AND NOT EXISTS (SELECT 1 FROM characters WHERE account_id=?)`, account.id, account.id); err != nil {
			return fmt.Errorf("delete empty S4A21 robot account uid=%d: %w", account.uid, err)
		}
		return nil
	}
	ids := make([]int, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.id)
	}
	return deleteStartupAccounts(ctx, conn, ids)
}

func purgeResult(plan robotcap.DangerousDeletePlan, deleted bool) robotcap.DangerousDeleteResult {
	return robotcap.DangerousDeleteResult{
		Mode: plan.Mode, UID: plan.UID, CID: plan.CID, MinUID: plan.MinUID, MaxUID: plan.MaxUID,
		AccountCount: plan.AccountCount, CharacterCount: plan.CharacterCount, RegistryCount: plan.RegistryCount,
		Deleted: deleted,
	}
}
