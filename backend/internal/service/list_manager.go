package service

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"sekaitext/backend/internal/model"
)

// ListManager manages story metadata (events, cards, main story, etc.).
type ListManager struct {
	updateMu sync.Mutex // single-flights UpdateAll so two CDN refreshes can't race-append the slices below

	// mu guards the metadata slices below. Read methods
	// (GetStory*/GetJsonPath/ResolveLabel/BuildVoiceIDClues) hold RLock; every
	// writer publishes under a short Lock: loadAll and InferVoiceEventID, plus the
	// incremental rebuild in update.go (updateEvents/updateCards/... build into a
	// local, then swap the field under Lock). The file I/O and the heavy build
	// always run outside the lock, so an update never blocks readers for more than
	// a pointer swap — no rebuild-long critical section. Readers additionally
	// snapshot each slice into a local before any length-check-then-index so a
	// single method always sees one consistent slice header.
	mu sync.RWMutex

	Events     []EventEntry
	Festivals  []FestivalEntry
	Cards      []CardEntry
	MainStory  []MainStoryEntry
	AreaTalks  []AreaTalkEntry
	Greets     []GreetEntry
	Specials   []SpecialEntry
	Catalog    map[string]interface{}

	// voiceClues maps every inferred voice prefix -> event array index. Unlike
	// the per-event InferredVoiceIDs.prefix (single value), this allows one
	// event to be reachable by multiple voice prefixes (e.g. a WL event known
	// both as wl_shuffle_03 via area talks and wl_3rd_group3 via its assetName).
	voiceClues map[string]int

	catalogDir string
	DBurl      string

	// AreaTalkByTime is only reset by update.go and is no longer used for
	// navigation: the "按时间" ordering is derived per request (see
	// buildAreaTalkByTime, which now returns a local) so concurrent requests can't
	// clobber a shared scratch slice.
	AreaTalkByTime []AreaTalkTimeEntry

	// CDN URLs
	baseUrls map[string]string
}

// EventEntry mirrors events.json structure.
type EventEntry struct {
	ID               int                    `json:"id"`
	KdyicrID         int                    `json:"kdyicr_id"`
	Title            string                 `json:"title"`
	Name             string                 `json:"name"`
	Chapters         []EventChapter         `json:"chapters"`
	Cards            []int                  `json:"cards"`
	InferredVoiceIDs map[string]interface{} `json:"inferredVoiceIDs,omitempty"`
}

// EventChapter represents a chapter in an event.
type EventChapter struct {
	Title     string `json:"title"`
	AssetName string `json:"assetName"`
}

// FestivalEntry mirrors festivals.json structure.
type FestivalEntry struct {
	ID            int    `json:"id"`
	IsBirthday    bool   `json:"isBirthday"`
	Cards         []int  `json:"cards"`
	Collaboration string `json:"collaboration,omitempty"`
	LevelUp       bool   `json:"levelup,omitempty"`
}

// CardEntry mirrors cards.json structure.
type CardEntry struct {
	ID          int  `json:"id"`
	CharacterID int  `json:"characterId"`
	CardNo      string `json:"cardNo"`
	Birthday    bool `json:"birthday"`
	LevelUp     bool `json:"levelup,omitempty"`
}

// MainStoryEntry mirrors mainStory.json structure.
type MainStoryEntry struct {
	Unit      string              `json:"unit"`
	AssetName string              `json:"assetName"`
	Chapters  []EventChapter      `json:"chapters"`
}

// AreaTalkEntry mirrors areatalks.json structure.
type AreaTalkEntry struct {
	ID             int    `json:"id"`
	TalkID         string `json:"talkid"`
	AreaID         int    `json:"areaId"`
	CharacterIDs   []int  `json:"characterIds"`
	ScenarioID     string `json:"scenarioId"`
	Type           string `json:"type"`
	AddEventID     int    `json:"addEventId"`
	ReleaseEventID int    `json:"releaseEventId"`
}

// GreetEntry mirrors greets.json structure.
type GreetEntry struct {
	Theme  GreetTheme    `json:"theme"`
	Year   int           `json:"year"`
	Greets []GreetItem   `json:"greets"`
}

// GreetTheme represents a greet theme.
type GreetTheme struct {
	Ch string `json:"ch"`
	En string `json:"en"`
}

// GreetItem represents a single greet entry.
type GreetItem struct {
	CharacterID int    `json:"characterId"`
	Text        string `json:"text"`
}

// SpecialEntry mirrors specials.json structure.
type SpecialEntry struct {
	Title    string `json:"title"`
	DirName  string `json:"dirName"`
	FileName string `json:"fileName"`
}

// AreaTalkTimeEntry is used for "按时间" sorting.
type AreaTalkTimeEntry struct {
	AddEventID     int  `json:"addEventId"`
	ReleaseEventID int  `json:"releaseEventId"`
	Limited        bool `json:"limited"`
	Monthly        bool `json:"monthly"`
}

// ChapterScenarioEntry stores resolved scenario info for a chapter.
type ChapterScenarioEntry struct {
	ID         int    `json:"id"`
	ScenarioID string `json:"scenarioId"`
	TalkID     string `json:"talkid"`
	IsSeparator bool  `json:"isSeparator,omitempty"`
}

