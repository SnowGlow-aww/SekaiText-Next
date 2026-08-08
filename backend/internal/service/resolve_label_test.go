package service

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"sekaitext/backend/internal/model"
)

func representativeStoryCatalog() *ListManager {
	cards := make([]CardEntry, 26)
	levelUpCards := make([]int, 26)
	for i := range cards {
		cards[i] = CardEntry{ID: i + 1, CharacterID: i + 1, CardNo: padZero3(i + 1)}
		levelUpCards[i] = i + 1
	}

	return &ListManager{
		Events: []EventEntry{
			{
				ID: 10, KdyicrID: 501, Title: "Ordinary Activity", Name: "event_501",
				Chapters: []EventChapter{{Title: "Ordinary Chapter With Spaces", AssetName: "event_501_01"}},
			},
			{
				ID: 205, KdyicrID: 700, Title: "World Link Activity", Name: "event_wl",
				Chapters: []EventChapter{{Title: "World Link Chapter", AssetName: "wl_3rd_group3_01"}},
			},
			{
				ID: 211, KdyicrID: 711, Title: "Activity Card Event", Name: "event_711",
				Cards: []int{1, 7},
			},
		},
		Festivals: []FestivalEntry{
			{ID: 1, Collaboration: "Collaboration Fixture", Cards: []int{2}},
			{ID: 2, IsBirthday: true, Cards: []int{3}},
			{ID: 4, Cards: []int{4, 5}},
			{ID: 99, LevelUp: true, Cards: levelUpCards},
		},
		Cards: cards,
		MainStory: []MainStoryEntry{
			{
				Unit: "light_sound", AssetName: "unit_leo",
				Chapters: []EventChapter{{Title: "Main Chapter With Spaces", AssetName: "main_leo_01"}},
			},
			{
				Unit: "idol", AssetName: "unit_mmj",
				Chapters: []EventChapter{{Title: "Second Main Chapter", AssetName: "main_mmj_01"}},
			},
		},
		AreaTalks: []AreaTalkEntry{
			// Production has 805 non-limited rows at the AddEventID == 1
			// boundary. They are initial talks, never upgrade candidates.
			{ID: 100, TalkID: "0001", ScenarioID: "areatalk_init_01", Type: "normal", AddEventID: 1, CharacterIDs: []int{7}},
			{ID: 200, TalkID: "0002", ScenarioID: "areatalk_upgrade_01", Type: "normal", AddEventID: 2, CharacterIDs: []int{8, 7}},
			{ID: 300, TalkID: "S0001", ScenarioID: "areatalk_extra_01", Type: "limited", AddEventID: 45, CharacterIDs: []int{9}},
		},
		Specials: []SpecialEntry{
			{Title: "Special Story SaveTitle With Spaces", DirName: "special_dir", FileName: "special_01"},
		},
	}
}

func completeIdentity(saveTitle, chapterTitle string) string {
	if chapterTitle == "" {
		return saveTitle
	}
	return saveTitle + " " + chapterTitle
}

func canonicalSortForStory(storyType string) string {
	switch storyType {
	case StoryLabelAreaTalkInit, StoryLabelAreaTalkUpgrade, StoryLabelAreaTalkExtra:
		return "character"
	default:
		return ""
	}
}

func productionShapedMainStoryCatalog() *ListManager {
	groups := []struct {
		unit      string
		assetName string
		prefix    string
		chapters  int
	}{
		{unit: "piapro", assetName: "piapro-story-chapter", chapters: 20},
		{unit: "light_sound", assetName: "light-sound-story-chapter", prefix: "leo", chapters: 21},
		{unit: "idol", assetName: "idol-story-chapter", prefix: "mmj", chapters: 21},
		{unit: "street", assetName: "street-story-chapter", prefix: "street", chapters: 21},
		{unit: "theme_park", assetName: "theme-park-story-chapter", prefix: "wonder", chapters: 21},
		{unit: "school_refusal", assetName: "school-refusal-story-chapter", prefix: "nightcode", chapters: 21},
	}
	mainStory := make([]MainStoryEntry, 0, len(groups))
	virtualSingerPrefixes := []string{"vsleo", "vsmmj", "vsstreet", "vswonder", "vsnightcode"}
	for _, group := range groups {
		chapters := make([]EventChapter, 0, group.chapters)
		for chapter := 0; chapter < group.chapters; chapter++ {
			assetName := ""
			if group.unit == "piapro" {
				assetName = fmt.Sprintf("%s_01_%02d", virtualSingerPrefixes[chapter/4], chapter%4+1)
			} else {
				assetName = fmt.Sprintf("%s_01_%02d", group.prefix, chapter)
			}
			chapters = append(chapters, EventChapter{
				Title:     fmt.Sprintf("%s production chapter %02d", group.unit, chapter),
				AssetName: assetName,
			})
		}
		mainStory = append(mainStory, MainStoryEntry{
			Unit: group.unit, AssetName: group.assetName, Chapters: chapters,
		})
	}
	return &ListManager{MainStory: mainStory}
}

