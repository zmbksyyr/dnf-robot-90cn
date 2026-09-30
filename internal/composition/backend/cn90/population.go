package cn90

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// SQLitePopulationInspector performs an operator-requested, read-only audit.
// It deliberately reuses startup/loadout validation rules so reported
// coverage has the same meaning as adapter-owned character compliance.
type SQLitePopulationInspector struct {
	DatabasePath  string
	AccountPrefix string
	Config        robotconfig.RuntimeConfig
	Equipment     []shared.EquipmentCatalogItem
	Maps          []shared.MapCatalogItem
}

func (p SQLitePopulationInspector) PopulationReport(ctx context.Context) (robotcap.PopulationReport, error) {
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	if strings.TrimSpace(p.DatabasePath) == "" || strings.TrimSpace(p.AccountPrefix) == "" {
		return robotcap.PopulationReport{}, fmt.Errorf("90CN population database path and account prefix are required")
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(p.DatabasePath))
	if err != nil {
		return robotcap.PopulationReport{}, fmt.Errorf("open 90CN population database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return robotcap.PopulationReport{}, fmt.Errorf("configure 90CN population database: %w", err)
	}
	scanner := SQLiteStartupInventory{AccountPrefix: p.AccountPrefix, Config: p.Config, Equipment: p.Equipment}
	scanner.Config.RobotUIDStart = 1
	scanner.Config.RobotUIDEnd = int(^uint(0) >> 1)
	accounts, characterIDs, err := scanner.readOwnedAccounts(ctx, db, p.AccountPrefix)
	if err != nil {
		return robotcap.PopulationReport{}, err
	}
	index, err := readStartupInventoryIndex(ctx, db, p.AccountPrefix, characterIDs)
	if err != nil {
		return robotcap.PopulationReport{}, err
	}
	return p.summarize(accounts, index), nil
}

func (p SQLitePopulationInspector) summarize(accounts []startupAccount, index startupInventoryIndex) robotcap.PopulationReport {
	report := robotcap.PopulationReport{Accounts: len(accounts), GeneratedAt: time.Now().UTC()}
	jobs := make(map[int]int)
	genders := make(map[string]int)
	areas := make(map[shared.MapAreaKey]int)
	items := equipmentByID(p.Equipment)
	equipSlots := configuredEquipmentSlots(p.Config)
	avatarSlots := configuredAvatarSlots(p.Config)
	for _, account := range accounts {
		for _, character := range account.characters {
			if character.deleteFlag != 0 {
				continue
			}
			report.Characters++
			jobs[character.job]++
			genders[cn90JobGender(character.job)]++
			areas[shared.MapAreaKey{Village: character.village, Area: character.area}]++
			cores := index.cores[character.id]
			measureEquipment(&report.Equipment, character, cores, items, equipSlots, p.Config)
			measureAvatars(&report.Avatars, character, cores, index.avatarDetails[character.id], items, avatarSlots, p.Config)
		}
	}
	report.Genders = genderBuckets(genders, report.Characters)
	report.Jobs = jobBuckets(jobs, report.Characters)
	finalizeLoadout(&report.Equipment, report.Characters)
	finalizeLoadout(&report.Avatars, report.Characters)
	p.measureAreas(&report, areas)
	return report
}

func configuredEquipmentSlots(rc robotconfig.RuntimeConfig) []int {
	if len(rc.EquipSlots) > 0 {
		return rc.EquipSlots
	}
	return []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
}

func configuredAvatarSlots(rc robotconfig.RuntimeConfig) []int {
	if len(rc.AvatarSlots) > 0 {
		return rc.AvatarSlots
	}
	return []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
}

func measureEquipment(out *robotcap.PopulationLoadout, character startupCharacter, cores map[int][]byte, items map[int]shared.EquipmentCatalogItem, slots []int, rc robotconfig.RuntimeConfig) {
	out.ExpectedSlots += len(slots)
	setCounts := make(map[string]int)
	filled := 0
	for _, slot := range slots {
		item, ok := startupCoreItem(cores[slot+11], cn90ItemKindEquipment, items)
		if !ok || item.ItemType != slot || !equipmentcap.UsableByJob(item.UseJob, character.job) || slot < 11 && item.Level > character.level {
			continue
		}
		filled++
		addSetKeys(setCounts, item.SetKey)
	}
	out.FilledSlots += filled
	if len(slots) > 0 && filled == len(slots) {
		out.FullCharacters++
	}
	if filled > 0 && maxSetCount(setCounts) >= requiredSetCoverage(filled, rc.EquipSetMinSlots, 5) {
		out.SetCharacters++
	}
}

func measureAvatars(out *robotcap.PopulationLoadout, character startupCharacter, cores map[int][]byte, details map[int]struct{}, items map[int]shared.EquipmentCatalogItem, slots []int, rc robotconfig.RuntimeConfig) {
	out.ExpectedSlots += len(slots)
	setCounts := make(map[string]int)
	filled := 0
	for _, slot := range slots {
		core := cores[slot]
		item, ok := startupCoreItem(core, cn90ItemKindAvatar, items)
		if !ok || item.ItemType != slot+20 || !equipmentcap.AvatarRenderable(item) || !equipmentcap.AvatarUsableByJob(item, cn90AvatarJob(character.job)) || len(core) < 9 {
			continue
		}
		itemUID := int(binary.LittleEndian.Uint32(core[5:9]))
		if _, ok := details[itemUID]; !ok {
			continue
		}
		filled++
		addSetKeys(setCounts, item.SetKey)
	}
	out.FilledSlots += filled
	if len(slots) > 0 && filled == len(slots) {
		out.FullCharacters++
	}
	if filled > 0 && maxSetCount(setCounts) >= requiredSetCoverage(filled, rc.AvatarSetMinSlots, 6) {
		out.SetCharacters++
	}
}

func addSetKeys(counts map[string]int, value string) {
	for _, key := range strings.Split(value, "|") {
		if key = strings.TrimSpace(key); key != "" {
			counts[key]++
		}
	}
}

func finalizeLoadout(out *robotcap.PopulationLoadout, characters int) {
	out.SlotCoveragePercent = percent(out.FilledSlots, out.ExpectedSlots)
	out.FullPercent = percent(out.FullCharacters, characters)
	out.SetPercent = percent(out.SetCharacters, characters)
}

func cn90JobGender(job int) string {
	switch job {
	case 0, 1, 2, 3, 4, 9:
		return "male"
	case 5, 6, 7, 8, 10:
		return "female"
	default:
		return "unknown"
	}
}

func genderBuckets(counts map[string]int, total int) []robotcap.PopulationBucket {
	result := make([]robotcap.PopulationBucket, 0, 3)
	for _, key := range []string{"male", "female", "unknown"} {
		if count := counts[key]; count > 0 {
			result = append(result, robotcap.PopulationBucket{Key: key, Count: count, Percent: percent(count, total)})
		}
	}
	return result
}

func jobBuckets(counts map[int]int, total int) []robotcap.PopulationBucket {
	keys := make([]int, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	result := make([]robotcap.PopulationBucket, 0, len(keys))
	for _, key := range keys {
		result = append(result, robotcap.PopulationBucket{Key: strconv.Itoa(key), Count: counts[key], Percent: percent(counts[key], total)})
	}
	return result
}

func (p SQLitePopulationInspector) measureAreas(report *robotcap.PopulationReport, counts map[shared.MapAreaKey]int) {
	available := make(map[shared.MapAreaKey]shared.MapCatalogItem)
	for _, item := range p.Maps {
		if item.Use {
			available[shared.MapAreaKey{Village: item.Village, Area: item.Area}] = item
		}
	}
	report.AvailableAreas = len(available)
	for key, count := range counts {
		item, known := available[key]
		if known {
			report.OccupiedAreas++
		} else {
			report.UnknownAreaCharacters += count
		}
		report.Areas = append(report.Areas, robotcap.PopulationArea{
			Village: key.Village, VillageName: item.VillageName, Area: key.Area,
			Count: count, Percent: percent(count, report.Characters),
		})
	}
	report.AreaCoveragePercent = percent(report.OccupiedAreas, report.AvailableAreas)
	sort.Slice(report.Areas, func(i, j int) bool {
		if report.Areas[i].Count != report.Areas[j].Count {
			return report.Areas[i].Count > report.Areas[j].Count
		}
		if report.Areas[i].Village != report.Areas[j].Village {
			return report.Areas[i].Village < report.Areas[j].Village
		}
		return report.Areas[i].Area < report.Areas[j].Area
	})
}

func percent(value, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}
