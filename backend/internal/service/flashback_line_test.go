package service

import "testing"

// TestFlashbackSourceLine verifies the flashback source-line locator: a voice ID
// resolves to its CDN source scenario and the exact 1-based line where it appears
// when that scenario is parsed (the line the translator sees on load). Uses the
// locally cached group3 scenario; skips when the catalog/data isn't present.
func TestFlashbackSourceLine(t *testing.T) {
	const catalog = "/Users/amia/Library/Application Support/com.is14w.sekaitext/resources/catalog"
	const dataDir = "/Users/amia/Library/Application Support/com.is14w.sekaitext/resources/data"

	lm := NewListManager(catalog)
	if len(lm.Events) == 0 {
		t.Skip("catalog not available")
	}
	fb := NewFlashbackAnalyzer(lm)

	// URL resolution must point at the event's source scenario.
	url, fileName, asset, ok := fb.ResolveVoiceSourceURL("voice_ev_wl_shuffle_03_01_05_09", "haruki")
	if !ok {
		t.Fatal("ResolveVoiceSourceURL failed")
	}
	if asset != "wl_3rd_group3_01" || fileName != "wl_3rd_group3_01.json" {
		t.Errorf("unexpected asset/file: %s / %s", asset, fileName)
	}
	if url == "" {
		t.Error("empty URL")
	}

	jl := NewJsonLoaderService(fb)
	jl.SetSourceLocator(NewDownloader(dataDir), dataDir)

	// Verified against the real parse of wl_3rd_group3_01: this voice sits on
	// parsed line 15 (こはね: え……). The voice-ID-encoded number (05) is NOT the
	// physical line — it differs because scene/separator lines shift positions.
	if got := jl.locateVoiceLine("voice_ev_wl_shuffle_03_01_05_09"); got != 15 {
		t.Errorf("locateVoiceLine = %d, want 15", got)
	}
	// Cached second call must agree.
	if got := jl.locateVoiceLine("voice_ev_wl_shuffle_03_01_05_09"); got != 15 {
		t.Errorf("cached locateVoiceLine = %d, want 15", got)
	}
}
