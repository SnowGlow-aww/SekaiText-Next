package service

import (
	"testing"

	"sekaitext/backend/internal/model"
)

// These tests pin the DstIdx contract between CompareText and the editing ops:
// every chk-backed row's DstIdx is its TRUE position in checktalks (= the
// dstTalks the frontend keeps and saves), deletion rows carry -1, and editing /
// adding / removing rows never writes into an unrelated dstTalks line. The old
// running-counter scheme drifted ahead of checktalks whenever a ref group had
// more sub-lines than the chk group, after which every later edit overwrote the
// WRONG line in the saved file.

func cmpTalk(idx int, speaker, text string) model.DstTalk {
	return model.DstTalk{Idx: idx, Speaker: speaker, Text: text, Start: true, End: true, Checked: true, Save: true}
}

// ref idx1 has two sub-lines, chk idx1 has one → one deletion row in between.
func compareFixture() (talks, dsttalks []model.DstTalk) {
	e := NewEditorService()
	ref := []model.DstTalk{
		cmpTalk(1, "咲希", "第一行旧文。"),
		cmpTalk(1, "咲希", "被删掉的第二行。"),
		cmpTalk(2, "穗波", "第二句旧文。"),
	}
	chk := []model.DstTalk{
		cmpTalk(1, "咲希", "第一行新文。"),
		cmpTalk(2, "穗波", "第二句新文。"),
	}
	talks = e.CompareText(ref, chk, 2)
	dsttalks = append([]model.DstTalk(nil), chk...)
	return talks, dsttalks
}

func TestCompareTextDstIdxMatchesCheckPositions(t *testing.T) {
	talks, dsttalks := compareFixture()
	if len(talks) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(talks))
	}
	// chk-backed rows point at their true positions in checktalks.
	if talks[0].DstIdx != 0 {
		t.Errorf("row0 (chk line 1) DstIdx = %d, want 0", talks[0].DstIdx)
	}
	if talks[2].DstIdx != 1 {
		t.Errorf("row2 (chk line 2) DstIdx = %d, want 1 (must not drift past the deletion row)", talks[2].DstIdx)
	}
	// The deletion row has no slot.
	if talks[1].DstIdx != -1 {
		t.Errorf("deletion row DstIdx = %d, want -1", talks[1].DstIdx)
	}
	if talks[1].Text != "" {
		t.Errorf("deletion row text = %q, want empty", talks[1].Text)
	}
	_ = dsttalks
}

// Editing the LAST row must update its own line, not a drifted neighbour.
func TestChangeTextAfterDeletionRowWritesCorrectSlot(t *testing.T) {
	e := NewEditorService()
	talks, dsttalks := compareFixture()
	talks, dsttalks = e.ChangeText(2, "第二句合意文。", 2, talks, dsttalks, nil)
	if got := dsttalks[1].Text; got != "第二句合意文。" {
		t.Errorf("dstTalks[1] = %q, want the edited text", got)
	}
	if got := dsttalks[0].Text; got != "第一行新文。" {
		t.Errorf("dstTalks[0] was clobbered: %q", got)
	}
}

// Typing into a deletion row must INSERT a slot at the right position instead
// of overwriting another line or appending at the end.
func TestChangeTextDeletionRowInsertsSlotInPlace(t *testing.T) {
	e := NewEditorService()
	talks, dsttalks := compareFixture()
	talks, dsttalks = e.ChangeText(1, "还原的第二行。", 2, talks, dsttalks, nil)

	if len(dsttalks) != 3 {
		t.Fatalf("expected dstTalks to grow to 3, got %d", len(dsttalks))
	}
	want := []string{"第一行新文。", "还原的第二行。", "第二句旧文。"}
	for i, w := range want[:2] {
		if dsttalks[i].Text != w {
			t.Errorf("dstTalks[%d] = %q, want %q", i, dsttalks[i].Text, w)
		}
	}
	if dsttalks[2].Text != "第二句新文。" {
		t.Errorf("dstTalks[2] = %q, want the untouched second line", dsttalks[2].Text)
	}
	// Mappings stay consistent afterwards.
	if talks[1].DstIdx != 1 {
		t.Errorf("edited row DstIdx = %d, want 1", talks[1].DstIdx)
	}
	if talks[2].DstIdx != 2 {
		t.Errorf("following row DstIdx = %d, want 2 (shifted by the insert)", talks[2].DstIdx)
	}
	// And a follow-up edit on the last row lands on its own line.
	talks, dsttalks = e.ChangeText(2, "第二句再改。", 2, talks, dsttalks, nil)
	if dsttalks[2].Text != "第二句再改。" {
		t.Errorf("follow-up edit landed on %q", dsttalks[2].Text)
	}
	if dsttalks[1].Text != "还原的第二行。" {
		t.Errorf("follow-up edit clobbered the inserted line: %q", dsttalks[1].Text)
	}
}