// NewListManager creates and loads metadata from the setting directory.
func NewListManager(catalogDir string) *ListManager {
	lm := &ListManager{
		catalogDir: catalogDir,
		Catalog:    make(map[string]interface{}),
		baseUrls: map[string]string{
			"best":         "https://storage.sekai.best/sekai-jp-assets/",
			"uni":          "https://assets.unipjsk.com/",
			"haruki":       "https://production-sekai-assets.neo.bot.haruki.seiunx.com/jp-assets/",
			"moesekai-jp":  "https://storage.exmeaning.com/sekai-jp-assets/",
			"moesekai-cn":  "https://storage.exmeaning.com/sekai-cn-assets/",
		},
	}
	lm.loadCatalog()
	lm.loadAll()
	return lm
}

func (lm *ListManager) loadCatalog() {
	path := filepath.Join(lm.catalogDir, "setting.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &lm.Catalog)
	}
}

func (lm *ListManager) loadAll() {
	// Read every file into a local first, then publish all slices under the write
	// lock in one short critical section, so a concurrent reader (RLock) can never
	// observe a half-swapped metadata set (and file I/O never runs under the lock).
	events := loadJSONFile[[]EventEntry](lm.catalogDir, "events.json")
	festivals := loadJSONFile[[]FestivalEntry](lm.catalogDir, "festivals.json")
	cards := loadJSONFile[[]CardEntry](lm.catalogDir, "cards.json")
	mainStory := loadJSONFile[[]MainStoryEntry](lm.catalogDir, "mainStory.json")
	areaTalks := loadJSONFile[[]AreaTalkEntry](lm.catalogDir, "areatalks.json")
	greets := loadJSONFile[[]GreetEntry](lm.catalogDir, "greets.json")
	specials := loadJSONFile[[]SpecialEntry](lm.catalogDir, "specials.json")

	lm.mu.Lock()
	lm.Events = events
	lm.Festivals = festivals
	lm.Cards = cards
	lm.MainStory = mainStory
	lm.AreaTalks = areaTalks
	lm.Greets = greets
	lm.Specials = specials
	lm.mu.Unlock()
	log.Println("All metadata loaded")
}

func loadJSONFile[T any](dir, fileName string) T {
	var zero T
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Warning: could not load %s: %v", fileName, err)
		return zero
	}
	if err := json.Unmarshal(data, &zero); err != nil {
		log.Printf("Warning: could not parse %s: %v", fileName, err)
		return zero
	}
	return zero
}

// --- Story Type constants (Chinese labels) ---
const (
	StoryLabelEvent       = "活动剧情"
	StoryLabelMainStory   = "主线剧情"
	StoryLabelCardEvent   = "活动卡面"
	StoryLabelCardSpecial = "特殊卡面"
	StoryLabelCardInit    = "初始卡面"
	StoryLabelCardUpgrade = "升级卡面"
	StoryLabelAreaTalkInit = "初始地图对话"
	StoryLabelAreaTalkUpgrade = "升级地图对话"
	StoryLabelAreaTalkExtra = "追加地图对话"
	StoryLabelGreet       = "主界面语音"
	StoryLabelSpecial     = "特殊剧情"
)

// GetStoryTypes returns available story type names (Chinese labels).
func (lm *ListManager) GetStoryTypes() []string {
	return []string{
		StoryLabelEvent,
		StoryLabelMainStory,
		StoryLabelCardEvent,
		StoryLabelCardSpecial,
		StoryLabelCardInit,
		StoryLabelCardUpgrade,
		StoryLabelAreaTalkInit,
		StoryLabelAreaTalkUpgrade,
		StoryLabelAreaTalkExtra,
		// StoryLabelGreet (主界面语音) is intentionally omitted: GetJsonPath has no
		// case for it, so it would always 404 "story not found". Don't surface an
		// unloadable type until the greet voice URL is implemented.
		StoryLabelSpecial,
	}
}

// GetStorySorts returns sort options for a given story type.
func (lm *ListManager) GetStorySorts(storyType string) []model.StorySort {
	switch storyType {
	case StoryLabelAreaTalkInit, StoryLabelAreaTalkUpgrade:
		return []model.StorySort{
			{Label: "按人物", Value: "character"},
			{Label: "按地点", Value: "area"},
		}
	case StoryLabelGreet:
		return []model.StorySort{
			{Label: "按人物", Value: "character"},
			{Label: "按时间", Value: "time"},
		}
	case StoryLabelAreaTalkExtra:
		return []model.StorySort{
			{Label: "按人物", Value: "character"},
			{Label: "按时间", Value: "time"},
			{Label: "按地点", Value: "area"},
		}
	default:
		return nil
	}
}

