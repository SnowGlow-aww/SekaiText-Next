package service

import (
	"strings"
	"testing"
)

// TestFlashbackClueResolution guards the flashback clue dictionary against the
// regression where InferVoiceEventID was never called, leaving clueDict empty
// so every event flashback rendered "未知活动". It also covers the assetName
// fallback that lets World Link events (wl_3rd_groupN) resolve even though area
// talks don't cover them.
//
// Skips automatically if the local catalog isn't present (CI without data).
func TestFlashbackClueResolution(t *testing.T) {
	const catalog = "/Users/amia/Library/Application Support/com.is14w.sekaitext/resources/catalog"
	lm := NewListManager(catalog)
	if len(lm.Events) == 0 {
		t.Skip("catalog not available")
	}
	fb := NewFlashbackAnalyzer(lm)

	if len(fb.clueDict) == 0 {
		t.Fatal("clueDict is empty — InferVoiceEventID likely not called")
	}

	cases := []struct {
		voice      string
		wantInHint string // substring expected somewhere in the hints
	}{
		{"voice_ev_wl_shuffle_03_01_01_09", "Into the New Light"}, // area-talk derived
		{"voice_ev_wl_3rd_group3_01_05_09", "Into the New Light"},  // assetName fallback
		{"voice_ev_wl_3rd_group1_01_05_09", "約束のPasserelle"},      // assetName fallback
		{"voice_ev_night_19_08_01_19", "傷だらけの手で"},               // numbered event
	}
	for _, c := range cases {
		clue, ignore := fb.GetClueFromVoiceID(c.voice)
		if ignore || clue == "" {
			t.Errorf("%s: no clue extracted", c.voice)
			continue
		}
		hints := fb.GetClueHints(clue, "zh-cn")
		joined := strings.Join(hints, " | ")
		if strings.Contains(joined, "未知活动") || strings.Contains(joined, "未知来源") {
			t.Errorf("%s -> clue=%q still unresolved: %v", c.voice, clue, hints)
			continue
		}
		if !strings.Contains(joined, c.wantInHint) {
			t.Errorf("%s -> clue=%q hints=%v, want substring %q", c.voice, clue, hints, c.wantInHint)
		}
	}
}
