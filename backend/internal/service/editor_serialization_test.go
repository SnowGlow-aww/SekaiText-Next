package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"sekaitext/backend/internal/model"
)

func TestSerializeWithMetaKeepsTeamPlainTextContract(t *testing.T) {
	editor := NewEditorService()
	meta := &model.SaveMetadata{
		StoryType:  "event",
		Index:      "211",
		Chapter:    0,
		Source:     "haruki",
		ScenarioID: "event_211_01",
		Mode:       1,
	}
	talks := []model.DstTalk{{
		Idx: 1, Speaker: "爱莉", Text: "译文", Start: true, End: true, Save: true,
	}}

	content := editor.SerializeWithMeta(talks, true, meta)
	if strings.HasPrefix(content, "#SekaiText ") {
		t.Fatalf("team translation output gained a metadata header: %q", content)
	}
	if content != "爱莉：译文" {
		t.Fatalf("plain-text serialization = %q", content)
	}

	loaded, loadedMeta, err := editor.LoadContent(content)
	if err != nil {
		t.Fatal(err)
	}
	if loadedMeta != nil {
		t.Fatalf("plain-text file unexpectedly produced metadata: %+v", loadedMeta)
	}
	if len(loaded) != 1 || loaded[0].Speaker != "爱莉" || loaded[0].Text != "译文" {
		t.Fatalf("plain-text round trip = %+v", loaded)
	}
}

func TestLoadContentAcceptsLegacyMetadataHeader(t *testing.T) {
	editor := NewEditorService()
	wantMeta := model.SaveMetadata{
		StoryType:  "card-event",
		Sort:       "211",
		Index:      "airi",
		Chapter:    0,
		Source:     "haruki",
		ScenarioID: "event_211_airi_01",
		Mode:       2,
	}
	header, err := json.Marshal(wantMeta)
	if err != nil {
		t.Fatal(err)
	}

	loaded, gotMeta, err := editor.LoadContent("#SekaiText " + string(header) + "\n爱莉：旧译文")
	if err != nil {
		t.Fatal(err)
	}
	if gotMeta == nil || !reflect.DeepEqual(*gotMeta, wantMeta) {
		t.Fatalf("legacy metadata = %+v, want %+v", gotMeta, wantMeta)
	}
	if len(loaded) != 1 || loaded[0].Speaker != "爱莉" || loaded[0].Text != "旧译文" {
		t.Fatalf("legacy body parse = %+v", loaded)
	}
}

func TestCreateFileJPModePreservesOriginalPunctuation(t *testing.T) {
	source := []model.SourceTalk{{
		Speaker: "爱莉",
		Text:    "第一行？！……「测试」♪☆/『引用』",
	}}

	editor := NewEditorService()
	talks := editor.CreateFile(source, true)
	if len(talks) != 1 || talks[0].Text != source[0].Text {
		t.Fatalf("JP template changed original punctuation: %+v", talks)
	}
	content := editor.SerializeContent(talks, true)
	if content != "爱莉：第一行？！……「测试」♪☆/『引用』" {
		t.Fatalf("serialized JP template = %q", content)
	}
}

func TestCreateFileJPModePreservesDialogueLineBreaks(t *testing.T) {
	source := []model.SourceTalk{
		{Speaker: "场景", Text: "ストリートのセカイ"},
		{Speaker: "爱莉", Text: "第一行？！\n第二行……\n第三行「完」"},
		{Speaker: "", Text: ""},
		{Speaker: "场景", Text: "后续场景"},
	}

	editor := NewEditorService()
	talks := editor.CreateFile(source, true)
	if len(talks) != 6 {
		t.Fatalf("got %d dst talks, want 6: %+v", len(talks), talks)
	}
	if got := talks[1]; !got.Start || got.End || got.Text != "第一行？！" {
		t.Fatalf("first dialogue segment = %+v", got)
	}
	if got := talks[2]; got.Start || got.End || got.Text != "第二行……" {
		t.Fatalf("middle dialogue segment = %+v", got)
	}
	if got := talks[3]; got.Start || !got.End || got.Text != "第三行「完」" {
		t.Fatalf("last dialogue segment = %+v", got)
	}

	content := editor.SerializeContent(talks, true)
	if !strings.Contains(content, "爱莉：第一行？！\\N第二行……\\N第三行「完」") {
		t.Fatalf("serialized dialogue lost \\N separators: %q", content)
	}
	if strings.Contains(strings.ReplaceAll(content, "\r\n", ""), "\n") {
		t.Fatalf("serialized content contains bare LF instead of CRLF: %q", content)
	}

	loaded, _, err := editor.LoadContent(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(talks) {
		t.Fatalf("round-trip talk count = %d, want %d: %+v", len(loaded), len(talks), loaded)
	}
	for i, want := range talks {
		if loaded[i].Speaker != want.Speaker || loaded[i].Start != want.Start || loaded[i].End != want.End {
			t.Fatalf("round-trip row %d structure = %+v, want speaker/start/end from %+v", i, loaded[i], want)
		}
	}
}

func TestManagedTranslationFileNameMatchesEditorContract(t *testing.T) {
	cases := []struct {
		name         string
		saveTitle    string
		chapterTitle string
		want         string
	}{
		{
			name:         "chaptered",
			saveTitle:    "event211-airi",
			chapterTitle: "前篇",
			want:         "【翻译】event211-airi 前篇.txt",
		},
		{
			name:         "chapterless",
			saveTitle:    "Special Story SaveTitle With Spaces",
			chapterTitle: "",
			want:         "【翻译】Special Story SaveTitle With Spaces.txt",
		},
		{
			name:         "safe fallback",
			saveTitle:    "a/b",
			chapterTitle: "",
			want:         "【翻译】a_b.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ManagedTranslationFileName(tc.saveTitle, tc.chapterTitle); got != tc.want {
				t.Fatalf("ManagedTranslationFileName(%q, %q) = %q, want %q", tc.saveTitle, tc.chapterTitle, got, tc.want)
			}
		})
	}
}