// GetStoryIndexList returns index options for a story type and sort.
func (lm *ListManager) GetStoryIndexList(storyType, sort string) []model.StoryIndex {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	var indices []model.StoryIndex

	// Snapshot shared slices once; an unlocked concurrent rebuild in update.go must
	// not shift len/backing-array between the loop bound and the indexing below.
	mainStory := lm.MainStory
	events := lm.Events
	festivals := lm.Festivals
	greets := lm.Greets
	specials := lm.Specials

	switch storyType {
	case StoryLabelMainStory:
		for _, unit := range mainStory {
			name := model.UnitDict[unit.Unit]
			indices = append(indices, model.StoryIndex{
				Label: name,
				Value: name,
			})
		}

	case StoryLabelEvent, StoryLabelCardEvent:
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			label := strconv.Itoa(ev.ID) + " " + ev.Title
			indices = append(indices, model.StoryIndex{
				Label: label,
				Value: strconv.Itoa(ev.ID),
			})
		}

	case StoryLabelCardSpecial:
		for i := len(festivals) - 1; i >= 0; i-- {
			f := festivals[i]
			var label string
			if f.Collaboration != "" {
				label = f.Collaboration
			} else if f.IsBirthday {
				idx := f.ID
				year := 2021 + (idx+2)/4
				month := (idx+2)%4*3 + 1
				label = "Birthday " + strconv.Itoa(year) + " " + padZero(month) + "-" + padZero(month+2)
			} else {
				idx := f.ID
				year := 2021 + idx/4
				month := idx%4*3 + 1
				label = "Festival " + strconv.Itoa(year) + " " + padZero(month)
			}
			indices = append(indices, model.StoryIndex{Label: label, Value: strconv.Itoa(len(festivals) - 1 - i)})
		}

	case StoryLabelCardInit, StoryLabelCardUpgrade:
		for idx, char := range model.CharacterDict[:26] {
			indices = append(indices, model.StoryIndex{Label: char.NameJ, Value: strconv.Itoa(idx)})
			if idx%4 == 3 && idx < 20 {
				indices = append(indices, model.StoryIndex{Label: "-", Value: "-"})
			}
		}

	case StoryLabelAreaTalkInit, StoryLabelAreaTalkUpgrade, StoryLabelAreaTalkExtra:
		if sort == "character" {
			for idx, char := range model.CharacterDict[:26] {
				indices = append(indices, model.StoryIndex{Label: char.NameJ, Value: strconv.Itoa(idx)})
				if idx%4 == 3 && idx < 20 {
					indices = append(indices, model.StoryIndex{Label: "-", Value: "-"})
				}
			}
		} else if sort == "time" {
			byTime := lm.buildAreaTalkByTime()
			for i := len(byTime) - 1; i >= 0; i-- {
				indices = append(indices, model.StoryIndex{Label: "time", Value: strconv.Itoa(i)})
			}
		} else if sort == "area" {
			for _, area := range model.AreaDict {
				if area != "" {
					indices = append(indices, model.StoryIndex{Label: area, Value: area})
				}
			}
		}

	case StoryLabelGreet:
		if sort == "character" {
			for idx, char := range model.CharacterDict {
				indices = append(indices, model.StoryIndex{Label: char.NameJ, Value: strconv.Itoa(idx)})
				if (idx%4 == 3 && idx < 20) || idx == 25 {
					indices = append(indices, model.StoryIndex{Label: "-", Value: "-"})
				}
			}
		} else if sort == "time" {
			for i := len(greets) - 1; i >= 0; i-- {
				g := greets[i]
				label := g.Theme.Ch + " " + strconv.Itoa(g.Year)
				indices = append(indices, model.StoryIndex{Label: label, Value: strconv.Itoa(i)})
			}
		}

	case StoryLabelSpecial:
		for i := len(specials) - 1; i >= 0; i-- {
			indices = append(indices, model.StoryIndex{
				Label: specials[i].Title,
				Value: strconv.Itoa(i),
			})
		}
	}

	return indices
}

