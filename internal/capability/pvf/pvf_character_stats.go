package pvf

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// StatVector is one growth row of a .chr file. Values are the file's raw
// numbers scaled by ten, matching the server's CharacterStatComputer.
type StatVector struct {
	HpMax, MpMax, PhysAtk, PhysDef, MagAtk, MagDef int
	FireRes, WaterRes, DarkRes, LightRes           int
	InventoryLimit, HpRegen, MpRegen, MoveSpeed    int
	AttackSpeed, CastSpeed, HitRecovery, JumpPower int
	Weight                                         int
}

func (v *StatVector) add(other StatVector) {
	v.HpMax += other.HpMax
	v.MpMax += other.MpMax
	v.PhysAtk += other.PhysAtk
	v.PhysDef += other.PhysDef
	v.MagAtk += other.MagAtk
	v.MagDef += other.MagDef
	v.FireRes += other.FireRes
	v.WaterRes += other.WaterRes
	v.DarkRes += other.DarkRes
	v.LightRes += other.LightRes
	v.InventoryLimit += other.InventoryLimit
	v.HpRegen += other.HpRegen
	v.MpRegen += other.MpRegen
	v.MoveSpeed += other.MoveSpeed
	v.AttackSpeed += other.AttackSpeed
	v.CastSpeed += other.CastSpeed
	v.HitRecovery += other.HitRecovery
	v.JumpPower += other.JumpPower
	v.Weight += other.Weight
}

// CharacterStatTables is one job's parsed .chr growth graph: the initial value
// row, the [growtype N] transfer rows and the [awakening N] rows. The Set flags
// distinguish an absent row from an all-zero row.
type CharacterStatTables struct {
	Base        StatVector
	Growtype    [7]StatVector
	GrowtypeSet [7]bool
	Awakening   [7][3]StatVector
	AwakenSet   [7][3]bool
}

// Premium bonuses applied by the server when building the wire stat blob.
const (
	premiumHpBonus = 9800
	premiumMpBonus = 10500
)

// FallbackCharacterStatTables is used when a .chr file is missing or cannot be
// parsed. Unlike the server's fixed fallback (which throws above level 1), the
// zero-delta growth rows keep provisioning working: the panel then shows the
// base row plus the premium bonus. ComposeRuntime logs every job that fell back
// so a broken .chr file stays visible.
func FallbackCharacterStatTables() CharacterStatTables {
	table := CharacterStatTables{
		Base: StatVector{
			HpMax: 1800, MpMax: 1400, PhysAtk: 75, PhysDef: 75, MagAtk: 45, MagDef: 45,
			InventoryLimit: 480000, MpRegen: 500, MoveSpeed: 8500,
			AttackSpeed: 8500, CastSpeed: 7000, HitRecovery: 6000, JumpPower: 4300, Weight: 500000,
		},
	}
	for n := 1; n <= 6; n++ {
		table.GrowtypeSet[n] = true
	}
	return table
}

// ProjectCharacterStatCatalog parses every job of character/character.lst into
// its growth tables. It also returns the jobs that fell back because their
// .chr file is missing, unreadable or malformed.
func ProjectCharacterStatCatalog(archive TownTextArchive) (map[int]CharacterStatTables, []int) {
	result := make(map[int]CharacterStatTables)
	if archive == nil {
		return result, nil
	}
	lst, _ := archive.ReadText("character/character.lst")
	if lst == "" {
		return result, nil
	}
	var fallbackJobs []int
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		job, err := strconv.Atoi(match[1])
		if err != nil || job < 0 || job > 255 {
			continue
		}
		body, _ := archive.ReadText(normalizePVFPath("character/" + match[2]))
		tables, err := parseCharacterStatTables(body)
		if err != nil {
			result[job] = FallbackCharacterStatTables()
			fallbackJobs = append(fallbackJobs, job)
			continue
		}
		result[job] = tables
	}
	return result, fallbackJobs
}

func parseCharacterStatTables(body string) (CharacterStatTables, error) {
	var tables CharacterStatTables
	lower := strings.ToLower(body)
	initPos := strings.Index(lower, "[initial value]")
	if initPos < 0 {
		return tables, fmt.Errorf("character stat file has no [initial value] section")
	}
	var growtypePos [8]int
	for n := 1; n <= 6; n++ {
		position := strings.Index(lower, "[growtype "+strconv.Itoa(n)+"]")
		growtypePos[n] = position
		tables.GrowtypeSet[n] = position >= 0
	}
	growtypePos[7] = len(body)
	tables.Base = parseStatVector(body[initPos:nextGrowtypeBoundary(growtypePos, tables.GrowtypeSet, 1, len(body))])
	for n := 1; n <= 6; n++ {
		if !tables.GrowtypeSet[n] {
			continue
		}
		block := body[growtypePos[n]:nextGrowtypeBoundary(growtypePos, tables.GrowtypeSet, n+1, len(body))]
		blockLower := strings.ToLower(block)
		awakening1 := strings.Index(blockLower, "[awakening 1]")
		awakening2 := strings.Index(blockLower, "[awakening 2]")
		ownEnd := len(block)
		if awakening1 >= 0 {
			ownEnd = awakening1
		} else if awakening2 >= 0 {
			ownEnd = awakening2
		}
		tables.Growtype[n] = parseStatVector(block[:ownEnd])
		if awakening1 >= 0 {
			end := len(block)
			if awakening2 > awakening1 {
				end = awakening2
			}
			tables.Awakening[n][1] = parseStatVector(block[awakening1:end])
			tables.AwakenSet[n][1] = true
		}
		if awakening2 >= 0 {
			tables.Awakening[n][2] = parseStatVector(block[awakening2:])
			tables.AwakenSet[n][2] = true
		}
	}
	if !tables.GrowtypeSet[1] {
		return CharacterStatTables{}, fmt.Errorf("character stat file has no [growtype 1] section")
	}
	return tables, nil
}

