package service

import (
	"strings"
	"testing"

	"sekaitext/backend/internal/model"
)

// TestLeadingSceneSplitPreserved guards the "首行被覆盖" bug: when a translator
// rewrites the opening narration and splits ONE source scene line into several
// lines, CheckLines must merge them back into one entry (aligned to the single
// source slot) WITHOUT dropping any line from the head. Previously the leading
// run was blindly trimmed off the front, deleting the real first line and
// shifting every following row up.
func TestLeadingSceneSplitPreserved(t *testing.T) {
	src := []model.SourceTalk{
		{Speaker: "场景", Text: "ストリートのセカイ"},
		{Speaker: "", Text: ""},
		{Speaker: "场景", Text: "広場"},
		{Speaker: "？？？", Text: "おーい！"},
	}
	// Loaded proofread draft: opening scene split into TWO lines.
	loaded := []model.DstTalk{
		{Idx: 1, Speaker: "场景", Text: "我们跟这家店约好了不会把人拍进去，", Start: true, End: true, Save: true},
		{Idx: 2, Speaker: "场景", Text: "才获准在咖啡馆内拍摄的。", Start: true, End: true, Save: true},
		{Idx: 3, Speaker: "", Text: "", Start: true, End: true, Save: true},
		{Idx: 4, Speaker: "场景", Text: "广场", Start: true, End: true, Save: true},
		{Idx: 5, Speaker: "？？？", Text: "喂——！", Start: true, End: true, Save: true},
	}

	e := NewEditorService()
	aligned := e.CheckLines(src, loaded)

	if len(aligned) == 0 {
		t.Fatal("no aligned talks")
	}
	// First row must still carry the first line's content (merged with the
	// translator's second line), NOT the second line alone.
	if !strings.Contains(aligned[0].Text, "我们跟这家店约好了不会把人拍进去，") {
		t.Errorf("first line lost: aligned[0].Text=%q", aligned[0].Text)
	}
	if !strings.Contains(aligned[0].Text, "才获准在咖啡馆内拍摄的。") {
		t.Errorf("split second line lost: aligned[0].Text=%q", aligned[0].Text)
	}
	// "广场" must remain its own scene line, not be shoved out of place.
	foundGuangchang := false
	for _, tk := range aligned {
		if tk.Text == "广场" {
			foundGuangchang = true
		}
	}
	if !foundGuangchang {
		t.Error("广场 scene line missing after alignment")
	}

	// Round-trip: both split lines must write back to the file verbatim.
	out := e.SerializeContent(aligned, true)
	if strings.HasPrefix(out, "#SekaiText") {
		t.Error("output must not carry #SekaiText header")
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if len(lines) < 2 || lines[0] != "我们跟这家店约好了不会把人拍进去，" || lines[1] != "才获准在咖啡馆内拍摄的。" {
		t.Errorf("split scene lines did not round-trip: first lines=%q", lines[:min(4, len(lines))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