// GetStoryChapterList returns chapters for a given story.
func (lm *ListManager) GetStoryChapterList(storyType, sort, index string) []model.StoryChapter {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	idx := parseIndex(index)
	var chapters []model.StoryChapter

	// Snapshot shared slices once (see GetStoryIndexList).
	mainStory := lm.MainStory
	cards := lm.Cards
	festivals := lm.Festivals

	switch storyType {
	case StoryLabelMainStory:
		unitIdx := idx
		if unitIdx >= 0 && unitIdx < len(mainStory) {
			for ci, chapter := range mainStory[unitIdx].Chapters {
				var epNo int
				if unitIdx == 0 {
					epNo = ci%4 + 1
				} else {
					epNo = ci
				}
				chapters = append(chapters, model.StoryChapter{
					Number: ci,
					Label:  strconv.Itoa(epNo) + " " + chapter.Title,
				})
			}
		}

	case StoryLabelEvent:
		event := lm.findEventByID(idx)
		if event != nil {
			for ci, chapter := range event.Chapters {
				chapters = append(chapters, model.StoryChapter{
					Number: ci,
					Label:  strconv.Itoa(ci+1) + " " + chapter.Title,
				})
			}
		}

	case StoryLabelCardEvent:
		event := lm.findEventByID(idx)
		if event != nil {
			for _, cardID := range event.Cards {
				if cardID >= 1 && cardID <= len(cards) {
					// Keep the 3-slot layout even for a hole-fill card
					// (CharacterID = -1) so the chapter index stays aligned with
					// GetJsonPath's validCards; just avoid CharacterDict[-2].
					charName := cardCharNameJ(cards[cardID-1].CharacterID)
					n := len(chapters)
					chapters = append(chapters,
						model.StoryChapter{Number: n, Label: charName + " 前篇"},
						model.StoryChapter{Number: n + 1, Label: charName + " 后篇"},
						model.StoryChapter{Number: n + 2, Label: "-"},
					)
				}
			}
			if len(chapters) > 0 {
				chapters = chapters[:len(chapters)-1]
			}
		}

	case StoryLabelCardSpecial:
		content := festivals
		contentIdx := len(content) - idx
		if contentIdx >= 1 && contentIdx <= len(content) {
			for _, cardID := range content[contentIdx-1].Cards {
				if cardID >= 1 && cardID <= len(cards) {
					// festival scans include hole-fill cards (CharacterID = -1);
					// keep the slot but never index CharacterDict out of range.
					charName := cardCharNameJ(cards[cardID-1].CharacterID)
					n := len(chapters)
					chapters = append(chapters,
						model.StoryChapter{Number: n, Label: charName + " 前篇"},
						model.StoryChapter{Number: n + 1, Label: charName + " 后篇"},
						model.StoryChapter{Number: n + 2, Label: "-"},
					)
				}
			}
			if len(chapters) > 0 {
				chapters = chapters[:len(chapters)-1]
			}
		}

	case StoryLabelCardInit:
		if idx >= 0 && idx < 26 {
			chapters = append(chapters,
				model.StoryChapter{Number: 0, Label: "1☆ 前篇"},
				model.StoryChapter{Number: 1, Label: "1☆ 后篇"},
				model.StoryChapter{Number: 2, Label: "1☆ 其他"},
				model.StoryChapter{Number: 3, Label: "2☆ 前篇"},
				model.StoryChapter{Number: 4, Label: "2☆ 后篇"},
				model.StoryChapter{Number: 5, Label: "2☆ 其他"},
				model.StoryChapter{Number: 6, Label: "3☆ 前篇"},
				model.StoryChapter{Number: 7, Label: "3☆ 后篇"},
				model.StoryChapter{Number: 8, Label: "3☆ 其他"},
				model.StoryChapter{Number: 9, Label: "4☆ 前篇"},
				model.StoryChapter{Number: 10, Label: "4☆ 后篇"},
				model.StoryChapter{Number: 11, Label: "4☆ 其他"},
			)
		}

	case StoryLabelCardUpgrade:
		if idx >= 0 && idx < 26 {
			chapters = append(chapters,
				model.StoryChapter{Number: 0, Label: "前篇"},
				model.StoryChapter{Number: 1, Label: "后篇"},
				model.StoryChapter{Number: 2, Label: "其他"},
			)
		}

	case StoryLabelAreaTalkInit:
		cs := lm.buildAreaTalkChapterScenario("init", sort)
		for ci := range cs {
			if cs[ci].IsSeparator {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: "-"})
			} else {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: cs[ci].ScenarioID})
			}
		}

	case StoryLabelAreaTalkUpgrade:
		cs := lm.buildAreaTalkChapterScenario("upgrade", sort)
		for ci := range cs {
			if cs[ci].IsSeparator {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: "-"})
			} else {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: cs[ci].ScenarioID})
			}
		}

	case StoryLabelAreaTalkExtra:
		cs := lm.buildAreaTalkChapterScenario("extra", sort)
		for ci := range cs {
			if cs[ci].IsSeparator {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: "-"})
			} else {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: cs[ci].ScenarioID})
			}
		}

	case StoryLabelGreet:
		chapters = append(chapters, model.StoryChapter{Number: 0, Label: "默认"})

	case StoryLabelSpecial:
		chapters = append(chapters, model.StoryChapter{Number: 0, Label: "默认"})
	}

	return chapters
}

