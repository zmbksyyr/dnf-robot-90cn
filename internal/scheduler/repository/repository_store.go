package repository

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	equipcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	storecap "robot/internal/capability/store"
)

func (r *SQLRepository) MarkStoreStarted(uid int) error {
	_, err := r.Exec("UPDATE d_starsky.Dummylist SET function_type=2 WHERE UID=?", uid)
	return err
}

func (r *SQLRepository) PrepareStorePosition(info robotcap.Info) error {
	_, err := r.Exec("UPDATE d_starsky.Dummylist SET curvill=?,curarea=?,curx=?,cury=?,function_type=2 WHERE UID=?", info.Village, info.Area, info.X, info.Y, info.UID)
	return err
}

func (r *SQLRepository) PrepareDisjointPosition(info robotcap.Info, cost int) error {
	if cost <= 0 {
		cost = 500
	}
	_, err := r.Exec("UPDATE d_starsky.Dummylist SET curvill=?,curarea=?,curx=?,cury=?,function_type=3,discost=? WHERE UID=?",
		info.Village, info.Area, info.X, info.Y, cost, info.UID)
	return err
}

func (r *SQLRepository) RestoreDummyNormal(info robotcap.Info) error {
	_, err := r.Exec("UPDATE d_starsky.Dummylist SET curvill=?,curarea=?,curx=?,cury=?,function_type=0 WHERE UID=?",
		info.Village, info.Area, info.X, info.Y, info.UID)
	return err
}

func (r *SQLRepository) SyncCharacterVillage(cid int, village int) (int, error) {
	if cid <= 0 {
		return 0, fmt.Errorf("invalid cid %d", cid)
	}
	if _, err := r.Exec("UPDATE taiwan_cain.charac_info SET village=? WHERE charac_no=?", village, cid); err != nil {
		return 0, fmt.Errorf("update charac_info: %w", err)
	}
	if _, err := r.Exec("UPDATE taiwan_cain.charac_stat SET village=?,village_prev=? WHERE charac_no=?", village, village, cid); err != nil {
		return 0, fmt.Errorf("update charac_stat: %w", err)
	}
	var infoVillage, statVillage, statPrev int
	if err := r.QueryRow(`SELECT ci.village,cs.village,cs.village_prev
		FROM taiwan_cain.charac_info ci JOIN taiwan_cain.charac_stat cs ON cs.charac_no=ci.charac_no
		WHERE ci.charac_no=?`, cid).Scan(&infoVillage, &statVillage, &statPrev); err != nil {
		return 0, fmt.Errorf("verify charac village: %w", err)
	}
	if infoVillage != village || statVillage != village || statPrev != village {
		return 0, fmt.Errorf("verify charac village mismatch want=%d info=%d stat=%d prev=%d", village, infoVillage, statVillage, statPrev)
	}
	return statPrev, nil
}