func TestResolveLabelRepresentativeCatalogRoundTrips(t *testing.T) {
	lm := representativeStoryCatalog()
	tests := []struct {
		name      string
		storyType string
		index     string
		chapter   int
	}{
		{name: "ordinary activity", storyType: StoryLabelEvent, index: "10", chapter: 0},
		{name: "world link activity", storyType: StoryLabelEvent, index: "205", chapter: 0},
		{name: "main story", storyType: StoryLabelMainStory, index: "0", chapter: 0},
		{name: "activity card front", storyType: StoryLabelCardEvent, index: "211", chapter: 0},
		{name: "activity card back", storyType: StoryLabelCardEvent, index: "211", chapter: 1},
		{name: "activity card special", storyType: StoryLabelCardEvent, index: "211", chapter: 2},
		{name: "collaboration special card", storyType: StoryLabelCardSpecial, index: "3", chapter: 0},
		{name: "birthday special card", storyType: StoryLabelCardSpecial, index: "2", chapter: 1},
		{name: "festival special card", storyType: StoryLabelCardSpecial, index: "1", chapter: 2},
		{name: "initial card", storyType: StoryLabelCardInit, index: "6", chapter: 8},
		{name: "upgrade card", storyType: StoryLabelCardUpgrade, index: "6", chapter: 1},
		{name: "initial area talk", storyType: StoryLabelAreaTalkInit, index: "6", chapter: 0},
		{name: "upgrade area talk", storyType: StoryLabelAreaTalkUpgrade, index: "6", chapter: 0},
		{name: "extra area talk", storyType: StoryLabelAreaTalkExtra, index: "8", chapter: 0},
		{name: "special story title with spaces", storyType: StoryLabelSpecial, index: "0", chapter: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sort := canonicalSortForStory(test.storyType)
			original := lm.GetJsonPath(test.storyType, sort, test.index, test.chapter, "haruki")
			if original.URL == "" || original.SaveTitle == "" {
				t.Fatalf("fixture did not produce a story path: %+v", original)
			}

			identity := completeIdentity(original.SaveTitle, original.ChapterTitle)
			storyType, index, indexLabel, chapter, ok := lm.ResolveLabel(identity)
			if !ok {
				t.Fatalf("ResolveLabel(%q) failed", identity)
			}
			if storyType != test.storyType || index != test.index || chapter != test.chapter {
				t.Fatalf("ResolveLabel(%q) = (%q, %q, %q, %d), want (%q, %q, _, %d)", identity, storyType, index, indexLabel, chapter, test.storyType, test.index, test.chapter)
			}
			if indexLabel == "" {
				t.Fatal("resolved index label must be canonical and non-empty")
			}
			indexFound := false
			for _, option := range lm.GetStoryIndexList(storyType, sort) {
				if option.Value == index {
					indexFound = true
					break
				}
			}
			if !indexFound {
				t.Fatalf("resolved index %q is not a valid %s/%s list option", index, storyType, sort)
			}
			chapterFound := false
			for _, option := range lm.GetStoryChapterList(storyType, sort, index) {
				if option.Number == chapter {
					chapterFound = true
					break
				}
			}
			if !chapterFound {
				t.Fatalf("resolved chapter %d is not a valid %s/%s/%s list option", chapter, storyType, sort, index)
			}

			roundTrip := lm.GetJsonPath(storyType, canonicalSortForStory(storyType), index, chapter, "haruki")
			if !reflect.DeepEqual(roundTrip, original) {
				t.Fatalf("GetJsonPath round trip differs:\n got  %+v\n want %+v", roundTrip, original)
			}
		})
	}
}

