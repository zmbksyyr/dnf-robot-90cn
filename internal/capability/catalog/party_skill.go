package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"robot/internal/foundation/atomicfile"
	foundationconfig "robot/internal/foundation/config"
	"robot/internal/shared"
)

const maxSafePartySkillLevel = 70

type partySkillCatalogEntry struct {
	ID         string          `json:"id,omitempty"`
	Enabled    bool            `json:"enabled"`
	JobLabel   string          `json:"job_label,omitempty"`
	Disabled   bool            `json:"disabled,omitempty"`
	Job        int             `json:"job"`
	SkillIndex int             `json:"skill_index"`
	State      int             `json:"state"`
	Level      int             `json:"level"`
	Name       string          `json:"name,omitempty"`
	ScriptPath string          `json:"script_path,omitempty"`
	StateData  json.RawMessage `json:"state_data,omitempty"`
	Risk       int             `json:"risk,omitempty"`
}

type partySkillCatalogDocument struct {
	Enabled       *bool              `json:"enabled"`
	MaxSkillLevel *int               `json:"max_skill_level"`
	Skills        *[]json.RawMessage `json:"skills"`
}

type partySkillCatalogEntryDocument struct {
	ID         string           `json:"id,omitempty"`
	Enabled    *bool            `json:"enabled,omitempty"`
	JobLabel   string           `json:"job_label,omitempty"`
	Disabled   bool             `json:"disabled,omitempty"`
	Job        *int             `json:"job"`
	SkillIndex *int             `json:"skill_index"`
	State      *int             `json:"state"`
	Level      *int             `json:"level"`
	Name       string           `json:"name,omitempty"`
	ScriptPath string           `json:"script_path,omitempty"`
	StateData  *json.RawMessage `json:"state_data,omitempty"`
	Risk       int              `json:"risk,omitempty"`
}

type PartySkillCatalogIssue struct {
	Index      int
	Job        int
	SkillIndex int
	State      int
	Reason     string
}

type PartySkillCatalogReport struct {
	Enabled                bool
	Entries                []shared.PartySkillState
	Issues                 []PartySkillCatalogIssue
	SourceCount            int
	DisabledCount          int
	SwitchOffCount         int
	OverLevelCount         int
	EffectiveMaxSkillLevel int
}

type PartySkillCatalogValidationError struct {
	Issues []PartySkillCatalogIssue
}

func (e *PartySkillCatalogValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ""
	}
	const detailLimit = 3
	details := make([]string, 0, min(len(e.Issues), detailLimit))
	for _, issue := range e.Issues[:min(len(e.Issues), detailLimit)] {
		details = append(details, fmt.Sprintf("entry=%d job=%d skill=%d state=%d: %s", issue.Index, issue.Job, issue.SkillIndex, issue.State, issue.Reason))
	}
	suffix := ""
	if remaining := len(e.Issues) - len(details); remaining > 0 {
		suffix = fmt.Sprintf("; and %d more", remaining)
	}
	return fmt.Sprintf("party skill catalog has %d invalid entries: %s%s", len(e.Issues), strings.Join(details, "; "), suffix)
}

func LoadPartySkills(configDir string) error {
	if strings.TrimSpace(configDir) == "" {
		return fmt.Errorf("empty party skill catalog directory")
	}
	report, err := ReadPartySkillCatalog(filepath.Join(configDir, "party_skill_catalog.json"))
	if err != nil {
		return err
	}
	if len(report.Issues) > 0 {
		return &PartySkillCatalogValidationError{Issues: report.Issues}
	}
	if !report.Enabled {
		shared.SetPartySkillStates(nil)
		return nil
	}
	shared.SetPartySkillStates(report.Entries)
	return nil
}

// ReadPartySkillCatalog parses and validates the whitelist without changing
// the process-wide runtime snapshot. Entries with semantic errors are reported
// in Issues; a runtime publisher must reject the whole snapshot when Issues is
// non-empty.
func ReadPartySkillCatalog(path string) (PartySkillCatalogReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PartySkillCatalogReport{}, err
	}
	return parsePartySkillCatalog(data)
}

func parsePartySkillCatalog(data []byte) (PartySkillCatalogReport, error) {
	raw, err := decodePartySkillCatalogDocument(data)
	if err != nil {
		return PartySkillCatalogReport{}, err
	}

	report := PartySkillCatalogReport{
		Enabled:                *raw.Enabled,
		SourceCount:            len(*raw.Skills),
		EffectiveMaxSkillLevel: *raw.MaxSkillLevel,
		Entries:                make([]shared.PartySkillState, 0, len(*raw.Skills)),
	}
	for index, rawEntry := range *raw.Skills {
		entry, err := decodePartySkillCatalogEntry(index, rawEntry)
		if err != nil {
			return PartySkillCatalogReport{}, err
		}
		if reason := invalidPartySkillEntryReason(entry); reason != "" {
			report.addPartySkillIssue(index, entry, reason)
			continue
		}
		values, err := decodePartySkillStateData(entry.StateData)
		if err != nil {
			report.addPartySkillIssue(index, entry, err.Error())
			continue
		}
		stateData, err := partySkillStateData(values)
		if err != nil {
			report.addPartySkillIssue(index, entry, err.Error())
			continue
		}
		if entry.Disabled {
			report.DisabledCount++
			continue
		}
		if !entry.Enabled {
			report.SwitchOffCount++
			continue
		}
		if entry.Level > report.EffectiveMaxSkillLevel {
			report.OverLevelCount++
			continue
		}
		report.Entries = append(report.Entries, shared.PartySkillState{
			Job: entry.Job, SkillIndex: entry.SkillIndex, State: entry.State,
			Level: entry.Level, Name: entry.Name, ScriptPath: entry.ScriptPath,
			StateData: stateData, Risk: entry.Risk,
		})
	}
	if !report.Enabled {
		report.Entries = nil
	}
	return report, nil
}

