package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"robot/internal/capability/pvf"
	robottemplate "robot/internal/capability/robottemplate"
	foundationconfig "robot/internal/foundation/config"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// ItemCatalogView exposes the cached catalogs to audited read-only callers.
// The slices and their nested values must not be modified.
type ItemCatalogView struct {
	Equipment []shared.EquipmentCatalogItem
	Stackable []shared.EquipmentCatalogItem
}

func ViewItemCatalogs(configDir string) ItemCatalogView {
	if strings.TrimSpace(configDir) == "" {
		return ItemCatalogView{}
	}
	return ItemCatalogView{
		Equipment: equipmentFileView(configDir, "equipment_catalog.json"),
		Stackable: equipmentFileView(configDir, "stackable_catalog.json"),
	}
}

func ViewStackable(configDir string) []shared.EquipmentCatalogItem {
	if strings.TrimSpace(configDir) == "" {
		return nil
	}
	return equipmentFileView(configDir, "stackable_catalog.json")
}

type jsonFileStamp struct {
	exists  bool
	mtimeNS int64
	size    int64
}

type jsonFileCacheEntry[T any] struct {
	stamp     jsonFileStamp
	value     T
	checkedAt time.Time
}

type jsonFileCache[T any] struct {
	mu      lockhub.Locker
	entries map[string]jsonFileCacheEntry[T]
}

var (
	mapCatalogFiles  jsonFileCache[[]shared.MapCatalogItem]
	shoutFiles       jsonFileCache[robottemplate.ShoutTemplates]
	nameFiles        jsonFileCache[robottemplate.NameTemplates]
	itemCatalogFiles jsonFileCache[[]shared.EquipmentCatalogItem]
)

const (
	catalogFileCheckInterval = time.Second
	maxCatalogCacheEntries   = 32
)

// ViewMaps returns the cached map catalog for audited read-only callers.
func ViewMaps(configDir string) []shared.MapCatalogItem {
	if strings.TrimSpace(configDir) == "" {
		return nil
	}
	path := filepath.Join(configDir, "map_catalog.json")
	return mapCatalogFiles.load(path, nil, func(data []byte, fallback []shared.MapCatalogItem) []shared.MapCatalogItem {
		var out []shared.MapCatalogItem
		if json.Unmarshal(data, &out) != nil {
			return fallback
		}
		return out
	})
}

func ShoutTemplates(configDir string) robottemplate.ShoutTemplates {
	fallback := robottemplate.ShoutTemplates{Channel: "world", Type: 80, Messages: []string{"hello"}}
	if strings.TrimSpace(configDir) == "" {
		return robottemplate.CloneShoutTemplates(fallback)
	}
	path := filepath.Join(configDir, "robot_shout_templates.json")
	t := shoutFiles.load(path, fallback, decodeShoutTemplates)
	return robottemplate.CloneShoutTemplates(t)
}

// ReadShoutTemplates parses one complete template snapshot. Malformed, empty,
// and legacy array files are rejected so a runtime watcher can retain the
// previous valid snapshot.
func ReadShoutTemplates(path string) (robottemplate.ShoutTemplates, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return robottemplate.ShoutTemplates{}, err
	}
	return parseShoutTemplates(data)
}

func parseShoutTemplates(data []byte) (robottemplate.ShoutTemplates, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return robottemplate.ShoutTemplates{}, fmt.Errorf("empty shout template")
	}
	if data[0] != '{' {
		return robottemplate.ShoutTemplates{}, fmt.Errorf("shout template must be an object")
	}
	var raw struct {
		Channel  *string   `json:"channel"`
		Type     *int      `json:"type"`
		Messages *[]string `json:"messages"`
	}
	if err := foundationconfig.DecodeJSONBytes(data, &raw); err != nil {
		return robottemplate.ShoutTemplates{}, err
	}
	if raw.Channel == nil || raw.Type == nil || raw.Messages == nil {
		return robottemplate.ShoutTemplates{}, fmt.Errorf("shout template requires channel, type, and messages")
	}
	channel := *raw.Channel
	if channel != "world" && channel != "local" {
		return robottemplate.ShoutTemplates{}, fmt.Errorf("shout template channel must be world or local")
	}
	if *raw.Type < 1 || *raw.Type > 255 {
		return robottemplate.ShoutTemplates{}, fmt.Errorf("shout template type must be between 1 and 255")
	}
	messages, err := validateTemplateStrings("messages", *raw.Messages)
	if err != nil {
		return robottemplate.ShoutTemplates{}, err
	}
	if len(messages) == 0 {
		return robottemplate.ShoutTemplates{}, fmt.Errorf("shout template contains no messages")
	}
	t := robottemplate.ShoutTemplates{Channel: channel, Type: *raw.Type, Messages: messages}
	return robottemplate.CloneShoutTemplates(t), nil
}

