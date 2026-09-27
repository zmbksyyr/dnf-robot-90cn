package pvf

import (
	"regexp"
	"strconv"
	"strings"
)

// pvfListEntryPattern matches one `<id> `path“ entry of a PVF LST file. The
// game server's character list uses exactly this shape (see its
// CharacterStatComputer / CharacterSkillProfile loaders).
var pvfListEntryPattern = regexp.MustCompile("(\\d+)\\s+`([^`]+)`")

// pvfBacktickPattern extracts every backtick-delimited name.
var pvfBacktickPattern = regexp.MustCompile("`([^`]*)`")

// maxA21FirstGrow mirrors the server's CharacterStatComputer guard: the first
// grow nibble must not exceed 5.
const maxA21FirstGrow = 5

// ProjectJobGrowCatalog mirrors the game server's character/*.chr transfer
// graph: character/character.lst maps a job id to its .chr file, and the
// [growtype name] line lists the base class name followed by every transfer
// branch name. Commented placeholder names ("//...") are declared but not
// released and must not be offered. The result maps a job id to the valid
// first-grow (transfer) values of that job.
func ProjectJobGrowCatalog(archive TownTextArchive) map[int][]int {
	result := make(map[int][]int)
	if archive == nil {
		return result
	}
	lst, _ := archive.ReadText("character/character.lst")
	if lst == "" {
		return result
	}
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		job, err := strconv.Atoi(match[1])
		if err != nil || job < 0 || job > 255 {
			continue
		}
		path := normalizePVFPath("character/" + match[2])
		body, _ := archive.ReadText(path)
		branches := parseJobGrowBranches(body)
		if len(branches) > 0 {
			result[job] = branches
		}
	}
	return result
}

// parseJobGrowBranches reads the [growtype name] line of a .chr file. The
// first backtick name is the base class; each following name is a transfer
// branch whose 1-based position is the first-grow value.
func parseJobGrowBranches(body string) []int {
	lower := strings.ToLower(body)
	start := strings.Index(lower, "[growtype name]")
	if start < 0 {
		return nil
	}
	rest := body[start+len("[growtype name]"):]
	// The server regex treats the names as the first non-empty line after the
	// tag, so accept both the same-line and next-line layouts.
	rest = strings.TrimLeft(rest, " \t\r\n")
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		rest = rest[:end]
	}
	names := pvfBacktickPattern.FindAllStringSubmatch(rest, -1)
	branches := make([]int, 0, len(names))
	for index := 1; index < len(names); index++ {
		name := strings.TrimSpace(names[index][1])
		// Commented entries mark declared-but-unreleased branches; keep the
		// positional value for the remaining names without offering them.
		if name == "" || strings.HasPrefix(name, "//") {
			continue
		}
		if index > maxA21FirstGrow {
			break
		}
		// A branch whose [growtype N] stat section is missing cannot produce
		// character stats (the server requires that row), so do not offer it.
		if !hasPVFTag(body, "growtype "+strconv.Itoa(index+1)) {
			continue
		}
		branches = append(branches, index)
	}
	return branches
}

func hasPVFTag(body, tag string) bool {
	return strings.Contains(strings.ToLower(body), "["+strings.ToLower(tag)+"]")
}