func TestCardChapterNavigationSurfacesEveryThreeSlotCardChapter(t *testing.T) {
	lm := representativeStoryCatalog()
	cases := []struct {
		name      string
		storyType string
		index     string
		wantCount int
	}{
		{name: "activity cards", storyType: StoryLabelCardEvent, index: "211", wantCount: 6},
		{name: "special cards", storyType: StoryLabelCardSpecial, index: "3", wantCount: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chapters := lm.GetStoryChapterList(tc.storyType, "", tc.index)
			if len(chapters) != tc.wantCount {
				t.Fatalf("chapter count = %d, want %d: %+v", len(chapters), tc.wantCount, chapters)
			}
			for chapterIdx, chapter := range chapters {
				if chapter.Number != chapterIdx {
					t.Fatalf("chapter %d has coordinate %d", chapterIdx, chapter.Number)
				}
			}

			last := chapters[len(chapters)-1]
			path := lm.GetJsonPath(tc.storyType, "", tc.index, last.Number, "haruki")
			if path.URL == "" || path.ChapterTitle != "特殊篇" {
				t.Fatalf("last special chapter is not loadable: %+v", path)
			}
		})
	}
}

func TestResolveLabelFailsClosedOnAmbiguousOrIncompleteIdentity(t *testing.T) {
	lm := representativeStoryCatalog()

	card := lm.GetJsonPath(StoryLabelCardEvent, "", "211", 0, "haruki")
	if _, _, _, _, ok := lm.ResolveLabel(card.SaveTitle); ok {
		t.Fatal("card SaveTitle without a chapter must remain ambiguous")
	}
	if _, _, _, _, ok := lm.ResolveLabel(card.SaveTitle + " Wrong Chapter"); ok {
		t.Fatal("a non-canonical chapter title must not resolve")
	}

	lm.Specials = append(lm.Specials, SpecialEntry{Title: lm.Specials[0].Title, DirName: "other", FileName: "other_01"})
	if _, _, _, _, ok := lm.ResolveLabel(lm.Specials[0].Title); ok {
		t.Fatal("duplicate special-story SaveTitles must fail closed")
	}

	lm = representativeStoryCatalog()
	lm.AreaTalks = append(lm.AreaTalks, AreaTalkEntry{
		ID: 400, TalkID: "0001", ScenarioID: "different_talk", Type: "normal", AddEventID: 0, CharacterIDs: []int{1},
	})
	if _, _, _, _, ok := lm.ResolveLabel("areatalk-0001"); ok {
		t.Fatal("duplicate area-talk identities must fail closed")
	}

	if _, _, _, _, ok := lm.ResolveLabel("greet-newyear-airi"); ok {
		t.Fatal("greet must remain unsupported")
	}
	if _, _, _, _, ok := lm.ResolveLabel("totally-bogus-xyz"); ok {
		t.Fatal("unknown identity must not resolve")
	}
}

func TestMainStoryIndexValuesRoundTripAndAcceptLegacyLabels(t *testing.T) {
	lm := representativeStoryCatalog()
	indices := lm.GetStoryIndexList(StoryLabelMainStory, "")
	if len(indices) != 2 || indices[0].Value != "0" || indices[1].Value != "1" {
		t.Fatalf("main story index values = %+v, want numeric catalog coordinates", indices)
	}

	for i, option := range indices {
		numeric := lm.GetJsonPath(StoryLabelMainStory, "", option.Value, 0, "haruki")
		legacy := lm.GetJsonPath(StoryLabelMainStory, "", option.Label, 0, "haruki")
		unitKey := lm.GetJsonPath(StoryLabelMainStory, "", lm.MainStory[i].Unit, 0, "haruki")
		if numeric.URL == "" || !reflect.DeepEqual(legacy, numeric) || !reflect.DeepEqual(unitKey, numeric) {
			t.Fatalf("main index compatibility failed for %+v:\n numeric=%+v\n legacy=%+v\n unit=%+v", option, numeric, legacy, unitKey)
		}
		if chapters := lm.GetStoryChapterList(StoryLabelMainStory, "", option.Label); len(chapters) == 0 {
			t.Fatalf("legacy main index label %q produced no chapters", option.Label)
		}
	}
}

