package service

import (
	"testing"

	"sekaitext/backend/internal/model"
)

// Compare 校对/合意: each sub-line yields exactly ONE row carrying Baseline
// (refer text) + Text (check text) + a merged diff. No row splitting.
func TestCompareProducesOneRowPerSubline(t *testing.T) {
	e := &EditorService{}
	refer := []model.DstTalk{{Idx: 3, Speaker: "瑞希", Text: "原文", Start: true, End: true, DstIdx: 0}}
	check := []model.DstTalk{{Idx: 3, Speaker: "瑞希", Text: "改过的原文", Start: true, End: true, DstIdx: 0}}

	out := e.CompareText(refer, check, 1)
	if len(out) != 1 {
		t.Fatalf("expected exactly 1 row, got %d: %+v", len(out), out)
	}
	r := out[0]
	if r.Text != "改过的原文" {
		t.Errorf("row Text should be check text, got %q", r.Text)
	}
	if r.Baseline != "原文" {
		t.Errorf("row Baseline should be refer text, got %q", r.Baseline)
	}
	if len(r.DiffParts) == 0 {
		t.Errorf("differing texts should carry a diff")
	}
	if r.CheckMode || r.Proofread != nil {
		t.Errorf("new model must not set CheckMode/Proofread: %+v", r)
	}
}

// refer==check (fresh load of a 翻译 draft into 校对 mode): one row, no diff.
func TestCompareIdenticalNoDiff(t *testing.T) {
	e := &EditorService{}
	aligned := []model.DstTalk{{Idx: 3, Text: "原文", Start: true, End: true, DstIdx: 0}}
	out := e.CompareText(aligned, aligned, 1)
	if len(out) != 1 {
		t.Fatalf("expected 1 row, got %d", len(out))
	}
	if out[0].Baseline != "原文" {
		t.Errorf("baseline should be set even when identical, got %q", out[0].Baseline)
	}
	if len(out[0].DiffParts) != 0 {
		t.Errorf("identical text must have no diff, got %+v", out[0].DiffParts)
	}
}

// Editing in 校对/合意 updates in place: row count stays fixed, diff recomputed
// against the row's own Baseline. Editing back to the baseline clears the diff.
func TestChangeTextInPlace(t *testing.T) {
	e := &EditorService{}
	talks := []model.DstTalk{
		{Idx: 3, Speaker: "瑞希", Text: "原文", Start: true, End: true, Save: true, Checked: true, DstIdx: 0, Baseline: "原文"},
	}
	dst := []model.DstTalk{{Idx: 3, Speaker: "瑞希", Text: "原文", DstIdx: 0, Baseline: "原文"}}

	out, odst := e.ChangeText(0, "改过", 1, talks, dst, nil)
	if len(out) != 1 {
		t.Fatalf("edit must not change row count, got %d", len(out))
	}
	if len(odst) != 1 {
		t.Fatalf("dst row count must stay 1, got %d", len(odst))
	}
	if out[0].Text != "改过" {
		t.Errorf("text should update, got %q", out[0].Text)
	}
	if len(out[0].DiffParts) == 0 {
		t.Errorf("edit differing from baseline should carry diff")
	}

	// Edit back to baseline → diff cleared, still one row.
	back, _ := e.ChangeText(0, "原文", 1, out, odst, nil)
	if len(back) != 1 {
		t.Fatalf("still one row, got %d", len(back))
	}
	if len(back[0].DiffParts) != 0 {
		t.Errorf("editing back to baseline must clear diff, got %+v", back[0].DiffParts)
	}
}

// 翻译 mode (0) must be unaffected: in-place update, no baseline/diff, no split.
func TestTranslateModeNoSplit(t *testing.T) {
	e := &EditorService{}
	talks := []model.DstTalk{{Idx: 1, Speaker: "瑞希", Text: "原文", Start: true, End: true, Save: true, Checked: true, DstIdx: 0}}
	dst := []model.DstTalk{{Idx: 1, Speaker: "瑞希", Text: "原文", DstIdx: 0}}
	out, _ := e.ChangeText(0, "改过", 0, talks, dst, nil)
	if len(out) != 1 {
		t.Fatalf("translate mode must not split, got %d rows", len(out))
	}
	if out[0].CheckMode || out[0].Proofread != nil || len(out[0].DiffParts) != 0 {
		t.Errorf("translate row must stay plain, got %+v", out[0])
	}
}
