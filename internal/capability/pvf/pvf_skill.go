package pvf

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"robot/internal/shared"
)

type SkillState = shared.SkillState

var (
	loadStatePathRE   = regexp.MustCompile(`(?i)^sqr/character/.+load_state\.nut$`)
	pushStateRE       = regexp.MustCompile(`(?is)IRDSQRCharacter\.pushState\s*\(\s*([^,]+),\s*["']([^"']+)["']\s*,\s*["'][^"']*["']\s*,\s*([^,]+),\s*([^\)]+)\)`)
	scriptReferenceRE = regexp.MustCompile(`(?i)["'](character/[^"']+\.nut)["']`)
	symbolRE          = regexp.MustCompile(`(?m)^\s*(?:const\s+)?([A-Z][A-Z0-9_]*)\s*(?:<-|=)\s*(-?\d+)\s*;?`)
)

func setSkillStateCatalog(entries []SkillState) {
	shared.SetSkillStates(entries)
}

func extractSkillStateCatalog(archive *pvfArchive) []SkillState {
	if archive == nil {
		return nil
	}
	referenced := make(map[string]string)
	loadStates := make([]string, 0)
	for path := range archive.files {
		if !loadStatePathRE.MatchString(path) {
			continue
		}
		text := archive.text(path)
		if strings.TrimSpace(text) == "" {
			continue
		}
		loadStates = append(loadStates, text)
		for _, match := range scriptReferenceRE.FindAllStringSubmatch(text, -1) {
			ref := normalizePVFPath("sqr/" + match[1])
			if refText := archive.text(ref); strings.TrimSpace(refText) != "" {
				referenced[ref] = refText
			}
		}
	}
	symbols := skillStateSymbols(referenced)
	type stateKey struct {
		job, skill, state int
		path              string
	}
	seen := make(map[stateKey]bool)
	entries := make([]SkillState, 0)
	for _, text := range loadStates {
		for _, match := range pushStateRE.FindAllStringSubmatch(text, -1) {
			job, jobOK := resolveSkillStateValue(match[1], symbols)
			state, stateOK := resolveSkillStateValue(match[3], symbols)
			skill, skillOK := resolveSkillStateValue(match[4], symbols)
			path := normalizePVFPath("sqr/" + match[2])
			if !jobOK || !stateOK || !skillOK || job < 0 || skill <= 0 || skill > 255 || state < 0 || state > 255 || strings.TrimSpace(referenced[path]) == "" {
				continue
			}
			key := stateKey{job: job, skill: skill, state: state, path: path}
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, SkillState{Job: job, SkillIndex: skill, State: state, ScriptPath: path})
		}
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
	return entries
}

func skillStateSymbols(scripts map[string]string) map[string]int {
	symbols := map[string]int{
		"ENUM_CHARACTERJOB_SWORDMAN":         0,
		"ENUM_CHARACTERJOB_FIGHTER":          1,
		"ENUM_CHARACTERJOB_GUNNER":           2,
		"ENUM_CHARACTERJOB_MAGE":             3,
		"ENUM_CHARACTERJOB_PRIEST":           4,
		"ENUM_CHARACTERJOB_AT_GUNNER":        5,
		"ENUM_CHARACTERJOB_THIEF":            6,
		"ENUM_CHARACTERJOB_AT_FIGHTER":       7,
		"ENUM_CHARACTERJOB_AT_MAGE":          8,
		"ENUM_CHARACTERJOB_DEMONIC_SWORDMAN": 9,
		"ENUM_CHARACTERJOB_CREATOR_MAGE":     10,
	}
	for _, text := range scripts {
		for _, match := range symbolRE.FindAllStringSubmatch(text, -1) {
			value, err := strconv.Atoi(match[2])
			if err == nil {
				if _, exists := symbols[match[1]]; !exists {
					symbols[match[1]] = value
				}
			}
		}
	}
	return symbols
}

func resolveSkillStateValue(raw string, symbols map[string]int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if value, err := strconv.Atoi(raw); err == nil {
		return value, true
	}
	value, ok := symbols[raw]
	return value, ok
}
