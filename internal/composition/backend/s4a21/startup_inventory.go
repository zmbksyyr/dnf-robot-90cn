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
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/foundation/charset"
	"robot/internal/shared"
)

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
// complete, usable one-character robot is removed in the same transaction.
func (s SQLiteStartupInventory) ScanAndClean(ctx context.Context) (StartupInventory, error) {
	var result StartupInventory
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
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("connect S4A21 startup inventory: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return result, fmt.Errorf("configure S4A21 startup inventory: %w", err)
	}
	// A deferred transaction can become SQLITE_BUSY_SNAPSHOT when the game
	// server writes after our reads but before invalid-account deletion. Take
	// the write reservation first so the scan and cleanup remain one atomic
	// startup operation.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("begin S4A21 startup inventory: %w", err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)

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
	invalidAccountIDs := make([]int, 0)
	for _, account := range accounts {
		if len(account.characters) != 1 || !s.characterCompliant(account.characters[0], index, items) {
			invalidAccountIDs = append(invalidAccountIDs, account.id)
			result.DeletedCharacters += len(account.characters)
			continue
		}
		character := account.characters[0]
		name := strings.TrimSpace(charset.DecodePVFBytes(character.nameRaw))
		robot := robotcap.Info{
			UID: account.uid, CID: character.id, Name: name,
			Level: character.level, Job: character.job, Grow: character.grow,
			Village: character.village, Area: character.area, X: character.x, Y: character.y,
		}
		slot := uint16(character.slot)
		result.Robots = append(result.Robots, robot)
		result.Identities = append(result.Identities, robotstate.Identity{
			Backend: shared.BackendS4A21, Account: account.name, CharacterName: name, Slot: &slot,
			BackendCharacterID: strconv.Itoa(character.id),
		})
	}
	for _, accountID := range invalidAccountIDs {
		if _, err := conn.ExecContext(ctx, `DELETE FROM accounts WHERE account_id=?`, accountID); err != nil {
			return result, fmt.Errorf("delete invalid S4A21 robot account id=%d: %w", accountID, err)
		}
	}
	result.DeletedAccounts = len(invalidAccountIDs)
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("commit S4A21 startup inventory: %w", err)
	}
	sort.Slice(result.Robots, func(i, j int) bool { return result.Robots[i].UID < result.Robots[j].UID })
	sort.Slice(result.Identities, func(i, j int) bool { return result.Identities[i].Account < result.Identities[j].Account })
	return result, nil
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
	name := strings.TrimSpace(charset.DecodePVFBytes(character.nameRaw))
	encodedName, err := charset.EncodeGBKString(name)
	if err != nil || len(encodedName) < 2 || len(encodedName) > 18 {
		return false
	}
	if !configuredIntValue(s.Config.Jobs, character.job) || !configuredIntValue(s.Config.GrowTypes, character.grow) {
		return false
	}
	cores := index.cores[character.id]
	equipSlots := s.Config.EquipSlots
	if len(equipSlots) == 0 {
		equipSlots = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	for _, commonSlot := range equipSlots {
		item, ok := startupCoreItem(cores[commonSlot+11], a21ItemKindEquipment, items)
		if !ok || item.ItemType != commonSlot || !equipmentcap.UsableByJob(item.UseJob, character.job) {
			return false
		}
		if commonSlot < 11 && item.Level > character.level {
			return false
		}
	}
	avatarSlots := s.Config.AvatarSlots
	if len(avatarSlots) == 0 {
		avatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	}
	avatarCount := 0
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
	}
	if avatarCount < s.Config.MinAvatarSlots {
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