// GetJsonPath returns the CDN URL and filename for a story's JSON.
func (lm *ListManager) GetJsonPath(storyType, sort, index string, chapterIdx int, source string) model.JsonPathResult {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	extension := "asset"
	format := "uni"
	baseURL := lm.baseUrls["uni"]

	if source == "sekai.best" {
		format = "best"
		baseURL = lm.baseUrls["best"]
	} else if source == "haruki" {
		baseURL = lm.baseUrls["haruki"]
		extension = "json"
	} else if source == "unipjsk" {
		baseURL = lm.baseUrls["uni"]
		extension = "json"
	} else if source == "moesekai-jp" {
		format = "best"
		baseURL = lm.baseUrls["moesekai-jp"]
		extension = "json"
	} else if source == "moesekai-cn" {
		format = "best"
		baseURL = lm.baseUrls["moesekai-cn"]
		extension = "json"
	}

	idx := parseIndex(index)

	// Snapshot shared slices once (see GetStoryIndexList).
	mainStory := lm.MainStory
	cards := lm.Cards
	festivals := lm.Festivals
	specials := lm.Specials

	makeCardURL := func(charID int, cardNo, chapter string) (string, string) {
		char := model.CharacterDict[charID-1]
		cid := padZero3(charID)
		var url string
		if format == "best" {
			url = baseURL + "character/member/res" + cid + "_no" + cardNo + "/" + cid + cardNo + "_" + char.Name + chapter + "." + extension
		} else {
			url = baseURL + "startapp/character/member/res" + cid + "_no" + cardNo + "/" + cid + cardNo + "_" + char.Name + chapter + "." + extension
		}
		return url, char.Name
	}

	switch storyType {
	case StoryLabelMainStory:
		unitIdx := idx
		if unitIdx < 0 || unitIdx >= len(mainStory) {
			return model.JsonPathResult{}
		}
		unit := mainStory[unitIdx]
		ch := unit.Chapters
		if unitIdx == 0 {
			chapterIdx = (chapterIdx+1)*4/5
		}
		if chapterIdx < 0 || chapterIdx >= len(ch) {
			return model.JsonPathResult{}
		}
		chapter := ch[chapterIdx].AssetName
		var url string
		if format == "best" {
			url = baseURL + "scenario/unitstory/" + unit.AssetName + "/" + chapter + "." + extension
		} else {
			url = baseURL + "startapp/scenario/unitstory/" + unit.AssetName + "/" + chapter + "." + extension
		}
		return model.JsonPathResult{
			URL:          url,
			FileName:     "mainStory_" + chapter + ".json",
			SaveTitle:    strings.ReplaceAll(chapter, "_", "-"),
			ChapterTitle: ch[chapterIdx].Title,
		}

	case StoryLabelEvent:
		ev := lm.findEventByID(idx)
		if ev == nil || chapterIdx < 0 || chapterIdx >= len(ev.Chapters) {
			return model.JsonPathResult{}
		}
		chapter := ev.Chapters[chapterIdx].AssetName
		var url string
		if format == "best" {
			url = baseURL + "event_story/" + ev.Name + "/scenario/" + chapter + "." + extension
		} else {
			url = baseURL + "ondemand/event_story/" + ev.Name + "/scenario/" + chapter + "." + extension
		}
		return model.JsonPathResult{
			URL:          url,
			FileName:     chapter + ".json",
			SaveTitle:    strings.Join(lm.processChapterID(lm.eventReverseIndex(ev), strings.Split(chapter, "_")[1:]), "-"),
			ChapterTitle: ev.Chapters[chapterIdx].Title,
		}

	case StoryLabelCardEvent:
		ev := lm.findEventByID(idx)
		if ev == nil {
			return model.JsonPathResult{}
		}
		// Enumerate only valid cards, matching GetStoryChapterList's filter,
		// so the chapter slot the user picked maps to the same card here.
		var validCards []int
		for _, c := range ev.Cards {
			if c >= 1 && c <= len(cards) {
				validCards = append(validCards, c)
			}
		}
		cardSlot := chapterIdx / 3
		if cardSlot < 0 || cardSlot >= len(validCards) {
			return model.JsonPathResult{}
		}
		cardID := validCards[cardSlot]
		cardCharID := cards[cardID-1].CharacterID
		cardNo := cards[cardID-1].CardNo
		ch := padZero(chapterIdx%3 + 1)
		if cardCharID < 1 || cardCharID > len(model.CharacterDict) {
			return model.JsonPathResult{}
		}
		char := model.CharacterDict[cardCharID-1]
		charID := padZero3(cardCharID)
		var url string
		if format == "best" {
			url = baseURL + "character/member/res" + charID + "_no" + cardNo + "/" + charID + cardNo + "_" + char.Name + ch + "." + extension
		} else {
			url = baseURL + "startapp/character/member/res" + charID + "_no" + cardNo + "/" + charID + cardNo + "_" + char.Name + ch + "." + extension
		}
		return model.JsonPathResult{
			URL:          url,
			FileName:     "event" + padZero3(ev.ID) + "_" + char.Name + "_" + ch + ".json",
			SaveTitle:    "event" + padZero3(ev.ID) + "-" + char.Name,
			ChapterTitle: cardChapterTitle(chapterIdx),
		}

	case StoryLabelCardSpecial:
		fesIdx := len(festivals) - idx
		if fesIdx < 1 || fesIdx > len(festivals) {
			return model.JsonPathResult{}
		}
		f := festivals[fesIdx-1]
		cardSlot := chapterIdx / 3
		if cardSlot < 0 || cardSlot >= len(f.Cards) {
			return model.JsonPathResult{}
		}
		cardID := f.Cards[cardSlot]
		if cardID < 1 || cardID > len(cards) {
			return model.JsonPathResult{}
		}
		cardCharID := cards[cardID-1].CharacterID
		cardNo := cards[cardID-1].CardNo
		ch := padZero(chapterIdx%3 + 1)
		// Guard hole-fill cards (CharacterID = -1) before makeCardURL indexes
		// CharacterDict[cardCharID-1]; mirrors the StoryLabelCardEvent guard.
		if cardCharID < 1 || cardCharID > len(model.CharacterDict) {
			return model.JsonPathResult{}
		}
		url, charName := makeCardURL(cardCharID, cardNo, ch)
		return model.JsonPathResult{
			URL:          url,
			FileName:     "festival_" + padZero3(f.ID) + "_" + charName + "_" + ch + ".json",
			SaveTitle:    fesSaveTitle(f, charName),
			ChapterTitle: cardChapterTitle(chapterIdx),
		}

	case StoryLabelCardInit:
		// The index dropdown emits contiguous Values 0..25 (separators carry
		// Value "-" and consume no number), so idx maps directly to
		// CharacterDict[idx]; the 1-based card character id is idx+1.
		charId := idx + 1
		if charId < 1 || charId > len(model.CharacterDict) {
			return model.JsonPathResult{}
		}
		charName := model.CharacterDict[charId-1].Name
		rarity := chapterIdx/3 + 1
		rarityStr := padZero3(rarity)
		ch := padZero(chapterIdx%3 + 1)
		var url string
		if format == "best" {
			url = baseURL + "character/member/res" + padZero3(charId) + "_no" + rarityStr + "/" + padZero3(charId) + rarityStr + "_" + charName + ch + "." + extension
		} else {
			url = baseURL + "startapp/character/member/res" + padZero3(charId) + "_no" + rarityStr + "/" + padZero3(charId) + rarityStr + "_" + charName + ch + "." + extension
		}
		return model.JsonPathResult{
			URL:          url,
			FileName:     "release_" + charName + "_" + padZero(rarity) + "_" + ch + ".json",
			SaveTitle:    "release-" + charName + "-" + padZero(rarity),
			ChapterTitle: cardChapterTitle(chapterIdx),
		}

	case StoryLabelCardUpgrade:
		// The index dropdown emits contiguous Values 0..25, so idx maps directly
		// to CharacterDict[idx]; the 1-based card character id is idx+1.
		charId := idx + 1
		if charId < 1 || charId > len(model.CharacterDict) {
			return model.JsonPathResult{}
		}
		var levelupcards []int
		for _, f := range festivals {
			if f.LevelUp {
				levelupcards = f.Cards
				break
			}
		}
		if levelupcards == nil {
			return model.JsonPathResult{}
		}
		if charId < 1 || charId > len(levelupcards) {
			return model.JsonPathResult{}
		}
		cardID := levelupcards[charId-1]
		// Virtual singer special VS chapters
		if charId >= 21 && chapterIdx > 2 {
			vsIdx := 30 - 6 + chapterIdx/3
			var lvIdx int
			if charId == 22 { // Rin
				lvIdx = len(model.CharacterDict) - 4
			} else if charId == 23 { // Len
				lvIdx = len(model.CharacterDict) - 3
			} else if charId == 24 { // MEIKO
				lvIdx = len(model.CharacterDict) - 5
			} else if charId == 25 { // KAITO
				lvIdx = len(model.CharacterDict) - 2
			} else if charId == 26 { // Miku_band
				lvIdx = len(model.CharacterDict) - 1
			} else { // Miku (21)
				lvIdx = vsIdx
			}
			// Guard against a short/partial levelup festival card list.
			if lvIdx < 0 || lvIdx >= len(levelupcards) {
				return model.JsonPathResult{}
			}
			cardID = levelupcards[lvIdx]
		}
		if cardID < 1 || cardID > len(cards) {
			return model.JsonPathResult{}
		}
		cardNo := cards[cardID-1].CardNo
		ch := padZero(chapterIdx%3 + 1)
		url, charName := makeCardURL(charId, cardNo, ch)
		return model.JsonPathResult{
			URL:          url,
			FileName:     "levelup_" + charName + "_" + ch + ".json",
			SaveTitle:    "lvelup2023-" + charName,
			ChapterTitle: cardChapterTitle(chapterIdx),
		}

	case StoryLabelAreaTalkInit, StoryLabelAreaTalkUpgrade, StoryLabelAreaTalkExtra:
		// Re-derive the chapter scenario list from (storyType, sort) instead of a
		// shared lm field, so a concurrent /story/chapter for another type can't
		// clear or race the slice this /json-path relies on. The enumeration is
		// deterministic, so chapterIdx maps to the same entry GetStoryChapterList
		// produced.
		var talkType string
		switch storyType {
		case StoryLabelAreaTalkInit:
			talkType = "init"
		case StoryLabelAreaTalkUpgrade:
			talkType = "upgrade"
		case StoryLabelAreaTalkExtra:
			talkType = "extra"
		}
		cs := lm.buildAreaTalkChapterScenario(talkType, sort)
		if chapterIdx < 0 || chapterIdx >= len(cs) {
			return model.JsonPathResult{}
		}
		entry := cs[chapterIdx]
		group := entry.ID / 100
		var url string
		if format == "best" {
			url = baseURL + "scenario/actionset/group" + strconv.Itoa(group) + "/" + entry.ScenarioID + "." + extension
		} else {
			url = baseURL + "startapp/scenario/actionset/group" + strconv.Itoa(group) + "/" + entry.ScenarioID + "." + extension
		}
		fileName := "areatalk_" + entry.TalkID + "_" + entry.ScenarioID + ".json"
		return model.JsonPathResult{
			URL:          url,
			FileName:     fileName,
			SaveTitle:    "areatalk-" + entry.TalkID,
			ChapterTitle: "",
		}

	case StoryLabelSpecial:
		// The index dropdown emits Value = the raw array index, so idx already
		// is the wanted position; do NOT reverse it again.
		specialIdx := idx
		if specialIdx < 0 || specialIdx >= len(specials) {
			return model.JsonPathResult{}
		}
		story := specials[specialIdx]
		var url string
		if format == "best" {
			url = baseURL + "scenario/special/" + story.DirName + "/" + story.FileName + "." + extension
		} else {
			url = baseURL + "startapp/scenario/special/" + story.DirName + "/" + story.FileName + "." + extension
		}
		return model.JsonPathResult{
			URL:          url,
			FileName:     story.FileName + ".json",
			SaveTitle:    story.Title,
			ChapterTitle: "",
		}
	}

	return model.JsonPathResult{}
}

