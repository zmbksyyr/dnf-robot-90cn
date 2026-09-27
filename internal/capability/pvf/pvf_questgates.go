package pvf

import (
	"sort"
	"strconv"
	"strings"
)

// QuestGates is the fixed quest state that removes every quest-based dungeon
// gate. World map persistent gates are marked completed; world map
// [in progress] entries and dungeon [quest connection] ids stay active. An
// active quest satisfies either admission rule, so active wins on overlap.
type QuestGates struct {
	CompletedQuestIDs []int
	ActiveQuestIDs    []int
}

func (g QuestGates) Empty() bool {
	return len(g.CompletedQuestIDs) == 0 && len(g.ActiveQuestIDs) == 0
}

// ProjectQuestGates reads the released world maps (worldmap/worldmap.lst) and
// the dungeon list (dungeon/dungeon.lst) to derive the gate quest set.
func ProjectQuestGates(archive TownTextArchive) QuestGates {
	if archive == nil {
		return QuestGates{}
	}
	persistent, inProgress := projectWorldMapGates(archive)
	connections := projectDungeonQuestConnections(archive)
	active := make(map[int]struct{}, len(inProgress)+len(connections))
	for _, questID := range inProgress {
		if questID > 0 {
			active[questID] = struct{}{}
		}
	}
	for _, questID := range connections {
		if questID > 0 {
			active[questID] = struct{}{}
		}
	}
	completed := make(map[int]struct{})
	for _, questID := range persistent {
		if questID <= 0 {
			continue
		}
		if _, isActive := active[questID]; isActive {
			continue
		}
		completed[questID] = struct{}{}
	}
	return QuestGates{
		CompletedQuestIDs: sortedKeys(completed),
		ActiveQuestIDs:    sortedKeys(active),
	}
}

// projectWorldMapGates parses every released .wdm [dungeon] section. Entries
// are a continuous token stream of `dungeonId [[in progress]] [questId]`.
func projectWorldMapGates(archive TownTextArchive) (persistent, inProgress []int) {
	lst, _ := archive.ReadText("worldmap/worldmap.lst")
	if lst == "" {
		return nil, nil
	}
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		file := strings.TrimSpace(match[2])
		if file == "" || !strings.EqualFold(pvfPathExtension(file), ".wdm") {
			continue
		}
		text, _ := archive.ReadText(normalizePVFPath("worldmap/" + file))
		section, ok := sectionText(text, "dungeon")
		if !ok {
			continue
		}
		tokens := strings.Fields(section)
		for index := 0; index < len(tokens); {
			dungeonID, err := strconv.Atoi(tokens[index])
			if err != nil {
				index++
				continue
			}
			index++
			marker := false
			if index < len(tokens) && strings.EqualFold(tokens[index], "[in progress]") {
				marker = true
				index++
			} else if index+1 < len(tokens) && strings.EqualFold(tokens[index], "[in") && strings.EqualFold(tokens[index+1], "progress]") {
				marker = true
				index += 2
			}
			questID := 0
			if index < len(tokens) {
				if value, err := strconv.Atoi(tokens[index]); err == nil {
					questID = value
					index++
				}
			}
			if dungeonID <= 0 || questID <= 0 {
				continue
			}
			if marker {
				inProgress = append(inProgress, questID)
			} else {
				persistent = append(persistent, questID)
			}
		}
	}
	return persistent, inProgress
}

// projectDungeonQuestConnections collects the second value of every dungeon
// [quest connection] block, including maze-level blocks.
func projectDungeonQuestConnections(archive TownTextArchive) []int {
	lst, _ := archive.ReadText("dungeon/dungeon.lst")
	if lst == "" {
		return nil
	}
	result := make([]int, 0)
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		path := strings.TrimSpace(match[2])
		if path == "" || !strings.EqualFold(pvfPathExtension(path), ".dgn") {
			continue
		}
		text, _ := archive.ReadText(normalizePVFPath("dungeon/" + path))
		result = append(result, parseQuestConnectionIDs(text)...)
	}
	return result
}

func parseQuestConnectionIDs(body string) []int {
	const tag = "[quest connection]"
	lower := strings.ToLower(body)
	result := make([]int, 0, 2)
	searchFrom := 0
	for {
		index := strings.Index(lower[searchFrom:], tag)
		if index < 0 {
			break
		}
		index += searchFrom
		searchFrom = index + len(tag)
		block := body[searchFrom:]
		if end := strings.Index(block, "["); end >= 0 {
			block = block[:end]
		}
		values := parseAllInts(block)
		if len(values) >= 2 && values[1] > 0 {
			result = append(result, values[1])
		}
	}
	return result
}

func parseAllInts(block string) []int {
	fields := strings.Fields(block)
	result := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		result = append(result, value)
	}
	return result
}

func sectionText(text, name string) (string, bool) {
	lower := strings.ToLower(text)
	startTag := "[" + name + "]"
	start := strings.Index(lower, startTag)
	if start < 0 {
		return "", false
	}
	start += len(startTag)
	if end := strings.Index(lower[start:], "[/"+name+"]"); end >= 0 {
		return stripLineComments(text[start : start+end]), true
	}
	if next := strings.Index(text[start:], "["); next >= 0 {
		return stripLineComments(text[start : start+next]), true
	}
	return stripLineComments(text[start:]), true
}

func stripLineComments(section string) string {
	lines := strings.Split(section, "\n")
	for index, line := range lines {
		if comment := strings.Index(line, "//"); comment >= 0 {
			lines[index] = line[:comment]
		}
	}
	return strings.Join(lines, "\n")
}

func pvfPathExtension(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if index := strings.LastIndex(path, "."); index >= 0 {
		return path[index:]
	}
	return ""
}

func sortedKeys(values map[int]struct{}) []int {
	if len(values) == 0 {
		return nil
	}
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}
