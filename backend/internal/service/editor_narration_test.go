package service

import (
	"testing"

	"sekaitext/backend/internal/model"
)

func TestChangeTextAllowsNarrationButKeepsSeparatorReadOnly(t *testing.T) {
	e := &EditorService{}
	narration := []model.DstTalk{{Idx: 1, Speaker: "", Text: "旧旁白", Start: true, End: true, Save: true, DstIdx: 0}}
	dst := append([]model.DstTalk(nil), narration...)

	talks, saved := e.ChangeText(0, "新旁白", 0, narration, dst, nil)
	if talks[0].Text != "新旁白" || saved[0].Text != "新旁白" {
		t.Fatalf("narration was not editable: talks=%q dst=%q", talks[0].Text, saved[0].Text)
	}

	separator := []model.DstTalk{{Idx: 2, Speaker: "", Text: "", Start: true, End: true, Save: true, DstIdx: 0}}
	separatorDst := append([]model.DstTalk(nil), separator...)
	talks, saved = e.ChangeText(0, "不应写入", 0, separator, separatorDst, nil)
	if talks[0].Text != "" || saved[0].Text != "" {
		t.Fatalf("separator became editable: talks=%q dst=%q", talks[0].Text, saved[0].Text)
	}
}