func (r *SQLRepository) LoadInventory(cid int) ([]byte, error) {
	var raw []byte
	if err := r.QueryRow("SELECT UNCOMPRESS(inventory) FROM taiwan_cain_2nd.inventory WHERE charac_no=?", cid).Scan(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (r *SQLRepository) SaveInventory(cid int, capacity int, raw []byte) error {
	_, err := r.Exec("UPDATE taiwan_cain_2nd.inventory SET inventory_capacity=?,inventory=? WHERE charac_no=?", capacity, equipcap.CompressRaw(raw), cid)
	return err
}

func (r *SQLRepository) SaveInventoryRaw(cid int, raw []byte) error {
	_, err := r.Exec("UPDATE taiwan_cain_2nd.inventory SET inventory=? WHERE charac_no=?", equipcap.CompressRaw(raw), cid)
	return err
}

func (r *SQLRepository) ReplaceStoreStall(uid int, title string, items []storecap.StallItem) (storecap.StallResult, error) {
	if uid <= 0 {
		return storecap.StallResult{}, fmt.Errorf("invalid store stall uid=%d", uid)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return storecap.StallResult{}, fmt.Errorf("begin store stall replacement uid=%d: %w", uid, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.Exec("DELETE FROM d_starsky.Robot_stall WHERE UID=? AND function_type=2", uid); err != nil {
		return storecap.StallResult{}, fmt.Errorf("delete store stall uid=%d: %w", uid, err)
	}
	if query, args := buildStoreStallInsert(uid, items); query != "" {
		if _, err := tx.Exec(query, args...); err != nil {
			return storecap.StallResult{}, fmt.Errorf("insert store stall uid=%d items=%d: %w", uid, len(items), err)
		}
	}
	if _, err := tx.Exec("DELETE FROM d_starsky.Robot_stall_config WHERE UID=? AND function_type=2 AND cfg_type=3", uid); err != nil {
		return storecap.StallResult{}, fmt.Errorf("delete store stall config uid=%d: %w", uid, err)
	}
	if _, err := tx.Exec("INSERT INTO d_starsky.Robot_stall_config (cfg_content,cfg_type,UID,function_type,state) VALUES (?,3,?,2,1)", title, uid); err != nil {
		return storecap.StallResult{}, fmt.Errorf("insert store stall config uid=%d: %w", uid, err)
	}
	var result storecap.StallResult
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM d_starsky.Robot_stall WHERE UID=? AND function_type=2 AND state=1),
		(SELECT COUNT(*) FROM d_starsky.Robot_stall_config WHERE UID=? AND function_type=2 AND cfg_type=3 AND state=1)`, uid, uid).Scan(&result.StallRows, &result.ConfigRows); err != nil {
		return storecap.StallResult{}, fmt.Errorf("verify store stall config uid=%d: %w", uid, err)
	}
	if result.StallRows != len(items) || result.ConfigRows != 1 {
		return storecap.StallResult{}, fmt.Errorf("verify store stall mismatch uid=%d items=%d stall_rows=%d config_rows=%d", uid, len(items), result.StallRows, result.ConfigRows)
	}
	if err := tx.Commit(); err != nil {
		return storecap.StallResult{}, fmt.Errorf("commit store stall replacement uid=%d: %w", uid, err)
	}
	committed = true
	return result, nil
}

func buildStoreStallInsert(uid int, items []storecap.StallItem) (string, []interface{}) {
	if uid <= 0 || len(items) == 0 {
		return "", nil
	}
	values := make([]string, len(items))
	args := make([]interface{}, 0, len(items)*5)
	for index, item := range items {
		values[index] = "(?,?,?,?,1,?)"
		args = append(args, item.ItemID, item.Price, item.Count, 2, uid)
	}
	return "INSERT INTO d_starsky.Robot_stall (Trade_item,price,item_number,function_type,state,UID) VALUES " + strings.Join(values, ","), args
}

func (r *SQLRepository) AccountOnline(uid int) (bool, error) {
	if uid <= 0 {
		return false, fmt.Errorf("invalid account uid=%d", uid)
	}
	var online int
	if err := r.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM taiwan_login.login_account_3
		WHERE m_id=? AND login_status=1
	)`, uid).Scan(&online); err != nil {
		return false, fmt.Errorf("query account online uid=%d: %w", uid, err)
	}
	return online != 0, nil
}

func (r *SQLRepository) EnsureStorePermission(uid, cid int) (storecap.PermissionStatus, error) {
	if uid <= 0 || cid <= 0 {
		return storecap.PermissionStatus{}, fmt.Errorf("invalid store permission uid=%d cid=%d", uid, cid)
	}
	if err := r.storePermissionCooldownError(time.Now()); err != nil {
		return storecap.PermissionStatus{}, err
	}
	if _, err := r.ClearTradePunish(uid); err != nil {
		return storecap.PermissionStatus{}, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return storecap.PermissionStatus{}, fmt.Errorf("begin store permission uid=%d cid=%d: %w", uid, cid, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	steps := []struct {
		query string
		args  []interface{}
	}{
		{"DELETE FROM taiwan_login.member_premium WHERE m_id=?", []interface{}{uid}},
		{"INSERT INTO taiwan_login.member_premium(pre_type,m_id,service_start,service_end,event_id,server_id) VALUES(8,?,NOW(),'2030-12-31 00:00:00',50002,0)", []interface{}{uid}},
		{"DELETE FROM d_taiwan.member_miles WHERE m_id=?", []interface{}{uid}},
		{"INSERT INTO d_taiwan.member_miles(m_id,miles) VALUES(?,7)", []interface{}{uid}},
		{"DELETE FROM taiwan_prod.prod_buy_user WHERE m_id=?", []interface{}{uid}},
		{"INSERT INTO taiwan_prod.prod_buy_user(m_id,user_id,sex,birthday,first_buy_time,last_buy_time) VALUES(?,?,0,'',NOW(),NOW())", []interface{}{uid, strconv.Itoa(uid)}},
		{"DELETE FROM taiwan_prod.pu_user_list WHERE m_id=?", []interface{}{uid}},
		{"INSERT INTO taiwan_prod.pu_user_list(m_id) VALUES(?)", []interface{}{uid}},
		{"DELETE FROM taiwan_login.dnf_event_entry WHERE event_id=50002 AND m_id=? AND charac_no=?", []interface{}{uid, cid}},
		{"INSERT INTO taiwan_login.dnf_event_entry(event_id,m_id,occ_date,server_id,charac_no,obtain_date) VALUES(50002,?,NOW(),0,?,NOW())", []interface{}{uid, cid}},
	}
	for _, step := range steps {
		if _, err := tx.Exec(step.query, step.args...); err != nil {
			r.recordStorePermissionFailure(err, time.Now())
			return storecap.PermissionStatus{}, err
		}
	}
	var status storecap.PermissionStatus
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM taiwan_login.member_premium WHERE event_id=50002 AND pre_type=8 AND m_id=? AND service_end>NOW()),
		(SELECT COUNT(*) FROM d_taiwan.member_miles WHERE m_id=? AND miles>=7),
		(SELECT COUNT(*) FROM taiwan_prod.prod_buy_user WHERE m_id=?),
		(SELECT COUNT(*) FROM taiwan_prod.pu_user_list WHERE m_id=?),
		(SELECT COUNT(*) FROM taiwan_login.dnf_event_entry WHERE event_id=50002 AND m_id=? AND charac_no=?)`,
		uid, uid, uid, uid, uid, cid,
	).Scan(&status.Premium, &status.Miles, &status.ProdUser, &status.PUUser, &status.EventEntry); err != nil {
		r.recordStorePermissionFailure(err, time.Now())
		return storecap.PermissionStatus{}, fmt.Errorf("verify store permission uid=%d cid=%d: %w", uid, cid, err)
	}
	if !storePermissionReady(status) {
		return status, fmt.Errorf("verify store permission mismatch uid=%d cid=%d premium=%d miles=%d prod_user=%d pu_user=%d event_entry=%d",
			uid, cid, status.Premium, status.Miles, status.ProdUser, status.PUUser, status.EventEntry)
	}
	if err := tx.Commit(); err != nil {
		r.recordStorePermissionFailure(err, time.Now())
		return storecap.PermissionStatus{}, fmt.Errorf("commit store permission uid=%d cid=%d: %w", uid, cid, err)
	}
	committed = true
	r.clearStorePermissionCooldown()
	return status, nil
}

const storePermissionDatabaseCooldown = 60 * time.Second

func (r *SQLRepository) storePermissionCooldownError(now time.Time) error {
	r.storePermissionMu.Lock()
	defer r.storePermissionMu.Unlock()
	if r.storePermissionRetryAt.IsZero() || !now.Before(r.storePermissionRetryAt) {
		return nil
	}
	return fmt.Errorf("store permission database cooldown until %s: %s", r.storePermissionRetryAt.Format(time.RFC3339), r.storePermissionCause)
}

func (r *SQLRepository) recordStorePermissionFailure(err error, now time.Time) {
	if err == nil || (!strings.Contains(strings.ToLower(err.Error()), "duplicate") && !strings.Contains(err.Error(), "145")) {
		return
	}
	r.storePermissionMu.Lock()
	r.storePermissionRetryAt = now.Add(storePermissionDatabaseCooldown)
	r.storePermissionCause = err.Error()
	r.storePermissionMu.Unlock()
}

func (r *SQLRepository) clearStorePermissionCooldown() {
	r.storePermissionMu.Lock()
	r.storePermissionRetryAt = time.Time{}
	r.storePermissionCause = ""
	r.storePermissionMu.Unlock()
}

func storePermissionReady(status storecap.PermissionStatus) bool {
	return status.Premium > 0 && status.Miles > 0 && status.ProdUser > 0 && status.PUUser > 0 && status.EventEntry > 0
}

func (r *SQLRepository) EnsureDisjointProfession(info robotcap.Info) error {
	if info.UID <= 0 || info.CID <= 0 {
		return fmt.Errorf("invalid disjoint profession identity uid=%d cid=%d", info.UID, info.CID)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var uid, cid, expertJob int
	if err := tx.QueryRow("SELECT uid,cid FROM d_starsky.robot_registry WHERE uid=? AND cid=? FOR UPDATE", info.UID, info.CID).Scan(&uid, &cid); err != nil {
		return fmt.Errorf("lock disjoint registry uid=%d cid=%d: %w", info.UID, info.CID, err)
	}
	if err := tx.QueryRow("SELECT expert_job FROM taiwan_cain.charac_info WHERE charac_no=? AND m_id=? AND delete_flag=0 FOR UPDATE", info.CID, info.UID).Scan(&expertJob); err != nil {
		return fmt.Errorf("lock disjoint charac_info uid=%d cid=%d: %w", info.UID, info.CID, err)
	}
	if expertJob != 0 && expertJob != 3 {
		return fmt.Errorf("unsupported existing expert job %d for uid=%d cid=%d", expertJob, info.UID, info.CID)
	}
	var expertExp int
	if err := tx.QueryRow("SELECT expert_job_exp FROM taiwan_cain.charac_stat WHERE charac_no=? FOR UPDATE", info.CID).Scan(&expertExp); err != nil {
		return fmt.Errorf("lock disjoint charac_stat uid=%d cid=%d: %w", info.UID, info.CID, err)
	}
	if _, err := tx.Exec("UPDATE taiwan_cain.charac_info SET expert_job=3 WHERE charac_no=? AND m_id=?", info.CID, info.UID); err != nil {
		return fmt.Errorf("update disjoint charac_info uid=%d cid=%d: %w", info.UID, info.CID, err)
	}
	// Keep the exact profession threshold from the first high-success workflow.
	// Jumping a new profession directly to the later 2054 maximum made this
	// server family discard the conversion during CMD 238 handling.
	if expertExp < 800 {
		if _, err := tx.Exec("UPDATE taiwan_cain.charac_stat SET expert_job_exp=800 WHERE charac_no=?", info.CID); err != nil {
			return fmt.Errorf("update disjoint charac_stat uid=%d cid=%d: %w", info.UID, info.CID, err)
		}
	}
	var existing int
	err = tx.QueryRow("SELECT 1 FROM taiwan_cain.charac_expert_job WHERE charac_no=? FOR UPDATE", info.CID).Scan(&existing)
	switch err {
	case nil:
		if _, err := tx.Exec(`UPDATE taiwan_cain.charac_expert_job
			SET expert_job_info=IF(expert_job_info>0,expert_job_info,999998),
			    expert_job_info_ex=IF(expert_job_info_ex>0,expert_job_info_ex,7),
			    recipe=IF(LENGTH(recipe)>=30,recipe,?)
			WHERE charac_no=?`, make([]byte, 30), info.CID); err != nil {
			return fmt.Errorf("update disjoint expert row uid=%d cid=%d: %w", info.UID, info.CID, err)
		}
	case sql.ErrNoRows:
		if _, err := tx.Exec(`INSERT INTO taiwan_cain.charac_expert_job
			(charac_no,expert_job_giveup_cnt,expert_job_info,expert_job_info_ex,recipe)
			VALUES (?,0,999998,7,?)`, info.CID, make([]byte, 30)); err != nil {
			return fmt.Errorf("insert disjoint expert row uid=%d cid=%d: %w", info.UID, info.CID, err)
		}
	default:
		return fmt.Errorf("lock disjoint expert row uid=%d cid=%d: %w", info.UID, info.CID, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *SQLRepository) RevokeStorePermission(uid, cid int) error {
	if uid <= 0 {
		return nil
	}
	steps := []struct {
		table string
		query string
		args  []interface{}
	}{
		{"d_starsky.Robot_stall", "DELETE FROM d_starsky.Robot_stall WHERE UID=? AND function_type=2", []interface{}{uid}},
		{"d_starsky.Robot_stall_config", "DELETE FROM d_starsky.Robot_stall_config WHERE UID=? AND function_type=2", []interface{}{uid}},
		{"d_starsky.Dummylist", "UPDATE d_starsky.Dummylist SET function_type=0 WHERE UID=?", []interface{}{uid}},
		{"taiwan_login.member_premium", "DELETE FROM taiwan_login.member_premium WHERE m_id=? AND pre_type=8 AND event_id=50002", []interface{}{uid}},
		{"taiwan_login.dnf_event_entry", "DELETE FROM taiwan_login.dnf_event_entry WHERE event_id=50002 AND m_id=?", []interface{}{uid}},
	}
	for _, step := range steps {
		exists, err := r.TableExists(step.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := r.Exec(step.query, step.args...); err != nil {
			return err
		}
	}
	return nil
}