func decodeShoutTemplates(data []byte, fallback robottemplate.ShoutTemplates) robottemplate.ShoutTemplates {
	t, err := parseShoutTemplates(data)
	if err != nil {
		return fallback
	}
	return t
}

func (c *jsonFileCache[T]) load(path string, fallback T, decode func([]byte, T) T) T {
	path = canonicalCatalogPath(path)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, cached := c.entries[path]
	if cached && !entry.checkedAt.IsZero() && now.Sub(entry.checkedAt) < catalogFileCheckInterval {
		return entry.value
	}

	stamp, err := catalogFileStamp(path)
	if err != nil {
		if cached {
			return entry.value
		}
		return fallback
	}
	if cached && entry.stamp == stamp {
		entry.checkedAt = now
		c.entries[path] = entry
		return entry.value
	}

	value := fallback
	if cached {
		value = entry.value
	}
	if stamp.exists {
		data, readErr := os.ReadFile(path)
		switch {
		case readErr == nil:
			value = decode(data, value)
		case os.IsNotExist(readErr):
			stamp = jsonFileStamp{}
		default:
			return value
		}
	}
	if c.entries == nil {
		c.entries = make(map[string]jsonFileCacheEntry[T])
	}
	if !cached && len(c.entries) >= maxCatalogCacheEntries {
		c.evictOldestLocked()
	}
	c.entries[path] = jsonFileCacheEntry[T]{stamp: stamp, value: value, checkedAt: now}
	return value
}

func (c *jsonFileCache[T]) evictOldestLocked() {
	var oldestPath string
	var oldestCheck time.Time
	for path, entry := range c.entries {
		if oldestPath == "" || entry.checkedAt.Before(oldestCheck) {
			oldestPath = path
			oldestCheck = entry.checkedAt
		}
	}
	delete(c.entries, oldestPath)
}

func canonicalCatalogPath(path string) string {
	clean := filepath.Clean(path)
	if absolute, err := filepath.Abs(clean); err == nil {
		return absolute
	}
	return clean
}

func catalogFileStamp(path string) (jsonFileStamp, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return jsonFileStamp{}, nil
	}
	if err != nil {
		return jsonFileStamp{}, err
	}
	return jsonFileStamp{exists: true, mtimeNS: info.ModTime().UnixNano(), size: info.Size()}, nil
}

// NameTemplates returns a cached read-only template value. Callers must not
// modify the nested slices.
func NameTemplates(configDir string) robottemplate.NameTemplates {
	fallback := defaultNameTemplates()
	if strings.TrimSpace(configDir) == "" {
		return robottemplate.CloneNameTemplates(fallback)
	}
	path := filepath.Join(configDir, "robot_name_templates.json")
	return nameFiles.load(path, fallback, decodeNameTemplates)
}

// ReadNameTemplates parses one complete immutable name-template snapshot.
func ReadNameTemplates(path string) (robottemplate.NameTemplates, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return robottemplate.NameTemplates{}, err
	}
	return parseNameTemplates(data)
}

