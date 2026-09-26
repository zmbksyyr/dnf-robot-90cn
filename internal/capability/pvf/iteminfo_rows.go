package pvf

import (
	"strconv"
	"strings"
)

// normalizeItemInfoRows extracts the fixed 17-field rows used by the PVF
// catalog. It intentionally does not export or publish an iteminfo.dat file.
func normalizeItemInfoRows(text string) string {
	tokens := tokenizeItemInfo(text)
	rows := make([]string, 0, len(tokens)/17)
	for i := 0; i+16 < len(tokens); {
		if tokens[i] == "#PVF_File" {
			i++
			continue
		}
		if _, err := strconv.Atoi(tokens[i]); err != nil {
			i++
			continue
		}
		if _, err := strconv.Atoi(tokens[i+16]); err != nil {
			i++
			continue
		}
		rows = append(rows, strings.Join(tokens[i:i+17], " "))
		i += 17
	}
	if len(rows) == 0 {
		return text
	}
	return strings.Join(rows, "\n")
}

func parseItemInfoRows(text string) map[int][]string {
	tokens := tokenizeItemInfo(text)
	rows := make(map[int][]string, len(tokens)/17)
	for i := 0; i+16 < len(tokens); {
		if tokens[i] == "#PVF_File" {
			i++
			continue
		}
		id, err := strconv.Atoi(tokens[i])
		if err != nil {
			i++
			continue
		}
		if _, err := strconv.Atoi(tokens[i+16]); err != nil {
			i++
			continue
		}
		rows[id] = append([]string(nil), tokens[i:i+17]...)
		i += 17
	}
	return rows
}

func tokenizeItemInfo(text string) []string {
	tokens := make([]string, 0, 1024)
	for i := 0; i < len(text); {
		for i < len(text) && isItemInfoSpace(text[i]) {
			i++
		}
		if i >= len(text) {
			break
		}
		if text[i] == '`' {
			start := i
			i++
			for i < len(text) && text[i] != '`' {
				i++
			}
			if i < len(text) {
				i++
			}
			tokens = append(tokens, text[start:i])
			continue
		}
		start := i
		for i < len(text) && !isItemInfoSpace(text[i]) {
			i++
		}
		tokens = append(tokens, text[start:i])
	}
	return tokens
}

func isItemInfoSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}
