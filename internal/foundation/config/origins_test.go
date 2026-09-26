package config

import "testing"

func TestParseAllowedOriginsNormalizesAndValidates(t *testing.T) {
	origins, err := parseAllowedOrigins(" https://Panel.Example.com , http://127.0.0.1:8112/ ")
	if err != nil {
		t.Fatal(err)
	}
	if len(origins) != 2 || origins[0] != "https://panel.example.com" || origins[1] != "http://127.0.0.1:8112" {
		t.Fatalf("parsed origins = %v", origins)
	}
	if empty, err := parseAllowedOrigins("  "); err != nil || len(empty) != 0 {
		t.Fatalf("empty origins = %v err=%v", empty, err)
	}
	for _, invalid := range []string{"panel.example.com", "https://", "https://panel.example.com/path", "ftp://panel.example.com", "https://panel.example.com?x=1"} {
		if _, err := parseAllowedOrigins(invalid); err == nil {
			t.Fatalf("invalid origin %q was accepted", invalid)
		}
	}
}