func parseNameTemplates(data []byte) (robottemplate.NameTemplates, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return robottemplate.NameTemplates{}, fmt.Errorf("empty name template")
	}
	if data[0] != '{' {
		return robottemplate.NameTemplates{}, fmt.Errorf("name template must be an object")
	}
	type rawGrow struct {
		Label string   `json:"label"`
		Names []string `json:"names"`
	}
	type rawJob struct {
		Label string             `json:"label"`
		Names []string           `json:"names"`
		Grows map[string]rawGrow `json:"grows"`
	}
	var raw struct {
		Common []string          `json:"common"`
		Jobs   map[string]rawJob `json:"jobs"`
	}
	if err := foundationconfig.DecodeJSONBytes(data, &raw); err != nil {
		return robottemplate.NameTemplates{}, err
	}
	if raw.Common == nil || raw.Jobs == nil {
		return robottemplate.NameTemplates{}, fmt.Errorf("name template requires common and jobs")
	}
	seenNames := make(map[string]string)
	common, err := validateNameTemplateStrings("common", raw.Common, seenNames)
	if err != nil {
		return robottemplate.NameTemplates{}, err
	}
	t := robottemplate.NameTemplates{Common: common, Jobs: make(map[int]robottemplate.NamePool, len(raw.Jobs))}
	seenJobs := make(map[int]string, len(raw.Jobs))
	for jobKey, jobRaw := range raw.Jobs {
		job, err := parseNamePoolID("job", jobKey)
		if err != nil {
			return robottemplate.NameTemplates{}, err
		}
		if previous, exists := seenJobs[job]; exists {
			return robottemplate.NameTemplates{}, fmt.Errorf("job keys %q and %q resolve to duplicate job %d", previous, jobKey, job)
		}
		seenJobs[job] = jobKey
		label := strings.TrimSpace(jobRaw.Label)
		if label == "" || label != jobRaw.Label {
			return robottemplate.NameTemplates{}, fmt.Errorf("job %d label must be non-blank without surrounding whitespace", job)
		}
		names, err := validateNameTemplateStrings(fmt.Sprintf("jobs[%d].names", job), jobRaw.Names, seenNames)
		if err != nil {
			return robottemplate.NameTemplates{}, err
		}
		pool := robottemplate.NamePool{Label: label, Names: names, Grows: make(map[int]robottemplate.NamePool, len(jobRaw.Grows))}
		seenGrows := make(map[int]string, len(jobRaw.Grows))
		for growKey, growRaw := range jobRaw.Grows {
			grow, err := parseNamePoolID("grow", growKey)
			if err != nil {
				return robottemplate.NameTemplates{}, fmt.Errorf("job %d: %w", job, err)
			}
			if previous, exists := seenGrows[grow]; exists {
				return robottemplate.NameTemplates{}, fmt.Errorf("job %d grow keys %q and %q resolve to duplicate grow %d", job, previous, growKey, grow)
			}
			seenGrows[grow] = growKey
			growLabel := strings.TrimSpace(growRaw.Label)
			if growLabel == "" || growLabel != growRaw.Label {
				return robottemplate.NameTemplates{}, fmt.Errorf("job %d grow %d label must be non-blank without surrounding whitespace", job, grow)
			}
			growNames, err := validateNameTemplateStrings(fmt.Sprintf("jobs[%d].grows[%d].names", job, grow), growRaw.Names, seenNames)
			if err != nil {
				return robottemplate.NameTemplates{}, err
			}
			pool.Grows[grow] = robottemplate.NamePool{Label: growLabel, Names: growNames}
		}
		t.Jobs[job] = pool
	}
	if robottemplate.NameCount(t) == 0 {
		return robottemplate.NameTemplates{}, fmt.Errorf("name template requires at least one name")
	}
	return robottemplate.CloneNameTemplates(t), nil
}

func parseNamePoolID(kind, raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s key %q must be a non-negative integer", kind, raw)
	}
	return value, nil
}

func validateTemplateStrings(field string, values []string) ([]string, error) {
	return validateStringList(field, values, make(map[string]string), false)
}

func validateNameTemplateStrings(field string, values []string, seen map[string]string) ([]string, error) {
	return validateStringList(field, values, seen, true)
}

func validateStringList(field string, values []string, seen map[string]string, requireGameName bool) ([]string, error) {
	out := append([]string(nil), values...)
	for i, value := range out {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("template %s[%d] must not be blank", field, i)
		}
		if trimmed != value {
			return nil, fmt.Errorf("template %s[%d] must not have leading or trailing whitespace", field, i)
		}
		if requireGameName && !robottemplate.FitsGameSlot(value) {
			return nil, fmt.Errorf("template %s[%d] does not fit the game name slot", field, i)
		}
		if previous, exists := seen[value]; exists {
			return nil, fmt.Errorf("template %s[%d] duplicates %q from %s", field, i, value, previous)
		}
		seen[value] = fmt.Sprintf("%s[%d]", field, i)
	}
	return out, nil
}

func defaultNameTemplates() robottemplate.NameTemplates {
	return robottemplate.NameTemplates{
		Common: []string{"BotStar", "BotMoon", "BotSky", "BotWind"},
		Jobs:   map[int]robottemplate.NamePool{},
	}
}

func decodeNameTemplates(data []byte, fallback robottemplate.NameTemplates) robottemplate.NameTemplates {
	t, err := parseNameTemplates(data)
	if err != nil {
		return fallback
	}
	return t
}

func equipmentFileView(configDir string, name string) []shared.EquipmentCatalogItem {
	if strings.TrimSpace(configDir) == "" {
		return nil
	}
	path := filepath.Join(configDir, name)
	return itemCatalogFiles.load(path, nil, func(data []byte, fallback []shared.EquipmentCatalogItem) []shared.EquipmentCatalogItem {
		var out []shared.EquipmentCatalogItem
		if json.Unmarshal(data, &out) != nil {
			return fallback
		}
		if name == "equipment_catalog.json" {
			pvf.NormalizeEquipmentSetKeys(out)
		}
		return out
	})
}