// --- Helpers for ListManager ---

// cardCharNameJ returns the Japanese character name for a card's CharacterID, or
// "?" for hole-fill/invalid ids (CharacterID = -1), so callers can keep a slot in
// the chapter list without indexing CharacterDict out of range.
func cardCharNameJ(characterID int) string {
	if characterID >= 1 && characterID <= len(model.CharacterDict) {
		return model.CharacterDict[characterID-1].NameJ
	}
	return "?"
}

// buildAreaTalkByTime returns the "按时间" ordering as a request-local slice. It is
// not stored on lm: the old shared lm.AreaTalkByTime raced between concurrent
// requests and got reset between the /story/index and later calls.
func (lm *ListManager) buildAreaTalkByTime() []AreaTalkTimeEntry {
	talks := lm.AreaTalks
	var out []AreaTalkTimeEntry
	// Simplified: just stores add/release event IDs from area talks
	for _, at := range talks {
		if at.ScenarioID == "none" || at.AddEventID < 0 {
			continue
		}
		isLimited := at.Type == "limited"
		isMonthly := strings.Contains(at.ScenarioID, "monthly")
		out = append(out, AreaTalkTimeEntry{
			AddEventID:     at.AddEventID,
			ReleaseEventID: at.ReleaseEventID,
			Limited:        isLimited,
			Monthly:        isMonthly,
		})
	}
	return out
}

