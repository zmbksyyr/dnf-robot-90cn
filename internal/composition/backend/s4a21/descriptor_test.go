package s4a21

import "testing"

func TestDescriptorOwnsLocalizedSettingMetadata(t *testing.T) {
	settings := Info().Settings
	if len(settings) == 0 {
		t.Fatal("S4A21 settings are missing")
	}
	for _, setting := range settings {
		if setting.Key == "" || setting.Label == "" || setting.LabelZH == "" {
			t.Fatalf("setting metadata is incomplete: %+v", setting)
		}
		if setting.Hint == "" || setting.HintZH == "" {
			t.Fatalf("setting hint is incomplete: %+v", setting)
		}
	}
}