func nextGrowtypeBoundary(positions [8]int, present [7]bool, from, fallback int) int {
	for n := from; n <= 6; n++ {
		if present[n] {
			return positions[n]
		}
	}
	return fallback
}

func parseStatVector(section string) StatVector {
	return StatVector{
		HpMax:          statValue(section, "HP MAX"),
		MpMax:          statValue(section, "MP MAX"),
		PhysAtk:        statValue(section, "physical attack"),
		PhysDef:        statValue(section, "physical defense"),
		MagAtk:         statValue(section, "magical attack"),
		MagDef:         statValue(section, "magical defense"),
		FireRes:        statValue(section, "fire resistance"),
		WaterRes:       statValue(section, "water resistance"),
		DarkRes:        statValue(section, "dark resistance"),
		LightRes:       statValue(section, "light resistance"),
		InventoryLimit: statValue(section, "inventory limit"),
		HpRegen:        statValue(section, "HP regen speed"),
		MpRegen:        statValue(section, "MP regen speed"),
		MoveSpeed:      statValue(section, "move speed"),
		AttackSpeed:    statValue(section, "attack speed"),
		CastSpeed:      statValue(section, "cast speed"),
		HitRecovery:    statValue(section, "hit recovery"),
		JumpPower:      statValue(section, "jump power"),
		Weight:         statValue(section, "weight"),
	}
}

func statValue(section, key string) int {
	pattern, ok := statPatterns[key]
	if !ok {
		return 0
	}
	match := pattern.FindStringSubmatch(section)
	if match == nil {
		return 0
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}
	return int(float32(value) * 10)
}

var statPatterns = buildStatPatterns()

func buildStatPatterns() map[string]*regexp.Regexp {
	keys := []string{
		"HP MAX", "MP MAX", "physical attack", "physical defense",
		"magical attack", "magical defense", "fire resistance", "water resistance",
		"dark resistance", "light resistance", "inventory limit", "HP regen speed",
		"MP regen speed", "move speed", "attack speed", "cast speed",
		"hit recovery", "jump power", "weight",
	}
	result := make(map[string]*regexp.Regexp, len(keys))
	for _, key := range keys {
		result[key] = regexp.MustCompile(`(?i)\[` + regexp.QuoteMeta(key) + `\]\s*([-\d.]+)`)
	}
	return result
}

// BuildAdditionalInfo mirrors the server's
// CharacterStatComputer.BuildAdditionalInfo: it accumulates the base row plus
// the level growth rows (1..14 base, 15..49 transfer, 50+ awakening), adds the
// premium HP/MP bonuses and serializes the 82-byte wire blob.
func BuildAdditionalInfo(tables CharacterStatTables, level, first, second int) ([]byte, error) {
	if first < 0 || first > maxA21FirstGrow || second < 0 || second > 2 {
		return nil, fmt.Errorf("invalid character growth first=%d second=%d", first, second)
	}
	if level < 1 {
		level = 1
	}
	acc := tables.Base
	if level > 1 {
		if !tables.GrowtypeSet[1] {
			return nil, fmt.Errorf("character stat table has no [growtype 1] growth row")
		}
		base := tables.Growtype[1]
		if !tables.GrowtypeSet[first+1] {
			return nil, fmt.Errorf("character stat table has no [growtype %d] growth row", first+1)
		}
		firstGrow := tables.Growtype[first+1]
		awakening := firstGrow
		if second > 0 {
			if !tables.AwakenSet[first+1][second] {
				return nil, fmt.Errorf("character stat table has no [awakening %d] row for growtype %d", second, first+1)
			}
			awakening = tables.Awakening[first+1][second]
		}
		for i := 1; i < level; i++ {
			switch {
			case i <= 14:
				acc.add(base)
			case i <= 49:
				acc.add(firstGrow)
			default:
				acc.add(awakening)
			}
		}
	}
	blob := make([]byte, 82)
	binary.LittleEndian.PutUint32(blob[0:4], uint32(acc.HpMax+premiumHpBonus))
	binary.LittleEndian.PutUint32(blob[4:8], uint32(acc.MpMax+premiumMpBonus))
	putInt16 := func(offset, value int) {
		binary.LittleEndian.PutUint16(blob[offset:offset+2], uint16(int16(value)))
	}
	putInt16(8, acc.PhysAtk)
	putInt16(10, acc.PhysDef)
	putInt16(12, acc.MagAtk)
	putInt16(14, acc.MagDef)
	putInt16(16, acc.FireRes)
	putInt16(18, acc.WaterRes)
	putInt16(20, acc.DarkRes)
	putInt16(22, acc.LightRes)
	// offsets 24..57 are the server's 17 x u16 status-resistance placeholders
	binary.LittleEndian.PutUint32(blob[58:62], uint32(acc.InventoryLimit))
	binary.LittleEndian.PutUint16(blob[62:64], uint16(acc.HpRegen))
	binary.LittleEndian.PutUint16(blob[64:66], uint16(acc.MpRegen))
	binary.LittleEndian.PutUint32(blob[66:70], uint32(acc.MoveSpeed))
	binary.LittleEndian.PutUint16(blob[70:72], uint16(acc.AttackSpeed))
	binary.LittleEndian.PutUint16(blob[72:74], uint16(acc.CastSpeed))
	binary.LittleEndian.PutUint16(blob[74:76], uint16(acc.HitRecovery))
	binary.LittleEndian.PutUint16(blob[76:78], uint16(acc.JumpPower))
	binary.LittleEndian.PutUint32(blob[78:82], uint32(acc.Weight))
	return blob, nil
}