// buildAreaTalkChapterScenario returns the ChapterScenario list for an area talk
// type as a request-local slice (not stored on lm), so /story/chapter and
// /json-path derive it independently and concurrent requests can't clobber shared
// scratch state. Deterministic given (talkType, area talks), so both endpoints
// agree on the chapterIdx -> entry mapping.
func (lm *ListManager) buildAreaTalkChapterScenario(talkType, sort string) []ChapterScenarioEntry {
	talks := lm.AreaTalks
	var out []ChapterScenarioEntry

	for _, at := range talks {
		if at.ScenarioID == "none" || at.ScenarioID == "" {
			out = append(out, ChapterScenarioEntry{IsSeparator: true})
			continue
		}

		// Filter by type
		isInit := talkType == "init" && at.AddEventID <= 1 && at.Type != "limited"
		isUpgrade := talkType == "upgrade" && at.AddEventID > 0 && at.Type != "limited"
		isExtra := talkType == "extra" && at.Type == "limited"

		if !isInit && !isUpgrade && !isExtra {
			continue
		}

		out = append(out, ChapterScenarioEntry{
			ID:         at.ID,
			ScenarioID: at.ScenarioID,
			TalkID:     at.TalkID,
		})
	}
	return out
}

func parseIndex(index string) int {
	i, err := strconv.Atoi(index)
	if err != nil {
		return 0
	}
	return i
}

func padZero(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func padZero3(n int) string {
	return strconv.Itoa(1000 + n)[1:]
}
// eventReverseIndex returns the 1-based position of an event in the list (oldest=1, newest=N).
func (lm *ListManager) eventReverseIndex(ev *EventEntry) int {
	for i := range lm.Events {
		if lm.Events[i].ID == ev.ID {
			return i + 1
		}
	}
	return 0
}

// ResolveLabel reverse-maps a filename label (the SaveTitle segment, e.g.
// "3rd-group3-01" or "198-06") back to the story coordinates GetJsonPath needs.
// Returns storyType, index (event ID as string), chapterIdx (0-based) and ok.
// Only event stories are resolved; other label shapes return ok=false so the
// caller falls back to manual selection.
func (lm *ListManager) ResolveLabel(label string) (storyType, index string, chapterIdx int, ok bool) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	label = strings.TrimSpace(label)
	if label == "" {
		return "", "", 0, false
	}

	events := lm.Events

	// Strategy 1: WL events keep an assetName-derived label (assetName
	// wl_3rd_group3_01 -> label 3rd-group3-01), so reconstruct "wl_<underscored>"
	// and match the chapter assetName directly. NOTE: we must NOT try an
	// "event_<underscored>" candidate here — ordinary-event assetNames are named
	// by the internal kdyicr id (event_204_01 belongs to the event whose
	// kdyicr=204, i.e. list position 202), while the numeric label encodes the
	// 1-based list position. Matching event_<label> would cross-wire the two
	// numbering schemes (e.g. label "204-01" wrongly loading event 202). Ordinary
	// events are handled by Strategy 2 instead.
	underscored := strings.ReplaceAll(label, "-", "_")
	for _, cand := range []string{"wl_" + underscored, underscored} {
		for ei := range events {
			for ci, ch := range events[ei].Chapters {
				if ch.AssetName == cand {
					return StoryLabelEvent, strconv.Itoa(events[ei].ID), ci, true
				}
			}
		}
	}

	// Strategy 2: "<eventReverseIndex>-<episode>" (ordinary events). Both parts
	// numeric: the Nth event in list order, Mth chapter (1-based).
	parts := strings.Split(label, "-")
	if len(parts) == 2 {
		rev, err1 := strconv.Atoi(parts[0])
		ep, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil && rev >= 1 && rev <= len(events) && ep >= 1 {
			ev := &events[rev-1]
			if ep <= len(ev.Chapters) {
				return StoryLabelEvent, strconv.Itoa(ev.ID), ep - 1, true
			}
		}
	}

	return "", "", 0, false
}