func TestMainStoryNavigationCoordinatesMatchEveryProductionShapedChapter(t *testing.T) {
	lm := productionShapedMainStoryCatalog()
	indices := lm.GetStoryIndexList(StoryLabelMainStory, "")
	if len(indices) != len(lm.MainStory) {
		t.Fatalf("main story indices = %d, want %d", len(indices), len(lm.MainStory))
	}

	for unitIdx, unit := range lm.MainStory {
		index := fmt.Sprintf("%d", unitIdx)
		t.Run(unit.Unit, func(t *testing.T) {
			if indices[unitIdx].Value != index {
				t.Fatalf("index value = %q, want exact catalog coordinate %q", indices[unitIdx].Value, index)
			}
			chapters := lm.GetStoryChapterList(StoryLabelMainStory, "", index)
			if len(chapters) != len(unit.Chapters) {
				t.Fatalf("chapter list length = %d, want %d", len(chapters), len(unit.Chapters))
			}
			for chapterIdx, chapter := range unit.Chapters {
				if chapters[chapterIdx].Number != chapterIdx {
					t.Fatalf("chapter option %d has coordinate %d", chapterIdx, chapters[chapterIdx].Number)
				}
				path := lm.GetJsonPath(StoryLabelMainStory, "", index, chapters[chapterIdx].Number, "haruki")
				wantSaveTitle := strings.ReplaceAll(chapter.AssetName, "_", "-")
				if path.SaveTitle != wantSaveTitle || path.ChapterTitle != chapter.Title {
					t.Fatalf("chapter %d loaded %+v, want SaveTitle %q ChapterTitle %q", chapterIdx, path, wantSaveTitle, chapter.Title)
				}
				storyType, resolvedIndex, _, resolvedChapter, ok := lm.ResolveLabel(completeIdentity(path.SaveTitle, path.ChapterTitle))
				if !ok || storyType != StoryLabelMainStory || resolvedIndex != index || resolvedChapter != chapterIdx {
					t.Fatalf("chapter %d identity resolved to (%q, %q, %d, %t)", chapterIdx, storyType, resolvedIndex, resolvedChapter, ok)
				}
			}
		})
	}
}

func TestAreaTalkBoundarySetsAreDisjointAndRoundTripExactly(t *testing.T) {
	lm := representativeStoryCatalog()
	tests := []struct {
		name       string
		storyType  string
		talkType   string
		wantIndex  string
		wantTalkID string
	}{
		{name: "initial AddEventID one", storyType: StoryLabelAreaTalkInit, talkType: "init", wantIndex: "6", wantTalkID: "0001"},
		{name: "upgrade after initial boundary", storyType: StoryLabelAreaTalkUpgrade, talkType: "upgrade", wantIndex: "6", wantTalkID: "0002"},
		{name: "extra limited", storyType: StoryLabelAreaTalkExtra, talkType: "extra", wantIndex: "8", wantTalkID: "S0001"},
	}
	seen := make(map[string]string)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries := lm.buildAreaTalkChapterScenario(test.talkType, "character")
			if len(entries) != 1 || entries[0].TalkID != test.wantTalkID {
				t.Fatalf("%s candidates = %+v, want only %s", test.storyType, entries, test.wantTalkID)
			}
			chapters := lm.GetStoryChapterList(test.storyType, "character", test.wantIndex)
			if len(chapters) != 1 {
				t.Fatalf("%s chapters = %+v, want one disjoint candidate", test.storyType, chapters)
			}
			path := lm.GetJsonPath(test.storyType, "character", test.wantIndex, chapters[0].Number, "haruki")
			if path.SaveTitle != "areatalk-"+test.wantTalkID {
				t.Fatalf("%s path = %+v", test.storyType, path)
			}
			if previous, exists := seen[path.SaveTitle]; exists {
				t.Fatalf("identity %q appeared in both %s and %s", path.SaveTitle, previous, test.storyType)
			}
			seen[path.SaveTitle] = test.storyType
			storyType, index, _, chapter, ok := lm.ResolveLabel(path.SaveTitle)
			if !ok || storyType != test.storyType {
				t.Fatalf("ResolveLabel(%q) = (%q, %q, %d, %t), want %q", path.SaveTitle, storyType, index, chapter, ok, test.storyType)
			}
			roundTrip := lm.GetJsonPath(storyType, "character", index, chapter, "haruki")
			if !reflect.DeepEqual(roundTrip, path) {
				t.Fatalf("area-talk round trip differs:\n got  %+v\n want %+v", roundTrip, path)
			}
		})
	}
}

