package robotconfig

import "strings"

func stripBOM(text string) string {
	return strings.TrimPrefix(text, "\ufeff")
}

func UpdateINIText(text string, values map[string]string) string {
	lines := strings.Split(strings.ReplaceAll(stripBOM(text), "\r\n", "\n"), "\n")
	section := ""
	seen := make(map[string]bool, len(values))
	sectionLine := make(map[string]int)
	lastInSection := make(map[string]int)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if end := strings.IndexByte(trimmed, ']'); end > 1 {
				section = strings.TrimSpace(trimmed[1:end])
				sectionLine[section] = i
				lastInSection[section] = i
			}
			continue
		}
		if section != "" && trimmed != "" {
			lastInSection[section] = i
		}
		if section == "" || trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if idx := strings.IndexByte(trimmed, '='); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			fullKey := section + "." + key
			value, ok := values[fullKey]
			if !ok {
				continue
			}
			prefix := line[:strings.Index(line, "=")+1]
			lines[i] = prefix + " " + value
			seen[fullKey] = true
		}
	}
	for fullKey, value := range values {
		if seen[fullKey] {
			continue
		}
		parts := strings.SplitN(fullKey, ".", 2)
		if len(parts) != 2 {
			continue
		}
		section, key := parts[0], parts[1]
		line := key + " = " + value
		if _, ok := sectionLine[section]; !ok {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "["+section+"]", line)
			sectionLine[section] = len(lines) - 2
			lastInSection[section] = len(lines) - 1
			continue
		}
		insertAt := lastInSection[section] + 1
		lines = append(lines[:insertAt], append([]string{line}, lines[insertAt:]...)...)
		for s, idx := range sectionLine {
			if idx >= insertAt {
				sectionLine[s] = idx + 1
			}
		}
		for s, idx := range lastInSection {
			if idx >= insertAt {
				lastInSection[s] = idx + 1
			}
		}
		lastInSection[section] = insertAt
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func PublicText(text string) string {
	hidden := map[string]bool{
		"auto.auto_game_port_stable_sec":       true,
		"auto.auto_game_port_check_timeout_ms": true,
		"auto.auto_move_interval_min_sec":      true,
		"auto.auto_move_interval_max_sec":      true,
		"auto.auto_store_probability_percent":  true,
		"auto.auto_store_interval_min_sec":     true,
		"auto.auto_store_interval_max_sec":     true,
		"auto.auto_store_duration_sec":         true,
		"auto.auto_store_tick_sec":             true,
		"auto.auto_store_max_position_tries":   true,
		"auto.auto_store_fail_cooldown_sec":    true,
		"scheduler.bad_recover_sec":            true,
		"scheduler.bad_failures":               true,
		"scheduler.metrics_interval_sec":       true,
		"scheduler.store_concurrent":           true,
		"scheduler.online_batch_size":          true,
		"scheduler.online_start_rate":          true,
		"scheduler.online_fill_timeout_sec":    true,
		"scheduler.breaker_abnormal_percent":   true,
		"scheduler.breaker_pause_sec":          true,
		"scheduler.breaker_release_batch":      true,
		"scheduler.breaker_floor_percent":      true,
		"scheduler.port_down_release_batch":    true,
		"system.actor_poll_ms":                 true,
		"system.manual_action_timeout_sec":     true,
		"system.packet_rate_per_sec":           true,
	}
	lines := strings.Split(strings.ReplaceAll(stripBOM(text), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if end := strings.IndexByte(trimmed, ']'); end > 1 {
				section = strings.TrimSpace(trimmed[1:end])
			}
			out = append(out, line)
			continue
		}
		if section != "" && trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, ";") {
			if idx := strings.IndexByte(trimmed, '='); idx > 0 {
				key := strings.TrimSpace(trimmed[:idx])
				if hidden[section+"."+key] {
					continue
				}
			}
		}
		out = append(out, line)
	}
	result := strings.Join(out, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}
