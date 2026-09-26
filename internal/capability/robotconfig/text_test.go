package robotconfig

import (
	"strings"
	"testing"
)

func TestUpdateINITextUpdatesAndAppends(t *testing.T) {
	input := "[auto]\nauto_actions = true\n\n[system]\npacket_rate_per_sec = 20\n"
	out := UpdateINIText(input, map[string]string{
		"auto.auto_actions":           "false",
		"system.actor_poll_ms":        "1000",
		"scheduler.online_batch_size": "30",
	})
	for _, want := range []string{
		"auto_actions = false",
		"actor_poll_ms = 1000",
		"[scheduler]",
		"online_batch_size = 30",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("updated ini missing %q in:\n%s", want, out)
		}
	}
}

func TestUpdateINITextHandlesUTF8BOM(t *testing.T) {
	input := "\ufeff[create]\nlevel_min = 50\n"
	out := UpdateINIText(input, map[string]string{"create.level_min": "60"})
	if strings.Count(out, "[create]") != 1 {
		t.Fatalf("BOM input produced a duplicate section:\n%s", out)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("updated ini does not parse: %v\n%s", err, out)
	}
	if !strings.Contains(out, "level_min = 60") {
		t.Fatalf("update not applied:\n%s", out)
	}
}

func TestPublicTextHandlesUTF8BOM(t *testing.T) {
	input := "\ufeff[scheduler]\nonline_batch_size = 20\n\n[robot]\nlevel_min = 50\n"
	out := PublicText(input)
	if strings.Contains(out, "online_batch_size") {
		t.Fatalf("BOM input leaked a hidden key:\n%s", out)
	}
	if !strings.Contains(out, "level_min = 50") {
		t.Fatalf("BOM input lost a visible key:\n%s", out)
	}
}

func TestPublicTextHidesAdaptiveKeys(t *testing.T) {
	input := "[auto]\nauto_actions = true\nauto_shout_interval_min_sec = 45\nauto_shout_interval_max_sec = 120\nauto_store_tick_sec = 10\n\n[scheduler]\nonline_batch_size = 20\n\n[robot]\nlevel_min = 50\n"
	out := PublicText(input)
	if strings.Contains(out, "auto_store_tick_sec") || strings.Contains(out, "online_batch_size") {
		t.Fatalf("public text leaked hidden keys:\n%s", out)
	}
	if !strings.Contains(out, "auto_actions = true") ||
		!strings.Contains(out, "auto_shout_interval_min_sec = 45") ||
		!strings.Contains(out, "auto_shout_interval_max_sec = 120") ||
		!strings.Contains(out, "level_min = 50") {
		t.Fatalf("public text removed visible keys:\n%s", out)
	}
}
