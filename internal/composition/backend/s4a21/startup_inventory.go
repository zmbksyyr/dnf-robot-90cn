package s4a21

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/foundation/charset"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

// startupDeleteLogLimit bounds the per-account deletion trace. Larger cleanups
// stay traceable through the aggregate counters and the DELETE_MORE line.
const startupDeleteLogLimit = 50

type StartupInventory struct {
	Robots            []robotcap.Info
	Identities        []robotstate.Identity
	ScannedAccounts   int
	DeletedAccounts   int
	DeletedCharacters int
}

type SQLiteStartupInventory struct {
	DatabasePath  string
	AccountPrefix string
	Config        robotconfig.RuntimeConfig
	Equipment     []shared.EquipmentCatalogItem
	JobGrows      map[int][]int
	StatTables    map[int]capabilitypvf.CharacterStatTables
}

type startupAccount struct {
	id         int
	uid        int
	name       string
	characters []startupCharacter
}

type startupCharacter struct {
	id, job, grow, level int
	village, area, x, y  int
	slot, deleteFlag     int
	nameRaw              []byte
}

type startupInventoryIndex struct {
	cores         map[int]map[int][]byte
	avatarDetails map[int]map[int]struct{}
	creatureKeys  map[int]map[int]struct{}
}

type startupSQLiteConn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ScanAndClean makes the game database the only durable source of S4A21 robot
// identity. Accounts are owned only when their name is exactly prefix+UID and
// the UID is inside the configured segment. Any owned account that is not a
// complete, usable one-character robot is removed.
//
// The scan is read-only. Deletion runs in a separate short write transaction
// that re-verifies each candidate against the recorded character set, so the
// game server is not blocked behind a full-table scan and no account is
// deleted when it changed between the scan and the cleanup.
func (s SQLiteStartupInventory) ScanAndClean(ctx context.Context) (StartupInventory, error) {
	var result StartupInventory
	ctx, cancel := context.WithTimeout(ctx, s4a21PersistenceTimeout)
	defer cancel()
	if strings.TrimSpace(s.DatabasePath) == "" {
		return result, fmt.Errorf("S4A21 startup inventory database path is required")
	}
	prefix := strings.TrimSpace(s.AccountPrefix)
	if prefix == "" {
		return result, fmt.Errorf("S4A21 startup inventory account prefix is required")
	}
	db, err := sql.Open("sqlite", s.DatabasePath)
	if err != nil {
		return result, fmt.Errorf("open S4A21 startup inventory: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	conn, err := db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("connect S4A21 startup inventory: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return result, fmt.Errorf("configure S4A21 startup inventory: %w", err)
	}

	// Phase 1: read-only scan.
	accounts, characterIDs, err := s.readOwnedAccounts(ctx, conn, prefix)
	if err != nil {
		return result, err
	}
	result.ScannedAccounts = len(accounts)
	index, err := readStartupInventoryIndex(ctx, conn, prefix, characterIDs)
	if err != nil {
		return result, err
	}
	items := equipmentByID(s.Equipment)
	invalidAccounts := make([]startupAccount, 0)
	for _, account := range accounts {
		if len(account.characters) != 1 || !s.characterCompliant(account.characters[0], index, items) {
			invalidAccounts = append(invalidAccounts, account)
			continue
		}
		character := account.characters[0]
		name := strings.TrimSpace(charset.DecodeWireName(character.nameRaw))
		robot := robotcap.Info{
			UID: account.uid, CID: character.id, Name: name,
			Level: character.level, Job: character.job, Grow: character.grow,
			Village: character.village, Area: character.area, X: character.x, Y: character.y,
		}
		slot := uint16(character.slot)
		result.Robots = append(result.Robots, robot)
		result.Identities = append(result.Identities, robotstate.Identity{
			Backend: BackendID, Account: account.name, CharacterName: name, Slot: &slot,
		})
	}

	// Phase 2: short write transaction.
	if len(invalidAccounts) > 0 {
		if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
			return result, fmt.Errorf("begin S4A21 startup inventory: %w", err)
		}
		deleted := false
		defer func() {
			if !deleted {
				_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
			}
		}()
		toDelete, deletedCharacters, err := s.verifyInvalidAccounts(ctx, conn, prefix, invalidAccounts)
		if err != nil {
			return result, err
		}
		for index, account := range toDelete {
			if index >= startupDeleteLogLimit {
				break
			}
			reason := "incomplete account"
			if len(account.characters) == 1 {
				reason = "character non-compliant"
			}
			foundationlog.Robotf("STARTUP_INVENTORY_DELETE account=%s uid=%d characters=%d reason=%s\n",
				account.name, account.uid, len(account.characters), reason)
		}
		if omitted := len(toDelete) - startupDeleteLogLimit; omitted > 0 {
			foundationlog.Robotf("STARTUP_INVENTORY_DELETE_MORE accounts=%d\n", omitted)
		}
		accountIDs := make([]int, 0, len(toDelete))
		for _, account := range toDelete {
			accountIDs = append(accountIDs, account.id)
		}
		if err := deleteStartupAccounts(ctx, conn, accountIDs); err != nil {
			return result, err
		}
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("commit S4A21 startup inventory: %w", err)
		}
		deleted = true
		result.DeletedAccounts = len(toDelete)
		result.DeletedCharacters = deletedCharacters
	}
	sort.Slice(result.Robots, func(i, j int) bool { return result.Robots[i].UID < result.Robots[j].UID })
	sort.Slice(result.Identities, func(i, j int) bool { return result.Identities[i].Account < result.Identities[j].Account })
	return result, nil
}