// Removing a deletion row (no slot) must not renumber anything: the old
// position-based decrement made every later edit land one line UP.
func TestRemoveLineDeletionRowKeepsMapping(t *testing.T) {
	e := NewEditorService()
	talks, dsttalks := compareFixture()
	talks, dsttalks = e.RemoveLine(1, talks, dsttalks)

	if len(dsttalks) != 2 {
		t.Fatalf("dstTalks length changed: %d", len(dsttalks))
	}
	if talks[1].DstIdx != 1 {
		t.Fatalf("remaining row DstIdx = %d, want 1 (must NOT be decremented)", talks[1].DstIdx)
	}
	talks, dsttalks = e.ChangeText(1, "第二句合意文。", 2, talks, dsttalks, nil)
	if dsttalks[1].Text != "第二句合意文。" {
		t.Errorf("edit after remove landed on %q / %q", dsttalks[0].Text, dsttalks[1].Text)
	}
	if dsttalks[0].Text != "第一行新文。" {
		t.Errorf("edit after remove clobbered line 0: %q", dsttalks[0].Text)
	}
}

// Adding a line anchored on a deletion row: the new sub-line gets no slot
// either (DstIdx -1); dstTalks is untouched until the user types into it.
func TestAddLineAfterDeletionRow(t *testing.T) {
	e := NewEditorService()
	talks, dsttalks := compareFixture()
	talks, dsttalks = e.AddLine(1, talks, dsttalks, true)

	if len(dsttalks) != 2 {
		t.Fatalf("dstTalks must be untouched, got len %d", len(dsttalks))
	}
	if talks[2].DstIdx != -1 {
		t.Errorf("new sub-line DstIdx = %d, want -1", talks[2].DstIdx)
	}
	if talks[3].DstIdx != 1 {
		t.Errorf("following row DstIdx = %d, want 1 (unshifted)", talks[3].DstIdx)
	}
}

// Translate-mode regression: talks and dstTalks are parallel; add + remove must
// keep DstIdx == position exactly as before the renumbering rework.
func TestAddRemoveLineTranslateModeParallel(t *testing.T) {
	e := NewEditorService()
	mk := func() ([]model.DstTalk, []model.DstTalk) {
		var talks []model.DstTalk
		for i, txt := range []string{"一。", "二。", "三。"} {
			tk := cmpTalk(i+1, "咲希", txt)
			tk.DstIdx = i
			talks = append(talks, tk)
		}
		dst := append([]model.DstTalk(nil), talks...)
		return talks, dst
	}

	talks, dst := mk()
	talks, dst = e.AddLine(1, talks, dst, false)
	if len(talks) != 4 || len(dst) != 4 {
		t.Fatalf("lengths after add: talks=%d dst=%d", len(talks), len(dst))
	}
	for i := range talks {
		if talks[i].DstIdx != i {
			t.Errorf("after add, talks[%d].DstIdx = %d, want %d", i, talks[i].DstIdx, i)
		}
	}
	talks, dst = e.RemoveLine(2, talks, dst)
	for i := range talks {
		if talks[i].DstIdx != i {
			t.Errorf("after remove, talks[%d].DstIdx = %d, want %d", i, talks[i].DstIdx, i)
		}
	}
	// Edits still land on the right lines.
	talks, dst = e.ChangeText(2, "三改。", 0, talks, dst, nil)
	if dst[2].Text != "三改。" {
		t.Errorf("translate-mode edit landed on %q", dst[2].Text)
	}
}