// processChapterID replaces the internal kdyicr_id in chapter asset name parts
// with the display event index, matching the Python reference logic.
func (lm *ListManager) processChapterID(eventIndex int, chapterIDs []string) []string {
	if len(chapterIDs) != 2 {
		return chapterIDs
	}
	kd, err1 := strconv.Atoi(chapterIDs[0])
	ep, err2 := strconv.Atoi(chapterIDs[1])
	if err1 != nil || err2 != nil || kd <= 0 || ep <= 0 {
		return chapterIDs
	}
	events := lm.Events
	if eventIndex < 1 || eventIndex > len(events) {
		return chapterIDs
	}
	ev := events[eventIndex-1]
	if kd != ev.KdyicrID {
		return chapterIDs
	}
	return []string{strconv.Itoa(eventIndex), chapterIDs[1]}
}

func cardChapterTitle(chapterIdx int) string {
	switch chapterIdx % 3 {
	case 0:
		return "前篇"
	case 1:
		return "后篇"
	default:
		return "特殊篇"
	}
}

// 卡面类 SaveTitle 不带 -01/-02 章节号：文件名后面拼的 ChapterTitle
// （前篇/后篇/特殊篇）已足够区分且更可读（用户反馈 01/02 是多余的）。
func fesSaveTitle(f FestivalEntry, charName string) string {
	if f.Collaboration != "" {
		return "collabo" + padZero3(f.ID) + "-" + charName
	}
	if f.IsBirthday {
		year := 2021 + (f.ID+2)/4
		return "birth" + strconv.Itoa(year) + "-" + charName
	}
	year := 2021 + f.ID/4
	month := f.ID%4*3 + 1
	return "fes" + strconv.Itoa(year) + padZero(month) + "-" + charName
}


func (lm *ListManager) findEventByID(id int) *EventEntry {
	for i := range lm.Events {
		if lm.Events[i].ID == id {
			return &lm.Events[i]
		}
	}
	return nil
}

// --- Voice Clue Inference ---

// BuildVoiceIDClues builds a map of voiceID prefix -> event info. It uses the
// full multi-prefix map collected by InferVoiceEventID (so a single event can
// be matched by several voice prefixes), falling back to the per-event
// InferredVoiceIDs.prefix for any event not covered there.
func (lm *ListManager) BuildVoiceIDClues() map[string]EventEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	events := lm.Events
	clues := make(map[string]EventEntry)
	for prefix, ei := range lm.voiceClues {
		if ei >= 0 && ei < len(events) {
			clues[prefix] = events[ei]
		}
	}
	for _, ev := range events {
		if iv, ok := ev.InferredVoiceIDs["prefix"]; ok {
			if prefix, ok := iv.(string); ok {
				if _, exists := clues[prefix]; !exists {
					clues[prefix] = ev
				}
			}
		}
	}
	return clues
}

// InferVoiceEventID infers voice event IDs from area talks and stores them in events.
func (lm *ListManager) InferVoiceEventID() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	eventsByID := make(map[int]int)
	for ei, ev := range lm.Events {
		eventsByID[ev.ID] = ei
	}

	clues := make(map[string]int) // clue prefix -> event array index
	areatalkRe := regexp.MustCompile(`areatalk_(ev|wl)_(.+)_\d+$`)

	for _, at := range lm.AreaTalks {
		match := areatalkRe.FindStringSubmatch(at.ScenarioID)
		if match == nil || at.AddEventID <= 0 {
			continue
		}
		eventClue := match[2]
		if match[1] == "wl" {
			eventClue = "wl_" + eventClue
		}
		if ei, exists := eventsByID[at.AddEventID]; exists {
			if prevEi, exists := clues[eventClue]; !exists || prevEi > ei {
				clues[eventClue] = ei
			}
		}
	}

	// Hard-coded patterns
	if ei, ok := eventsByID[1]; ok {
		clues["band_01"] = ei
	}
	if ei, ok := eventsByID[53]; ok {
		clues["night__"] = ei
	}
	if ei, ok := eventsByID[9]; ok {
		clues["shuffle_03"] = ei
	}

	// Fallback from chapter assetName. Area talks don't cover World Link events
	// (wl_3rd_group1/2/3, wl_<unit>_NN, etc.), so their voice prefixes never get
	// a clue and they render "未知活动". The first chapter's assetName with its
	// trailing episode number stripped (e.g. "wl_3rd_group3_01" -> "wl_3rd_group3")
	// equals the voice clue, so use it as a clue source. Only fill gaps — never
	// overwrite an area-talk-derived clue (those carry the correct choffset).
	assetEpRe := regexp.MustCompile(`_\d+$`)
	for ei, ev := range lm.Events {
		if len(ev.Chapters) == 0 {
			continue
		}
		prefix := assetEpRe.ReplaceAllString(ev.Chapters[0].AssetName, "")
		if prefix == "" {
			continue
		}
		if _, exists := clues[prefix]; !exists {
			clues[prefix] = ei
		}
	}

	for clue, ei := range clues {
		chOffset := 0
		if lm.Events[ei].ID == 9 {
			chOffset = 1
		}
		lm.Events[ei].InferredVoiceIDs = map[string]interface{}{
			"prefix":   clue,
			"choffset": chOffset,
		}
	}

	// Keep the full multi-prefix map so BuildVoiceIDClues can expose EVERY clue,
	// not just the single one that survived in each event's InferredVoiceIDs.
	lm.voiceClues = clues
}