// SyncPartySkillCatalog rebuilds the user-facing switch list from the current
// PVF export. Existing explicit switches and verified protocol metadata are
// preserved by the complete runtime key; newly discovered skills are off.
func SyncPartySkillCatalog(path, pvfPath string) (bool, error) {
	pvfData, err := os.ReadFile(pvfPath)
	if err != nil {
		return false, err
	}
	var pvfStates []shared.SkillState
	if err := json.Unmarshal(pvfData, &pvfStates); err != nil {
		return false, fmt.Errorf("decode PVF skill catalog: %w", err)
	}
	if len(pvfStates) == 0 {
		return false, fmt.Errorf("PVF skill catalog is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	raw, err := decodePartySkillCatalogDocument(data)
	if err != nil {
		return false, err
	}
	existing := make(map[string]partySkillCatalogEntry, len(*raw.Skills))
	for index, rawEntry := range *raw.Skills {
		entry, err := decodePartySkillCatalogEntry(index, rawEntry)
		if err != nil {
			return false, err
		}
		existing[partySkillRuntimeKey(entry.Job, entry.SkillIndex, entry.State, entry.ScriptPath)] = entry
	}

	entries := make([]partySkillCatalogEntry, 0, len(pvfStates))
	seen := make(map[string]struct{}, len(pvfStates))
	for _, state := range pvfStates {
		key := partySkillRuntimeKey(state.Job, state.SkillIndex, state.State, state.ScriptPath)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		entry, ok := existing[key]
		if !ok {
			entry = partySkillCatalogEntry{
				Enabled: false,
				Job:     state.Job, SkillIndex: state.SkillIndex, State: state.State,
				Level: maxSafePartySkillLevel, ScriptPath: state.ScriptPath,
				Name: partySkillDisplayName(state.ScriptPath), Risk: 2,
			}
		}
		entry.ID = key
		entry.JobLabel = partySkillJobLabel(state.Job)
		entry.Job = state.Job
		entry.SkillIndex = state.SkillIndex
		entry.State = state.State
		entry.ScriptPath = state.ScriptPath
		entry.Disabled = false
		if strings.TrimSpace(entry.Name) == "" {
			entry.Name = partySkillDisplayName(state.ScriptPath)
		}
		if entry.Level <= 0 || entry.Level > maxSafePartySkillLevel {
			entry.Level = maxSafePartySkillLevel
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Job != entries[j].Job {
			return entries[i].Job < entries[j].Job
		}
		if entries[i].SkillIndex != entries[j].SkillIndex {
			return entries[i].SkillIndex < entries[j].SkillIndex
		}
		if entries[i].State != entries[j].State {
			return entries[i].State < entries[j].State
		}
		return entries[i].ScriptPath < entries[j].ScriptPath
	})

	out, err := json.MarshalIndent(struct {
		Enabled       bool                     `json:"enabled"`
		MaxSkillLevel int                      `json:"max_skill_level"`
		Skills        []partySkillCatalogEntry `json:"skills"`
	}{Enabled: *raw.Enabled, MaxSkillLevel: *raw.MaxSkillLevel, Skills: entries}, "", "  ")
	if err != nil {
		return false, err
	}
	out = append(out, '\n')
	report, err := parsePartySkillCatalog(out)
	if err != nil {
		return false, err
	}
	if len(report.Issues) > 0 {
		return false, &PartySkillCatalogValidationError{Issues: report.Issues}
	}
	if bytes.Equal(data, out) {
		return false, nil
	}
	if err := atomicfile.WriteFile(path, out, 0644); err != nil {
		return false, err
	}
	return true, nil
}

func partySkillRuntimeKey(job, skillIndex, state int, scriptPath string) string {
	return strconv.Itoa(job) + ":" + strconv.Itoa(skillIndex) + ":" + strconv.Itoa(state) + ":" + normalizePartySkillCatalogPath(scriptPath)
}

func normalizePartySkillCatalogPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return strings.ToLower(strings.Trim(value, "/"))
}

func partySkillDisplayName(scriptPath string) string {
	name := strings.TrimSuffix(pathpkg.Base(normalizePartySkillCatalogPath(scriptPath)), pathpkg.Ext(scriptPath))
	if name == "" || name == "." {
		return "skill"
	}
	return name
}

func partySkillJobLabel(job int) string {
	labels := [...]string{"男鬼剑士", "女格斗家", "男神枪手", "女魔法师", "男圣职者", "女神枪手", "暗夜使者", "男格斗家", "男魔法师", "黑暗武士", "缔造者"}
	if job >= 0 && job < len(labels) {
		return labels[job]
	}
	return "职业 " + strconv.Itoa(job)
}

func decodePartySkillCatalogDocument(data []byte) (partySkillCatalogDocument, error) {
	var raw partySkillCatalogDocument
	if err := foundationconfig.DecodeJSONBytes(data, &raw); err != nil {
		return partySkillCatalogDocument{}, err
	}
	if raw.Enabled == nil || raw.MaxSkillLevel == nil || raw.Skills == nil {
		return partySkillCatalogDocument{}, fmt.Errorf("party skill catalog requires enabled, max_skill_level, and skills")
	}
	if *raw.MaxSkillLevel < 1 || *raw.MaxSkillLevel > maxSafePartySkillLevel {
		return partySkillCatalogDocument{}, fmt.Errorf("party skill catalog max_skill_level must be between 1 and %d", maxSafePartySkillLevel)
	}
	return raw, nil
}

func decodePartySkillCatalogEntry(index int, data []byte) (partySkillCatalogEntry, error) {
	var raw partySkillCatalogEntryDocument
	if err := foundationconfig.DecodeJSONBytes(data, &raw); err != nil {
		return partySkillCatalogEntry{}, fmt.Errorf("party skill catalog entry %d: %w", index, err)
	}
	if raw.Job == nil || raw.SkillIndex == nil || raw.State == nil || raw.Level == nil {
		return partySkillCatalogEntry{}, fmt.Errorf("party skill catalog entry %d requires job, skill_index, state, and level", index)
	}
	var stateData json.RawMessage
	if raw.StateData != nil {
		stateData = append(json.RawMessage(nil), (*raw.StateData)...)
	}
	if raw.Enabled == nil {
		return partySkillCatalogEntry{}, fmt.Errorf("party skill catalog entry %d requires enabled", index)
	}
	return partySkillCatalogEntry{
		ID: raw.ID, Enabled: *raw.Enabled, JobLabel: raw.JobLabel,
		Disabled: raw.Disabled, Job: *raw.Job, SkillIndex: *raw.SkillIndex,
		State: *raw.State, Level: *raw.Level, Name: raw.Name,
		ScriptPath: raw.ScriptPath, StateData: stateData, Risk: raw.Risk,
	}, nil
}

func SetPartySkillCatalogEnabled(path string, enabled bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	raw, err := decodePartySkillCatalogDocument(data)
	if err != nil {
		return err
	}
	report, err := parsePartySkillCatalog(data)
	if err != nil {
		return err
	}
	if len(report.Issues) > 0 {
		return &PartySkillCatalogValidationError{Issues: report.Issues}
	}
	raw.Enabled = &enabled
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	report, err = parsePartySkillCatalog(out)
	if err != nil {
		return err
	}
	if len(report.Issues) > 0 {
		return &PartySkillCatalogValidationError{Issues: report.Issues}
	}
	return atomicfile.WriteFile(path, out, 0644)
}

func (r *PartySkillCatalogReport) addPartySkillIssue(index int, entry partySkillCatalogEntry, reason string) {
	r.Issues = append(r.Issues, PartySkillCatalogIssue{
		Index: index, Job: entry.Job, SkillIndex: entry.SkillIndex, State: entry.State, Reason: reason,
	})
}

func invalidPartySkillEntryReason(entry partySkillCatalogEntry) string {
	switch {
	case entry.Job < 0 || entry.Job > 255:
		return fmt.Sprintf("job %d is outside 0..255", entry.Job)
	case entry.Level <= 0 || entry.Level > maxSafePartySkillLevel:
		return fmt.Sprintf("level %d is outside 1..%d", entry.Level, maxSafePartySkillLevel)
	case entry.SkillIndex <= 0 || entry.SkillIndex > 255:
		return fmt.Sprintf("skill_index %d is outside 1..255", entry.SkillIndex)
	case entry.State < 0 || entry.State > 255:
		return fmt.Sprintf("state %d is outside 0..255", entry.State)
	case entry.Risk < 0 || entry.Risk > 2:
		return fmt.Sprintf("risk %d is outside 0..2", entry.Risk)
	default:
		return ""
	}
}

func decodePartySkillStateData(raw json.RawMessage) ([]int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []int
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("invalid state_data: %w", err)
	}
	return values, nil
}

func partySkillStateData(values []int) ([]byte, error) {
	if len(values) > 3 {
		return nil, fmt.Errorf("state_data has %d values, maximum is 3", len(values))
	}
	data := make([]byte, 0, len(values)*3)
	for _, value := range values {
		if value < 0 || value > 0xffffff {
			return nil, fmt.Errorf("state_data value %d is outside 0..16777215", value)
		}
		data = append(data, byte(value), byte(value>>8), byte(value>>16))
	}
	return data, nil
}