// verifyInvalidAccounts re-reads each invalid candidate inside the write
// transaction and returns only accounts whose identity and character set still
// match the scan. An account that changed in the meantime is skipped and left
// for the next startup instead of being deleted with stale evidence.
func (s SQLiteStartupInventory) verifyInvalidAccounts(ctx context.Context, conn startupSQLiteConn, prefix string, candidates []startupAccount) ([]startupAccount, int, error) {
	const batchSize = 200
	toDelete := make([]startupAccount, 0, len(candidates))
	deletedCharacters := 0
	for start := 0; start < len(candidates); start += batchSize {
		end := start + batchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		args := make([]any, end-start)
		placeholders := make([]string, end-start)
		for index, candidate := range candidates[start:end] {
			args[index] = candidate.id
			placeholders[index] = "?"
		}
		query := `SELECT a.account_id,a.m_id,c.character_id,c.name,c.job,c.grow_type,c.level,
c.town_id,c.area_id,c.pos_x,c.pos_y,c.slot_index,c.delete_flag FROM accounts a
LEFT JOIN characters c ON c.account_id=a.account_id
WHERE a.account_id IN (` + strings.Join(placeholders, ",") + `)`
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("re-check S4A21 startup candidates: %w", err)
		}
		current := make(map[int]*startupAccount, end-start)
		for rows.Next() {
			var accountID int
			var accountName string
			var characterID, job, grow, level, village, area, x, y, slot, deleteFlag sql.NullInt64
			var nameRaw []byte
			if err := rows.Scan(&accountID, &accountName, &characterID, &nameRaw, &job, &grow, &level,
				&village, &area, &x, &y, &slot, &deleteFlag); err != nil {
				rows.Close()
				return nil, 0, fmt.Errorf("read S4A21 startup candidate: %w", err)
			}
			account := current[accountID]
			if account == nil {
				account = &startupAccount{id: accountID, name: accountName}
				current[accountID] = account
			}
			if characterID.Valid {
				account.characters = append(account.characters, startupCharacter{
					id: int(characterID.Int64), nameRaw: append([]byte(nil), nameRaw...),
					job: int(job.Int64), grow: int(grow.Int64), level: int(level.Int64),
					village: int(village.Int64), area: int(area.Int64), x: int(x.Int64), y: int(y.Int64),
					slot: int(slot.Int64), deleteFlag: int(deleteFlag.Int64),
				})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("scan S4A21 startup candidates: %w", err)
		}
		rows.Close()
		characterIDs := make([]int, 0)
		for _, account := range current {
			if len(account.characters) == 1 {
				characterIDs = append(characterIDs, account.characters[0].id)
			}
		}
		currentIndex, err := readStartupInventoryIndexForIDs(ctx, conn, characterIDs)
		if err != nil {
			return nil, 0, err
		}
		items := equipmentByID(s.Equipment)
		for index := range candidates[start:end] {
			candidate := candidates[start+index]
			state := current[candidate.id]
			if state == nil {
				foundationlog.Robotf("STARTUP_INVENTORY_SKIP_CHANGED account=%s reason=account disappeared during startup scan\n", candidate.name)
				continue
			}
			uid, owned := strictRobotUID(state.name, prefix)
			if !owned || uid != candidate.uid || !sameStartupCharacters(state.characters, candidate.characters) {
				foundationlog.Robotf("STARTUP_INVENTORY_SKIP_CHANGED account=%s reason=candidate changed during startup scan\n", candidate.name)
				continue
			}
			if len(state.characters) == 1 && s.characterCompliant(state.characters[0], currentIndex, items) {
				foundationlog.Robotf("STARTUP_INVENTORY_SKIP_CHANGED account=%s reason=candidate is now compliant\n", candidate.name)
				continue
			}
			toDelete = append(toDelete, candidate)
			deletedCharacters += len(candidate.characters)
		}
	}
	return toDelete, deletedCharacters, nil
}

