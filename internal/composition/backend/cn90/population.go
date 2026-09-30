package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// SQLitePopulationInspector performs an operator-requested, read-only audit.
// The first persistence stage reports account/character identity, job, gender
// and town coverage; equipment and avatar coverage arrive with the equipment
// stage.
type SQLitePopulationInspector struct {
	DatabasePath  string
	AccountPrefix string
	Config        robotconfig.RuntimeConfig
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
	characters, err := p.readCharacters(ctx, db)
	if err != nil {
		return robotcap.PopulationReport{}, err
	}
	accounts, err := p.readAccountCount(ctx, db)
	if err != nil {
		return robotcap.PopulationReport{}, err
	}
	return p.summarize(accounts, characters), nil
}

type populationCharacter struct {
	job     int
	level   int
	grow    int
	village int
	area    int
}

func (p SQLitePopulationInspector) readAccountCount(ctx context.Context, db *sql.DB) (int, error) {
	var accounts int
	query := `SELECT COUNT(*) FROM ` + dnfCharactersTable +
		` WHERE account_id LIKE ? AND delete_flag=0`
	if err := db.QueryRowContext(ctx, query, p.AccountPrefix+"%").Scan(&accounts); err != nil {
		return 0, fmt.Errorf("count 90CN population accounts: %w", err)
	}
	return accounts, nil
}

func (p SQLitePopulationInspector) readCharacters(ctx context.Context, db *sql.DB) ([]populationCharacter, error) {
	query := `SELECT job, level, grow_type, town_id, area_id FROM ` + dnfCharactersTable +
		` WHERE account_id LIKE ? AND delete_flag=0`
	rows, err := db.QueryContext(ctx, query, p.AccountPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("read 90CN population characters: %w", err)
	}
	defer rows.Close()
	characters := make([]populationCharacter, 0, 64)
	for rows.Next() {
		var jobText string
		var character populationCharacter
		if err := rows.Scan(&jobText, &character.level, &character.grow, &character.village, &character.area); err != nil {
			return nil, fmt.Errorf("scan 90CN population character: %w", err)
		}
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(jobText)); parseErr == nil {
			character.job = parsed
		}
		characters = append(characters, character)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate 90CN population characters: %w", err)
	}
	return characters, nil
}

func (p SQLitePopulationInspector) summarize(accounts int, characters []populationCharacter) robotcap.PopulationReport {
	report := robotcap.PopulationReport{Accounts: accounts, GeneratedAt: time.Now().UTC()}
	jobs := make(map[int]int)
	genders := make(map[string]int)
	areas := make(map[shared.MapAreaKey]int)
	for _, character := range characters {
		report.Characters++
		jobs[character.job]++
		genders[cn90JobGender(character.job)]++
		areas[shared.MapAreaKey{Village: character.village, Area: character.area}]++
	}
	report.Genders = genderBuckets(genders, report.Characters)
	report.Jobs = jobBuckets(jobs, report.Characters)
	p.measureAreas(&report, areas)
	return report
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