func TestResolveLabelFailsClosedOnProductionShapedFestivalCollision(t *testing.T) {
	lm := &ListManager{
		Cards: []CardEntry{
			{ID: 1, CharacterID: 25, CardNo: "030"},
			{ID: 2, CharacterID: 25, CardNo: "033"},
		},
		Festivals: []FestivalEntry{{ID: 11, Cards: []int{1, 2}}},
	}
	first := lm.GetJsonPath(StoryLabelCardSpecial, "", "0", 0, "haruki")
	second := lm.GetJsonPath(StoryLabelCardSpecial, "", "0", 3, "haruki")
	identity := completeIdentity(first.SaveTitle, first.ChapterTitle)
	if identity != "fes202310-meiko 前篇" || completeIdentity(second.SaveTitle, second.ChapterTitle) != identity {
		t.Fatalf("fixture did not reproduce the production collision: first=%+v second=%+v", first, second)
	}
	if first.URL == second.URL {
		t.Fatal("collision fixture must point to different card scenarios")
	}
	if _, _, _, _, ok := lm.ResolveLabel(identity); ok {
		t.Fatalf("cold ambiguous Festival identity %q must fail closed", identity)
	}
}

func TestResolveLabelFailsClosedOnVirtualSingerUpgradeCardCollisions(t *testing.T) {
	cards := make([]CardEntry, len(model.CharacterDict))
	levelUpCards := make([]int, len(cards))
	for idx := range cards {
		cards[idx] = CardEntry{ID: idx + 1, CharacterID: min(idx+1, 26), CardNo: padZero3(idx + 1)}
		levelUpCards[idx] = idx + 1
	}
	lm := &ListManager{
		Cards:     cards,
		Festivals: []FestivalEntry{{ID: 99, LevelUp: true, Cards: levelUpCards}},
	}

	for characterIdx := 20; characterIdx < 26; characterIdx++ {
		index := strconv.Itoa(characterIdx)
		base := lm.GetJsonPath(StoryLabelCardUpgrade, "", index, 0, "haruki")
		legacyVS := lm.GetJsonPath(StoryLabelCardUpgrade, "", index, 3, "haruki")
		identity := completeIdentity(base.SaveTitle, base.ChapterTitle)
		if base.URL == "" || legacyVS.URL == "" || base.URL == legacyVS.URL {
			t.Fatalf("character %d did not expose distinct loadable upgrade targets: base=%+v legacy=%+v", characterIdx, base, legacyVS)
		}
		if completeIdentity(legacyVS.SaveTitle, legacyVS.ChapterTitle) != identity {
			t.Fatalf("character %d identities differ: base=%q legacy=%q", characterIdx, identity, completeIdentity(legacyVS.SaveTitle, legacyVS.ChapterTitle))
		}
		if storyType, resolvedIndex, _, chapter, ok, matchKind, reason := lm.ResolveLabelDetailed(identity); ok || reason != "exact-ambiguous" {
			t.Fatalf("ambiguous upgrade identity resolved to (%q, %q, %d, %t, %q, %q)", storyType, resolvedIndex, chapter, ok, matchKind, reason)
		}
	}
}

func TestResolveLabelAcceptsBoundedLegacySpecialCardTitle(t *testing.T) {
	lm := representativeStoryCatalog()
	path := lm.GetJsonPath(StoryLabelCardInit, "", "6", 8, "haruki")
	storyType, index, _, chapter, ok := lm.ResolveLabel(path.SaveTitle + " 其他")
	if !ok || storyType != StoryLabelCardInit || index != "6" || chapter != 8 {
		t.Fatalf("legacy 其他 identity resolved to (%q, %q, %d, %t)", storyType, index, chapter, ok)
	}
}

func TestResolveLabelDetailedClassifiesLegacyTitlesWithoutGuessingCards(t *testing.T) {
	lm := representativeStoryCatalog()

	storyType, index, _, chapter, ok, matchKind, reason := lm.ResolveLabelDetailed("Special Story SaveTitle With Spaces 自定义标题")
	if !ok || storyType != StoryLabelSpecial || index != "0" || chapter != 0 || matchKind != "legacy" || reason != "" {
		t.Fatalf("unique legacy special identity = (%q, %q, %d, %t, %q, %q)", storyType, index, chapter, ok, matchKind, reason)
	}

	card := lm.GetJsonPath(StoryLabelCardEvent, "", "211", 0, "haruki")
	_, _, _, _, ok, matchKind, reason = lm.ResolveLabelDetailed(card.SaveTitle + " 爱莉前篇译文")
	if ok || matchKind != "" || reason != "legacy-ambiguous" {
		t.Fatalf("legacy card identity = (%t, %q, %q), want fail-closed legacy ambiguity", ok, matchKind, reason)
	}

	_, _, _, _, ok, matchKind, reason = lm.ResolveLabelDetailed(card.SaveTitle + " 后篇")
	if !ok || matchKind != "exact" || reason != "" {
		t.Fatalf("canonical card identity = (%t, %q, %q), want exact", ok, matchKind, reason)
	}
}