func sameStartupCharacters(current, recorded []startupCharacter) bool {
	if len(current) != len(recorded) {
		return false
	}
	currentIDs := make([]int, 0, len(current))
	for _, character := range current {
		currentIDs = append(currentIDs, character.id)
	}
	recordedIDs := make([]int, 0, len(recorded))
	for _, character := range recorded {
		recordedIDs = append(recordedIDs, character.id)
	}
	sort.Ints(currentIDs)
	sort.Ints(recordedIDs)
	for index := range currentIDs {
		if currentIDs[index] != recordedIDs[index] {
			return false
		}
	}
	return true
}

// readStartupInventoryIndexForIDs limits the write-transaction recheck to
// candidate characters. The initial inventory scan remains read-only.
func readStartupInventoryIndexForIDs(ctx context.Context, conn startupSQLiteConn, characterIDs []int) (startupInventoryIndex, error) {
	index := startupInventoryIndex{
		cores: make(map[int]map[int][]byte), avatarDetails: make(map[int]map[int]struct{}), creatureKeys: make(map[int]map[int]struct{}),
	}
	const batchSize = 200
	for start := 0; start < len(characterIDs); start += batchSize {
		end := min(start+batchSize, len(characterIDs))
		args := make([]any, end-start)
		places := make([]string, end-start)
		for i, id := range characterIDs[start:end] {
			args[i] = id
			places[i] = "?"
		}
		ids := strings.Join(places, ",")
		rows, err := conn.QueryContext(ctx, `SELECT character_id,slot_index,item_core FROM character_inventory_items
WHERE list_type=? AND slot_index BETWEEN 0 AND 28 AND character_id IN (`+ids+`)`, append([]any{a21ListTypeEquipment}, args...)...)
		if err != nil {
			return index, fmt.Errorf("re-check S4A21 equipped items: %w", err)
		}
		for rows.Next() {
			var id, slot int
			var core []byte
			if err := rows.Scan(&id, &slot, &core); err != nil {
				rows.Close()
				return index, err
			}
			if index.cores[id] == nil {
				index.cores[id] = make(map[int][]byte)
			}
			index.cores[id][slot] = append([]byte(nil), core...)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return index, err
		}
		rows.Close()

		rows, err = conn.QueryContext(ctx, `SELECT character_id,item_uid FROM character_avatar_detail WHERE character_id IN (`+ids+`)`, args...)
		if err != nil {
			return index, fmt.Errorf("re-check S4A21 avatar details: %w", err)
		}
		for rows.Next() {
			var id, itemUID int
			if err := rows.Scan(&id, &itemUID); err != nil {
				rows.Close()
				return index, err
			}
			if index.avatarDetails[id] == nil {
				index.avatarDetails[id] = make(map[int]struct{})
			}
			index.avatarDetails[id][itemUID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return index, err
		}
		rows.Close()

		rows, err = conn.QueryContext(ctx, `SELECT character_id,creature_key FROM character_creatures WHERE character_id IN (`+ids+`)`, args...)
		if err != nil {
			return index, fmt.Errorf("re-check S4A21 creature details: %w", err)
		}
		for rows.Next() {
			var id, creatureKey int
			if err := rows.Scan(&id, &creatureKey); err != nil {
				rows.Close()
				return index, err
			}
			if index.creatureKeys[id] == nil {
				index.creatureKeys[id] = make(map[int]struct{})
			}
			index.creatureKeys[id][creatureKey] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return index, err
		}
		rows.Close()
	}
	return index, nil
}

func deleteStartupAccounts(ctx context.Context, conn startupSQLiteConn, accountIDs []int) error {
	const batchSize = 500
	for start := 0; start < len(accountIDs); start += batchSize {
		end := start + batchSize
		if end > len(accountIDs) {
			end = len(accountIDs)
		}
		args := make([]any, end-start)
		placeholders := make([]string, end-start)
		for index, accountID := range accountIDs[start:end] {
			args[index] = accountID
			placeholders[index] = "?"
		}
		query := `DELETE FROM accounts WHERE account_id IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := conn.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete invalid S4A21 robot accounts batch=%d..%d: %w", start, end, err)
		}
	}
	return nil
}

func (s SQLiteStartupInventory) readOwnedAccounts(ctx context.Context, conn startupSQLiteConn, prefix string) ([]startupAccount, map[int]struct{}, error) {
	rows, err := conn.QueryContext(ctx, `SELECT a.account_id,a.m_id,
c.character_id,c.name,c.job,c.grow_type,c.level,c.town_id,c.area_id,c.pos_x,c.pos_y,c.slot_index,c.delete_flag
FROM accounts a LEFT JOIN characters c ON c.account_id=a.account_id
WHERE a.m_id LIKE ? ORDER BY a.account_id,c.character_id`, prefix+"%")
	if err != nil {
		return nil, nil, fmt.Errorf("scan S4A21 robot accounts: %w", err)
	}
	defer rows.Close()
	byID := make(map[int]*startupAccount)
	ordered := make([]*startupAccount, 0)
	characterIDs := make(map[int]struct{})
	for rows.Next() {
		var accountID int
		var accountName string
		var characterID, job, grow, level, village, area, x, y, slot, deleteFlag sql.NullInt64
		var nameRaw []byte
		if err := rows.Scan(&accountID, &accountName, &characterID, &nameRaw, &job, &grow, &level, &village, &area, &x, &y, &slot, &deleteFlag); err != nil {
			return nil, nil, fmt.Errorf("read S4A21 robot account row: %w", err)
		}
		uid, owned := ownedRobotUID(accountName, prefix, s.Config.RobotUIDStart, s.Config.RobotUIDEnd)
		if !owned {
			continue
		}
		account := byID[accountID]
		if account == nil {
			account = &startupAccount{id: accountID, uid: uid, name: accountName}
			byID[accountID] = account
			ordered = append(ordered, account)
		}
		if !characterID.Valid {
			continue
		}
		character := startupCharacter{
			id: int(characterID.Int64), nameRaw: append([]byte(nil), nameRaw...),
			job: int(job.Int64), grow: int(grow.Int64), level: int(level.Int64),
			village: int(village.Int64), area: int(area.Int64), x: int(x.Int64), y: int(y.Int64),
			slot: int(slot.Int64), deleteFlag: int(deleteFlag.Int64),
		}
		account.characters = append(account.characters, character)
		characterIDs[character.id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan S4A21 robot accounts: %w", err)
	}
	accounts := make([]startupAccount, 0, len(ordered))
	for _, account := range ordered {
		accounts = append(accounts, *account)
	}
	return accounts, characterIDs, nil
}

func readStartupInventoryIndex(ctx context.Context, conn startupSQLiteConn, prefix string, characterIDs map[int]struct{}) (startupInventoryIndex, error) {
	index := startupInventoryIndex{
		cores: make(map[int]map[int][]byte), avatarDetails: make(map[int]map[int]struct{}), creatureKeys: make(map[int]map[int]struct{}),
	}
	rows, err := conn.QueryContext(ctx, `SELECT i.character_id,i.slot_index,i.item_core
FROM character_inventory_items i JOIN characters c ON c.character_id=i.character_id
JOIN accounts a ON a.account_id=c.account_id
WHERE a.m_id LIKE ? AND i.list_type=? AND i.slot_index BETWEEN 0 AND 28`, prefix+"%", a21ListTypeEquipment)
	if err != nil {
		return index, fmt.Errorf("scan S4A21 equipped items: %w", err)
	}
	for rows.Next() {
		var characterID, slot int
		var core []byte
		if err := rows.Scan(&characterID, &slot, &core); err != nil {
			rows.Close()
			return index, err
		}
		if _, wanted := characterIDs[characterID]; !wanted {
			continue
		}
		if index.cores[characterID] == nil {
			index.cores[characterID] = make(map[int][]byte)
		}
		index.cores[characterID][slot] = append([]byte(nil), core...)
	}
	if err := rows.Close(); err != nil {
		return index, err
	}
	rows, err = conn.QueryContext(ctx, `SELECT d.character_id,d.item_uid FROM character_avatar_detail d
JOIN characters c ON c.character_id=d.character_id JOIN accounts a ON a.account_id=c.account_id
WHERE a.m_id LIKE ?`, prefix+"%")
	if err != nil {
		return index, fmt.Errorf("scan S4A21 avatar details: %w", err)
	}
	for rows.Next() {
		var characterID, itemUID int
		if err := rows.Scan(&characterID, &itemUID); err != nil {
			rows.Close()
			return index, err
		}
		if _, wanted := characterIDs[characterID]; !wanted {
			continue
		}
		if index.avatarDetails[characterID] == nil {
			index.avatarDetails[characterID] = make(map[int]struct{})
		}
		index.avatarDetails[characterID][itemUID] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return index, err
	}
	rows, err = conn.QueryContext(ctx, `SELECT p.character_id,p.creature_key FROM character_creatures p
JOIN characters c ON c.character_id=p.character_id JOIN accounts a ON a.account_id=c.account_id
WHERE a.m_id LIKE ?`, prefix+"%")
	if err != nil {
		return index, fmt.Errorf("scan S4A21 creature details: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var characterID, creatureKey int
		if err := rows.Scan(&characterID, &creatureKey); err != nil {
			return index, err
		}
		if _, wanted := characterIDs[characterID]; !wanted {
			continue
		}
		if index.creatureKeys[characterID] == nil {
			index.creatureKeys[characterID] = make(map[int]struct{})
		}
		index.creatureKeys[characterID][creatureKey] = struct{}{}
	}
	return index, rows.Err()
}

func (s SQLiteStartupInventory) characterCompliant(character startupCharacter, index startupInventoryIndex, items map[int]shared.EquipmentCatalogItem) bool {
	if character.deleteFlag != 0 || character.level < s.Config.LevelMin || character.level > s.Config.LevelMax || character.slot < 0 || character.slot > 65535 {
		return false
	}
	name := strings.TrimSpace(charset.DecodeWireName(character.nameRaw))
	encodedName, err := charset.EncodeGBKString(name)
	if err != nil || len(encodedName) < 2 || len(encodedName) > 18 {
		return false
	}
	if !configuredIntValue(s.Config.Jobs, character.job) || !s.growCompliant(character) {
		return false
	}
	cores := index.cores[character.id]
	equipSlots := s.Config.EquipSlots
	if len(equipSlots) == 0 {
		equipSlots = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	equipmentSetCounts := make(map[string]int)
	equipmentCount := 0
	for _, commonSlot := range equipSlots {
		item, ok := startupCoreItem(cores[commonSlot+11], a21ItemKindEquipment, items)
		if !ok || item.ItemType != commonSlot || !equipmentcap.UsableByJob(item.UseJob, character.job) {
			return false
		}
		if commonSlot < 11 && item.Level > character.level {
			return false
		}
		equipmentCount++
		for _, setKey := range strings.Split(item.SetKey, "|") {
			if setKey = strings.TrimSpace(setKey); setKey != "" {
				equipmentSetCounts[setKey]++
			}
		}
	}
	avatarSlots := s.Config.AvatarSlots
	if len(avatarSlots) == 0 {
		avatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	}
	avatarCount := 0
	avatarSetCounts := make(map[string]int)
	for _, slot := range avatarSlots {
		core := cores[slot]
		item, ok := startupCoreItem(core, a21ItemKindAvatar, items)
		if !ok || item.ItemType != slot+20 || !equipmentcap.AvatarRenderable(item) || !equipmentcap.AvatarUsableByJob(item, s4a21AvatarJob(character.job)) {
			continue
		}
		itemUID := int(binary.LittleEndian.Uint32(core[5:9]))
		if _, ok := index.avatarDetails[character.id][itemUID]; !ok {
			continue
		}
		avatarCount++
		for _, setKey := range strings.Split(item.SetKey, "|") {
			if setKey = strings.TrimSpace(setKey); setKey != "" {
				avatarSetCounts[setKey]++
			}
		}
	}
	if avatarCount < s.Config.MinAvatarSlots {
		return false
	}
	if s.Config.PreferEquipSets && maxSetCount(equipmentSetCounts) < requiredSetCoverage(equipmentCount, s.Config.EquipSetMinSlots, 5) {
		return false
	}
	if s.Config.PreferAvatarSets && maxSetCount(avatarSetCounts) < requiredSetCoverage(avatarCount, s.Config.AvatarSetMinSlots, 6) {
		return false
	}
	return s.petCompliant(character.id, cores, index.creatureKeys[character.id], items)
}

func (s SQLiteStartupInventory) petCompliant(characterID int, cores map[int][]byte, creatureKeys map[int]struct{}, items map[int]shared.EquipmentCatalogItem) bool {
	creatureCore := cores[a21CreatureSlot]
	if !s.Config.PetEnabled {
		return len(creatureCore) == 0 && len(creatureKeys) == 0 && !hasStartupArtifactCore(cores)
	}
	if len(creatureCore) == 0 {
		return len(creatureKeys) == 0 && !hasStartupArtifactCore(cores)
	}
	pet, ok := startupCoreItem(creatureCore, a21ItemKindCreature, items)
	if !ok || pet.ItemType != 30 || len(creatureCore) < 9 {
		return false
	}
	creatureUID := int(binary.LittleEndian.Uint32(creatureCore[5:9]))
	if creatureUID <= 0 {
		return false
	}
	if _, ok := creatureKeys[creatureUID]; !ok || len(creatureKeys) != 1 {
		return false
	}
	if !s.Config.PetArtifactEnabled {
		return true
	}
	artifactTypes := s.Config.PetArtifactSlots
	if len(artifactTypes) == 0 {
		artifactTypes = []int{31, 32, 33}
	}
	artifactCount := 0
	for _, itemType := range artifactTypes {
		slot := a21ArtifactSlotBase + itemType - 31
		item, ok := startupCoreItem(cores[slot], a21ItemKindArtifact, items)
		if ok && item.ItemType == itemType && equipmentcap.PetArtifactRenderable(item) {
			artifactCount++
		}
	}
	return artifactCount >= s.Config.MinPetArtifactSlots && artifactCount <= s.Config.MaxPetArtifactSlots
}

func startupCoreItem(core []byte, kind byte, items map[int]shared.EquipmentCatalogItem) (shared.EquipmentCatalogItem, bool) {
	if len(core) < 9 || core[0] != kind {
		return shared.EquipmentCatalogItem{}, false
	}
	item, ok := items[int(binary.LittleEndian.Uint32(core[1:5]))]
	return item, ok && item.ID > 0 && !item.Expire && s4a21LoadoutCompatible(item)
}

func hasStartupArtifactCore(cores map[int][]byte) bool {
	for slot := a21ArtifactSlotBase; slot <= a21ArtifactSlotBase+2; slot++ {
		if len(cores[slot]) > 0 {
			return true
		}
	}
	return false
}

// growCompliant accepts the grow states that the startup growth reconcile can
// bring in line with the configuration: untransferred, unreleased-branch and
// malformed values are re-picked from the PVF catalog, and a transferred
// character at stage 0 is awakened while reconcile_awakening provides an
// available stage. A branch-less job keeps stage 0 as its final state. Any
// other state has to match a configured awakening stage already.
func (s SQLiteStartupInventory) growCompliant(character startupCharacter) bool {
	if character.grow < 0 || character.grow > 255 {
		return false
	}
	branches := s.JobGrows[character.job]
	first := character.grow & 0x0F
	second := (character.grow >> 4) & 0x0F
	if first == 0 && len(branches) == 0 {
		return true
	}
	if releasedBranch(branches, first) && configuredIntValue(s.Config.GrowTypes, second) {
		return true
	}
	target, changed := reconciledGrow(character.job, character.grow, s.Config.GrowTypes, branches, s.StatTables, s.Config.ReconcileAwakening, nil)
	return changed && configuredIntValue(s.Config.GrowTypes, (target>>4)&0x0F)
}

func configuredIntValue(values []int, value int) bool {
	if len(values) == 0 {
		return true
	}
	for _, configured := range values {
		if configured == value {
			return true
		}
	}
	return false
}

func ownedRobotUID(account, prefix string, start, end int) (int, bool) {
	if start <= 0 || end < start || !strings.HasPrefix(account, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(account, prefix)
	uid, err := strconv.Atoi(suffix)
	if err != nil || uid < start || uid > end || account != prefix+strconv.Itoa(uid) {
		return 0, false
	}
	return uid, true
}