// CombatStatFields is the character_subtype1_fields projection of the 82-byte
// stat blob, using the same offsets as the server's SqliteSubtype1Repository.
type CombatStatFields struct {
	HpMax, MpMax, InventoryLimit, MoveSpeed, Weight int64
	PhysAtk, PhysDef, MagAtk, MagDef                int
	FireRes, WaterRes, DarkRes, LightRes            int
	HpRegen, MpRegen, AttackSpeed                   int
	CastSpeed, HitRecovery, JumpPower               int
}

func ParseCombatStatFields(blob []byte) (CombatStatFields, error) {
	if len(blob) < 82 {
		return CombatStatFields{}, fmt.Errorf("character stat blob is %d bytes, want 82", len(blob))
	}
	var fields CombatStatFields
	offset := 0
	fields.HpMax = int64(binary.LittleEndian.Uint32(blob[offset : offset+4]))
	offset += 4
	fields.MpMax = int64(binary.LittleEndian.Uint32(blob[offset : offset+4]))
	offset += 4
	fields.PhysAtk = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.PhysDef = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.MagAtk = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.MagDef = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.FireRes = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.WaterRes = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.DarkRes = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	fields.LightRes = int(int16(binary.LittleEndian.Uint16(blob[offset : offset+2])))
	offset += 2
	offset += 34 // 17 x u16 status resistance placeholders
	fields.InventoryLimit = int64(binary.LittleEndian.Uint32(blob[offset : offset+4]))
	offset += 4
	fields.HpRegen = int(binary.LittleEndian.Uint16(blob[offset : offset+2]))
	offset += 2
	fields.MpRegen = int(binary.LittleEndian.Uint16(blob[offset : offset+2]))
	offset += 2
	fields.MoveSpeed = int64(binary.LittleEndian.Uint32(blob[offset : offset+4]))
	offset += 4
	fields.AttackSpeed = int(binary.LittleEndian.Uint16(blob[offset : offset+2]))
	offset += 2
	fields.CastSpeed = int(binary.LittleEndian.Uint16(blob[offset : offset+2]))
	offset += 2
	fields.HitRecovery = int(binary.LittleEndian.Uint16(blob[offset : offset+2]))
	offset += 2
	fields.JumpPower = int(binary.LittleEndian.Uint16(blob[offset : offset+2]))
	offset += 2
	fields.Weight = int64(binary.LittleEndian.Uint32(blob[offset : offset+4]))
	return fields, nil
}

// ProjectLevelThresholds parses character/ExpTable.tbl: the cumulative
// experience required to leave each level.
func ProjectLevelThresholds(archive TownTextArchive) []int {
	if archive == nil {
		return nil
	}
	text, _ := archive.ReadText(normalizePVFPath("character/ExpTable.tbl"))
	if text == "" {
		return nil
	}
	fields := strings.Fields(text)
	thresholds := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		thresholds = append(thresholds, value)
	}
	return thresholds
}

// LevelExpFor returns the cumulative experience of a character at `level`,
// mirroring the server's "exp covers threshold(level-1)" convention.
func LevelExpFor(thresholds []int, level int) int {
	if level <= 1 || len(thresholds) == 0 {
		return 0
	}
	if level-1 > len(thresholds) {
		return thresholds[len(thresholds)-1]
	}
	return thresholds[level-2]
}
