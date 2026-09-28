package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// INIConfig represents a parsed INI configuration file.
type INIConfig struct {
	comment   byte
	separator byte
	data      map[string]map[string]string // section -> key -> value
	sections  []INISection
	entries   []INIEntry
}

// INISection is one section declaration in source order.
type INISection struct {
	Name string
	Line int
}

// INIEntry is one parsed setting in source order.
type INIEntry struct {
	Section string
	Key     string
	Value   string
	Line    int
}

// Load reads and parses an INI file. Comment char defaults to '#' and separator to '='.
// If filename is empty, an empty config is returned.
func Load(filename string) (*INIConfig, error) {
	cfg := &INIConfig{
		comment:   '#',
		separator: '=',
		data:      make(map[string]map[string]string),
	}

	if filename == "" {
		return cfg, nil
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return parseINI(f)
}

// LoadFromString parses INI content from a string.
func LoadFromString(content string) (*INIConfig, error) {
	return parseINI(strings.NewReader(content))
}

func parseINI(r interface {
	Read([]byte) (int, error)
}) (*INIConfig, error) {
	cfg := &INIConfig{
		comment:   '#',
		separator: '=',
		data:      make(map[string]map[string]string),
	}

	var section string
	seenSections := make(map[string]bool)
	lineNumber := 0
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lineNumber++
		raw := strings.TrimPrefix(scanner.Text(), "\ufeff")

		raw = strings.TrimSpace(raw)
		if raw == "" || raw[0] == '\r' || raw[0] == '\n' || raw[0] == cfg.comment || raw[0] == ';' {
			continue
		}

		if raw[0] == '[' {
			end := strings.IndexByte(raw, ']')
			if end <= 1 {
				return nil, fmt.Errorf("invalid INI section at line %d", lineNumber)
			}
			section = strings.TrimSpace(raw[1:end])
			if section == "" {
				return nil, fmt.Errorf("empty INI section at line %d", lineNumber)
			}
			if seenSections[section] {
				return nil, fmt.Errorf("duplicate INI section %s at line %d", section, lineNumber)
			}
			trailing := strings.TrimSpace(raw[end+1:])
			if trailing != "" && trailing[0] != cfg.comment && trailing[0] != ';' {
				return nil, fmt.Errorf("invalid INI section suffix at line %d", lineNumber)
			}
			seenSections[section] = true
			cfg.sections = append(cfg.sections, INISection{Name: section, Line: lineNumber})
			continue
		}

		idx := strings.IndexByte(raw, cfg.separator)
		if idx < 0 {
			return nil, fmt.Errorf("invalid INI entry at line %d", lineNumber)
		}
		key := strings.TrimSpace(raw[:idx])
		if section == "" || key == "" {
			return nil, fmt.Errorf("invalid INI entry at line %d", lineNumber)
		}
		value := strings.TrimSpace(raw[idx+1:])
		if cfg.data[section] == nil {
			cfg.data[section] = make(map[string]string)
		}
		if _, exists := cfg.data[section][key]; exists {
			return nil, fmt.Errorf("duplicate INI entry %s.%s at line %d", section, key, lineNumber)
		}
		cfg.data[section][key] = value
		cfg.entries = append(cfg.entries, INIEntry{Section: section, Key: key, Value: value, Line: lineNumber})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Sections returns parsed section declarations in source order.
func (c *INIConfig) Sections() []INISection {
	if c == nil || len(c.sections) == 0 {
		return nil
	}
	return append([]INISection(nil), c.sections...)
}

// Entries returns parsed settings in source order.
func (c *INIConfig) Entries() []INIEntry {
	if c == nil || len(c.entries) == 0 {
		return nil
	}
	return append([]INIEntry(nil), c.entries...)
}

// GetString returns the value for the given section and key, or defaultVal if not found.
func (c *INIConfig) GetString(section, key, defaultVal string) string {
	if c == nil || c.data == nil {
		return defaultVal
	}
	if m, ok := c.data[section]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return defaultVal
}
