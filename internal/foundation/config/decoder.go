package config

import (
	"fmt"
	"strconv"
	"strings"
)

type decoderKey struct {
	section string
	key     string
}

// Decoder strictly consumes known INI settings. Invalid typed values and any
// unconsumed section/key are rejected instead of silently falling back.
type Decoder struct {
	label        string
	sections     []INISection
	entries      []INIEntry
	values       map[decoderKey]INIEntry
	usedSections map[string]bool
	used         map[decoderKey]bool
	err          error
}

func NewDecoder(ini *INIConfig, label string) *Decoder {
	var sections []INISection
	var entries []INIEntry
	if ini != nil {
		sections = ini.Sections()
		entries = ini.Entries()
	}
	if strings.TrimSpace(label) == "" {
		label = "INI"
	}
	d := &Decoder{
		label: label, sections: sections, entries: entries,
		values:       make(map[decoderKey]INIEntry, len(entries)),
		usedSections: make(map[string]bool, len(sections)),
		used:         make(map[decoderKey]bool, len(entries)),
	}
	for _, entry := range entries {
		d.values[decoderKey{section: entry.Section, key: entry.Key}] = entry
	}
	return d
}

func (d *Decoder) String(section, key, fallback string) string {
	entry, ok := d.lookup(section, key)
	if !ok {
		return fallback
	}
	return entry.Value
}

func (d *Decoder) Int(section, key string, fallback int) int {
	entry, ok := d.lookup(section, key)
	if !ok {
		return fallback
	}
	value, err := strconv.Atoi(entry.Value)
	if err != nil {
		d.reject(entry, "must be an integer")
		return fallback
	}
	return value
}

func (d *Decoder) Bool(section, key string, fallback bool) bool {
	entry, ok := d.lookup(section, key)
	if !ok {
		return fallback
	}
	var value bool
	switch entry.Value {
	case "true":
		value = true
	case "false":
		value = false
	default:
		d.reject(entry, "must be true or false")
		return fallback
	}
	return value
}

func (d *Decoder) IntList(section, key string, fallback []int) []int {
	entry, ok := d.lookup(section, key)
	if !ok {
		return fallback
	}
	if strings.TrimSpace(entry.Value) == "" {
		d.reject(entry, "must contain at least one integer")
		return fallback
	}
	parts := strings.Split(entry.Value, ",")
	out := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			d.reject(entry, "must be a comma-separated integer list")
			return fallback
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			d.reject(entry, "must be a comma-separated integer list")
			return fallback
		}
		if seen[value] {
			d.reject(entry, fmt.Sprintf("must not contain duplicate integer %d", value))
			return fallback
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		d.reject(entry, "must contain at least one integer")
		return fallback
	}
	return out
}

// Check records a semantic validation error against a known setting.
func (d *Decoder) Check(section, key string, valid bool, reason string) {
	if valid || d.err != nil {
		return
	}
	if entry, ok := d.values[decoderKey{section: section, key: key}]; ok {
		d.reject(entry, reason)
		return
	}
	d.err = fmt.Errorf("%s [%s] %s: %s", d.label, section, key, reason)
}

// Obsolete consumes a known-but-removed setting so configuration files written
// by an older version keep loading. Callers surface the returned keys as a
// warning; any other unknown key is still rejected by Validate.
func (d *Decoder) Obsolete(section, key string) bool {
	_, ok := d.lookup(section, key)
	return ok
}

func (d *Decoder) Validate() error {
	if d.err != nil {
		return d.err
	}
	for _, section := range d.sections {
		if !d.usedSections[section.Name] {
			return fmt.Errorf("%s line %d [%s]: unknown section", d.label, section.Line, section.Name)
		}
	}
	for _, entry := range d.entries {
		key := decoderKey{section: entry.Section, key: entry.Key}
		if !d.used[key] {
			return fmt.Errorf("%s line %d [%s] %s: unknown setting", d.label, entry.Line, entry.Section, entry.Key)
		}
	}
	return nil
}

func (d *Decoder) lookup(section, key string) (INIEntry, bool) {
	d.usedSections[section] = true
	configKey := decoderKey{section: section, key: key}
	d.used[configKey] = true
	entry, ok := d.values[configKey]
	return entry, ok
}

func (d *Decoder) reject(entry INIEntry, reason string) {
	if d.err == nil {
		d.err = fmt.Errorf("%s line %d [%s] %s: %s", d.label, entry.Line, entry.Section, entry.Key, reason)
	}
}
