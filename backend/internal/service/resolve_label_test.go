package service

import "testing"

// TestResolveLabel verifies filename-label -> story-coordinate reverse mapping
// used to auto-load a translation's source scenario on open. Skips without catalog.
func TestResolveLabel(t *testing.T) {
	lm := NewListManager("/Users/amia/Library/Application Support/com.is14w.sekaitext/resources/catalog")
	if len(lm.Events) == 0 {
		t.Skip("catalog not available")
	}
	cases := []struct {
		label     string
		wantIndex string
		wantCh    int
	}{
		{"3rd-group3-01", "205", 0}, // WL event via assetName match
		{"3rd-group1-01", "200", 0},
		{"1-01", "1", 0}, // reverseIndex-episode form
	}
	for _, c := range cases {
		st, idx, ch, ok := lm.ResolveLabel(c.label)
		if !ok {
			t.Errorf("%s: not resolved", c.label)
			continue
		}
		if st != StoryLabelEvent || idx != c.wantIndex || ch != c.wantCh {
			t.Errorf("%s -> type=%s index=%s ch=%d, want event/%s/%d", c.label, st, idx, ch, c.wantIndex, c.wantCh)
		}
	}
	// Unresolvable label must return ok=false (caller keeps manual selection).
	if _, _, _, ok := lm.ResolveLabel("totally-bogus-xyz"); ok {
		t.Error("bogus label should not resolve")
	}
}
