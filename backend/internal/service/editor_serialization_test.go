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
