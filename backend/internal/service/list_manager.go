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

// Character2DPublishGuard wraps publication of the package-global character2d
// lookup. A guard that permits publication must invoke publish synchronously
// while its authorization remains valid. A nil guard preserves the desktop
// behavior and publishes immediately.
type Character2DPublishGuard func(publish func())

// ListManager manages story metadata (events, cards, main story, etc.).
type ListManager struct {
	updateMu sync.Mutex // single-flights UpdateAll so two CDN refreshes can't race-append the slices below

	// mu guards the metadata slices below. Read methods
	// (GetStory*/GetJsonPath/ResolveLabel/BuildVoiceIDClues) hold RLock; refreshes
	// build and validate a complete generation before swapping every field under
	// one short Lock. File I/O and heavy builds stay outside the lock, so an update
	// never blocks readers for more than a pointer swap. Readers additionally
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
	generation uint64

	// voiceClues maps every inferred voice prefix -> event array index. Unlike
	// the per-event InferredVoiceIDs.prefix (single value), this allows one
	// event to be reachable by multiple voice prefixes (e.g. a WL event known
	// both as wl_shuffle_03 via area talks and wl_3rd_group3 via its assetName).
	voiceClues              map[string]int
	character2DPublishGuard Character2DPublishGuard
	flashbackMu             sync.Mutex
	flashbacks              map[*FlashbackAnalyzer]struct{}

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
	ID          int    `json:"id"`
	CharacterID int    `json:"characterId"`
	CardNo      string `json:"cardNo"`
	Birthday    bool   `json:"birthday"`
	LevelUp     bool   `json:"levelup,omitempty"`
}

// MainStoryEntry mirrors mainStory.json structure.
type MainStoryEntry struct {
	Unit      string         `json:"unit"`
	AssetName string         `json:"assetName"`
	Chapters  []EventChapter `json:"chapters"`
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
	Theme  GreetTheme  `json:"theme"`
	Year   int         `json:"year"`
	Greets []GreetItem `json:"greets"`
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
	ID          int    `json:"id"`
	ScenarioID  string `json:"scenarioId"`
	TalkID      string `json:"talkid"`
	IsSeparator bool   `json:"isSeparator,omitempty"`
}

// NewListManager creates and loads metadata from the setting directory.
func NewListManager(catalogDir string) *ListManager {
	lm := &ListManager{
		catalogDir: catalogDir,
		Catalog:    make(map[string]interface{}),
		baseUrls: map[string]string{
			"best":        "https://storage.sekai.best/sekai-jp-assets/",
			"uni":         "https://assets.unipjsk.com/",
			"haruki":      "https://sekai-assets-bdf29c81.seiunx.net/jp-assets/",
			"moesekai-jp": "https://storage.exmeaning.com/sekai-jp-assets/",
			"moesekai-cn": "https://storage.exmeaning.com/sekai-cn-assets/",
		},
	}
	lm.loadCatalog()
	lm.loadAll()
	return lm
}

// SetCharacter2DPublishGuard installs a scope around package-global character2d
// publication. Mobile configures this before exposing a new ListManager so a
// superseded runtime cannot publish after a cross-root reinitialization.
func (lm *ListManager) SetCharacter2DPublishGuard(guard Character2DPublishGuard) {
	lm.mu.Lock()
	lm.character2DPublishGuard = guard
	lm.mu.Unlock()
}

func (lm *ListManager) registerFlashbackAnalyzer(fb *FlashbackAnalyzer) {
	lm.flashbackMu.Lock()
	if lm.flashbacks == nil {
		lm.flashbacks = make(map[*FlashbackAnalyzer]struct{})
	}
	lm.flashbacks[fb] = struct{}{}
	lm.flashbackMu.Unlock()
}

func (lm *ListManager) refreshFlashbackAnalyzers() {
	lm.flashbackMu.Lock()
	analyzers := make([]*FlashbackAnalyzer, 0, len(lm.flashbacks))
	for fb := range lm.flashbacks {
		analyzers = append(analyzers, fb)
	}
	lm.flashbackMu.Unlock()
	for _, fb := range analyzers {
		fb.refreshIndexes()
	}
}

func (lm *ListManager) loadCatalog() {
	path := filepath.Join(lm.catalogDir, "setting.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &lm.Catalog)
	}
}

func (lm *ListManager) loadAll() {
	if catalog, generation, err := loadCatalogGeneration(lm.catalogDir); err == nil {
		lm.mu.Lock()
		lm.Events = catalog.Events
		lm.Festivals = catalog.Festivals
		lm.Cards = catalog.Cards
		lm.MainStory = catalog.MainStory
		lm.AreaTalks = catalog.AreaTalks
		lm.Greets = catalog.Greets
		lm.Specials = catalog.Specials
		lm.generation = generation
		lm.mu.Unlock()
		log.Printf("All metadata loaded (generation %d)", generation)
		return
	}

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
	lm.generation = 0
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
	StoryLabelEvent           = "活动剧情"
	StoryLabelMainStory       = "主线剧情"
	StoryLabelCardEvent       = "活动卡面"
	StoryLabelCardSpecial     = "特殊卡面"
	StoryLabelCardInit        = "初始卡面"
	StoryLabelCardUpgrade     = "升级卡面"
	StoryLabelAreaTalkInit    = "初始地图对话"
	StoryLabelAreaTalkUpgrade = "升级地图对话"
	StoryLabelAreaTalkExtra   = "追加地图对话"
	StoryLabelGreet           = "主界面语音"
	StoryLabelSpecial         = "特殊剧情"
)

// CatalogState reports whether a complete, generation-backed catalog is
// available and the currently published generation. Legacy top-level JSON files
// remain readable for desktop compatibility, but they are not proof that Android
// has an atomically published catalog generation.
func (lm *ListManager) CatalogState() (ready bool, generation uint64) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	ready = lm.generation > 0 && len(lm.Events) > 0 && len(lm.Festivals) > 0 &&
		len(lm.Cards) > 0 && len(lm.MainStory) > 0 && len(lm.AreaTalks) > 0 &&
		len(lm.Specials) > 0
	return ready, lm.generation
}

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
		for unitIdx, unit := range mainStory {
			name := model.UnitDict[unit.Unit]
			indices = append(indices, model.StoryIndex{
				Label: name,
				// GetJsonPath addresses main-story entries by their catalog
				// position. Keep the display label human-readable, but make the
				// value round-trip through the same numeric coordinate.
				Value: strconv.Itoa(unitIdx),
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
			indices = append(indices, model.StoryIndex{
				Label: festivalIndexLabel(f),
				Value: strconv.Itoa(len(festivals) - 1 - i),
			})
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
			eventMap := make(map[int]string, len(events))
			for _, ev := range events {
				eventMap[ev.ID] = ev.Title
			}
			seenEventIDs := make(map[int]struct{})
			var eventIDs []int
			for _, at := range lm.AreaTalks {
				if at.ScenarioID == "none" || at.ScenarioID == "" || at.AddEventID <= 0 {
					continue
				}
				if storyType == StoryLabelAreaTalkExtra && at.Type != "limited" {
					continue
				}
				if storyType != StoryLabelAreaTalkExtra && at.Type == "limited" {
					continue
				}
				if _, seen := seenEventIDs[at.AddEventID]; !seen {
					seenEventIDs[at.AddEventID] = struct{}{}
					eventIDs = append(eventIDs, at.AddEventID)
				}
			}
			for i := 0; i < len(eventIDs); i++ {
				for j := i + 1; j < len(eventIDs); j++ {
					if eventIDs[i] < eventIDs[j] {
						eventIDs[i], eventIDs[j] = eventIDs[j], eventIDs[i]
					}
				}
			}
			for _, eid := range eventIDs {
				title := eventMap[eid]
				label := strconv.Itoa(eid)
				if title != "" {
					label = label + " " + title
				}
				indices = append(indices, model.StoryIndex{
					Label: label,
					Value: strconv.Itoa(eid),
				})
			}
		} else if sort == "area" {
			for areaID, area := range model.AreaDict {
				if area != "" {
					indices = append(indices, model.StoryIndex{Label: area, Value: strconv.Itoa(areaID)})
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
		unitIdx, valid := mainStoryIndex(mainStory, index)
		if valid {
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

	case StoryLabelAreaTalkInit, StoryLabelAreaTalkUpgrade, StoryLabelAreaTalkExtra:
		cs := lm.getAreaTalkEntries(storyType, sort, index)
		for ci := range cs {
			if cs[ci].IsSeparator {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: "-"})
			} else {
				chapters = append(chapters, model.StoryChapter{Number: ci, Label: cs[ci].TalkID + " " + cs[ci].ScenarioID})
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
		unitIdx, valid := mainStoryIndex(mainStory, index)
		if !valid {
			return model.JsonPathResult{}
		}
		unit := mainStory[unitIdx]
		ch := unit.Chapters
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
			SaveTitle:    lm.eventChapterSaveTitle(ev, ev.Chapters[chapterIdx]),
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
		if charId < 1 || charId > 26 || charId > len(model.CharacterDict) {
			return model.JsonPathResult{}
		}
		var levelupcards []int
		for _, f := range festivals {
			if f.LevelUp {
				levelupcards = f.Cards
				break
			}
		}
		cardSlot, valid := upgradeCardSlotIndex(idx, chapterIdx, len(levelupcards))
		if !valid {
			return model.JsonPathResult{}
		}
		cardID := levelupcards[cardSlot]
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
		cs := lm.getAreaTalkEntries(storyType, sort, index)
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

// getAreaTalkEntries returns the filtered ChapterScenarioEntry slice for an area talk
// type, sort and index as a request-local slice (not stored on lm).
func (lm *ListManager) getAreaTalkEntries(storyType, sort, index string) []ChapterScenarioEntry {
	talks := lm.AreaTalks
	var talkType string
	switch storyType {
	case StoryLabelAreaTalkInit:
		talkType = "init"
	case StoryLabelAreaTalkUpgrade:
		talkType = "upgrade"
	case StoryLabelAreaTalkExtra:
		talkType = "extra"
	default:
		return nil
	}

	charIDFilter := 0
	if sort == "character" && index != "" && index != "-" {
		charIdx := parseIndex(index)
		if charIdx >= 0 && charIdx < len(model.CharacterDict) {
			charIDFilter = charIdx + 1
		}
	}

	eventIDFilter := 0
	if sort == "time" && index != "" && index != "-" {
		eventIDFilter = parseIndex(index)
	}

	areaIDFilter := 0
	if sort == "area" && index != "" && index != "-" {
		if id, err := strconv.Atoi(index); err == nil && id > 0 && id < len(model.AreaDict) {
			areaIDFilter = id
		} else {
			for aid, aname := range model.AreaDict {
				if aname != "" && aname == index {
					areaIDFilter = aid
					break
				}
			}
		}
	}

	var out []ChapterScenarioEntry
	for _, at := range talks {
		if at.ScenarioID == "none" || at.ScenarioID == "" {
			continue
		}

		isInit := talkType == "init" && at.AddEventID <= 1 && at.Type != "limited"
		isUpgrade := talkType == "upgrade" && at.AddEventID > 1 && at.Type != "limited"
		isExtra := talkType == "extra" && at.Type == "limited"

		if !isInit && !isUpgrade && !isExtra {
			continue
		}

		if charIDFilter > 0 {
			hasChar := false
			for _, cid := range at.CharacterIDs {
				if cid == charIDFilter {
					hasChar = true
					break
				}
			}
			if !hasChar {
				continue
			}
		}

		if eventIDFilter > 0 && at.AddEventID != eventIDFilter {
			continue
		}

		if areaIDFilter > 0 && at.AreaID != areaIDFilter {
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

func (lm *ListManager) buildAreaTalkChapterScenario(talkType, sort string) []ChapterScenarioEntry {
	storyType := StoryLabelAreaTalkInit
	switch talkType {
	case "init":
		storyType = StoryLabelAreaTalkInit
	case "upgrade":
		storyType = StoryLabelAreaTalkUpgrade
	case "extra":
		storyType = StoryLabelAreaTalkExtra
	}
	return lm.getAreaTalkEntries(storyType, sort, "")
}

func parseIndex(index string) int {
	i, err := strconv.Atoi(index)
	if err != nil {
		return 0
	}
	return i
}

// mainStoryIndex accepts the current numeric catalog coordinate and the old
// display-name value emitted by pre-5.9.8 clients. New index options use the
// numeric value; a unique legacy name remains loadable, while duplicate legacy
// labels fail closed instead of selecting the first catalog entry.
func mainStoryIndex(mainStory []MainStoryEntry, index string) (int, bool) {
	if i, err := strconv.Atoi(strings.TrimSpace(index)); err == nil {
		return i, i >= 0 && i < len(mainStory)
	}
	matched := -1
	for i, unit := range mainStory {
		if index != unit.Unit && index != model.UnitDict[unit.Unit] {
			continue
		}
		if matched >= 0 {
			return 0, false
		}
		matched = i
	}
	return matched, matched >= 0
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

type storyLabelResolution struct {
	storyType  string
	index      string
	indexLabel string
	chapter    int
}

func canonicalIdentityMatches(identity, saveTitle, chapterTitle string) bool {
	identity = strings.TrimSpace(identity)
	saveTitle = strings.TrimSpace(saveTitle)
	chapterTitle = strings.TrimSpace(chapterTitle)
	if identity == "" || saveTitle == "" {
		return false
	}
	if chapterTitle == "" {
		return identity == saveTitle
	}
	if identity == saveTitle+" "+chapterTitle {
		return true
	}
	// Older chapter lists called the third card slot "其他", while GetJsonPath
	// has always emitted the canonical ChapterTitle "特殊篇". Preserve that one
	// bounded alias without accepting a chaptered story by SaveTitle alone.
	return chapterTitle == "特殊篇" && identity == saveTitle+" 其他"
}

func legacyIdentityMatches(identity, saveTitle string) bool {
	identity = strings.TrimSpace(identity)
	saveTitle = strings.TrimSpace(saveTitle)
	if identity == "" || saveTitle == "" {
		return false
	}
	suffix, ok := strings.CutPrefix(identity, saveTitle+" ")
	return ok && strings.TrimSpace(suffix) != ""
}

func appendStoryLabelResolution(resolutions *[]storyLabelResolution, candidate storyLabelResolution) {
	for _, current := range *resolutions {
		if current == candidate {
			return
		}
	}
	*resolutions = append(*resolutions, candidate)
}

func appendStoryLabelMatch(
	exactResolutions, legacyResolutions *[]storyLabelResolution,
	identity, saveTitle, chapterTitle string,
	candidate storyLabelResolution,
) {
	if canonicalIdentityMatches(identity, saveTitle, chapterTitle) {
		appendStoryLabelResolution(exactResolutions, candidate)
		return
	}
	if legacyIdentityMatches(identity, saveTitle) {
		appendStoryLabelResolution(legacyResolutions, candidate)
	}
}

func (lm *ListManager) eventChapterSaveTitle(ev *EventEntry, chapter EventChapter) string {
	parts := strings.Split(chapter.AssetName, "_")
	return strings.Join(lm.processChapterID(lm.eventReverseIndex(ev), parts[1:]), "-")
}

func festivalIndexLabel(f FestivalEntry) string {
	if f.Collaboration != "" {
		return f.Collaboration
	}
	if f.IsBirthday {
		year := 2021 + (f.ID+2)/4
		month := (f.ID+2)%4*3 + 1
		return "Birthday " + strconv.Itoa(year) + " " + padZero(month) + "-" + padZero(month+2)
	}
	year := 2021 + f.ID/4
	month := f.ID%4*3 + 1
	return "Festival " + strconv.Itoa(year) + " " + padZero(month)
}

func canonicalAreaTalkCharacter(entry AreaTalkEntry) (index, indexLabel string, ok bool) {
	characterID := 0
	for _, candidate := range entry.CharacterIDs {
		if candidate >= 1 && candidate <= 26 && (characterID == 0 || candidate < characterID) {
			characterID = candidate
		}
	}
	if characterID == 0 {
		return "", "", false
	}
	return strconv.Itoa(characterID - 1), model.CharacterDict[characterID-1].NameJ, true
}

// ResolveLabel reverse-maps the complete canonical filename identity back to
// GetJsonPath coordinates. Every candidate is generated from the published
// catalog using the same SaveTitle/ChapterTitle rules as GetJsonPath; nothing is
// inferred from a first whitespace-delimited token. A unique exact match is
// required. Card identities without a chapter, duplicate special titles, and
// duplicate area-talk TalkIDs therefore fail closed instead of loading another
// story. Cold area talks use the deterministic "character" sort (the frontend
// supplies it for the three area-talk labels) and the lowest valid participant.
func (lm *ListManager) ResolveLabelDetailed(label string) (storyType, index, indexLabel string, chapterIdx int, ok bool, matchKind, reason string) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	identity := strings.TrimSpace(label)
	if identity == "" {
		return "", "", "", 0, false, "", "not-found"
	}

	events := lm.Events
	cards := lm.Cards
	festivals := lm.Festivals
	mainStory := lm.MainStory
	areaTalks := lm.AreaTalks
	specials := lm.Specials
	exactResolutions := make([]storyLabelResolution, 0, 2)
	legacyResolutions := make([]storyLabelResolution, 0, 2)

	// Activity stories, including World Link, are matched against the exact
	// SaveTitle transformation used by GetJsonPath. This avoids crossing ordinary
	// event reverse indices with kdyicr IDs while still accepting WL asset labels.
	for eventIdx := range events {
		ev := &events[eventIdx]
		for chapter := range ev.Chapters {
			entry := ev.Chapters[chapter]
			idx := strconv.Itoa(ev.ID)
			appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, lm.eventChapterSaveTitle(ev, entry), entry.Title, storyLabelResolution{
				storyType:  StoryLabelEvent,
				index:      idx,
				indexLabel: idx + " " + ev.Title,
				chapter:    chapter,
			})
		}
	}

	// Main-story SaveTitles come from chapter asset names. Navigation and source
	// loading both use the exact catalog chapter coordinate for every group,
	// including the 20 VIRTUAL SINGER chapters.
	for unitIdx, unit := range mainStory {
		for catalogChapter, chapter := range unit.Chapters {
			appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, strings.ReplaceAll(chapter.AssetName, "_", "-"), chapter.Title, storyLabelResolution{
				storyType:  StoryLabelMainStory,
				index:      strconv.Itoa(unitIdx),
				indexLabel: model.UnitDict[unit.Unit],
				chapter:    catalogChapter,
			})
		}
	}

	// Activity cards use only valid card slots, exactly like GetJsonPath.
	for eventIdx := range events {
		ev := &events[eventIdx]
		validSlot := 0
		for _, cardID := range ev.Cards {
			if cardID < 1 || cardID > len(cards) {
				continue
			}
			card := cards[cardID-1]
			if card.CharacterID >= 1 && card.CharacterID <= len(model.CharacterDict) {
				charName := model.CharacterDict[card.CharacterID-1].Name
				saveTitle := "event" + padZero3(ev.ID) + "-" + charName
				for offset := 0; offset < 3; offset++ {
					chapter := validSlot*3 + offset
					idx := strconv.Itoa(ev.ID)
					appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, saveTitle, cardChapterTitle(chapter), storyLabelResolution{
						storyType:  StoryLabelCardEvent,
						index:      idx,
						indexLabel: idx + " " + ev.Title,
						chapter:    chapter,
					})
				}
			}
			validSlot++
		}
	}

	// Collaboration, birthday, and festival cards all share the festival catalog
	// and differ only in fesSaveTitle. Preserve the reverse dropdown coordinate.
	for festivalPos, festival := range festivals {
		for cardSlot, cardID := range festival.Cards {
			if cardID < 1 || cardID > len(cards) {
				continue
			}
			card := cards[cardID-1]
			if card.CharacterID < 1 || card.CharacterID > len(model.CharacterDict) {
				continue
			}
			charName := model.CharacterDict[card.CharacterID-1].Name
			for offset := 0; offset < 3; offset++ {
				chapter := cardSlot*3 + offset
				appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, fesSaveTitle(festival, charName), cardChapterTitle(chapter), storyLabelResolution{
					storyType:  StoryLabelCardSpecial,
					index:      strconv.Itoa(len(festivals) - 1 - festivalPos),
					indexLabel: festivalIndexLabel(festival),
					chapter:    chapter,
				})
			}
		}
	}

	// Initial cards are fully catalog-bounded by the production character table;
	// rarity is encoded in SaveTitle and the three chapter titles disambiguate it.
	for characterIdx := 0; characterIdx < 26 && characterIdx < len(model.CharacterDict); characterIdx++ {
		char := model.CharacterDict[characterIdx]
		for rarity := 1; rarity <= 4; rarity++ {
			saveTitle := "release-" + char.Name + "-" + padZero(rarity)
			for offset := 0; offset < 3; offset++ {
				chapter := (rarity-1)*3 + offset
				appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, saveTitle, cardChapterTitle(chapter), storyLabelResolution{
					storyType:  StoryLabelCardInit,
					index:      strconv.Itoa(characterIdx),
					indexLabel: char.NameJ,
					chapter:    chapter,
				})
			}
		}
	}

	// Upgrade cards surface one base three-part story per production character,
	// but GetJsonPath also accepts bounded legacy VIRTUAL SINGER coordinates that
	// load different cards under the same SaveTitle/chapter titles. Enumerate every
	// distinct loadable target so those exact identities fail closed instead of
	// silently resolving to the base card.
	var levelUpCards []int
	for _, festival := range festivals {
		if festival.LevelUp {
			levelUpCards = festival.Cards
			break
		}
	}
	for characterIdx := 0; characterIdx < 26 && characterIdx < len(model.CharacterDict) && characterIdx < len(levelUpCards); characterIdx++ {
		char := model.CharacterDict[characterIdx]
		saveTitle := "lvelup2023-" + char.Name
		for _, chapter := range upgradeCardIdentityChapters(characterIdx, levelUpCards, cards) {
			appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, saveTitle, cardChapterTitle(chapter), storyLabelResolution{
				storyType:  StoryLabelCardUpgrade,
				index:      strconv.Itoa(characterIdx),
				indexLabel: char.NameJ,
				chapter:    chapter,
			})
		}
	}

	// Area-talk SaveTitles contain only TalkID. Use one valid, deterministic
	// navigator coordinate and reject duplicate TalkIDs across underlying rows.
	// The catalog's historical filters overlap at AddEventID == 1, so classify
	// that boundary as initial here; upgrade starts after the initial release.
	for _, talk := range areaTalks {
		saveTitle := "areatalk-" + talk.TalkID
		if (!canonicalIdentityMatches(identity, saveTitle, "") && !legacyIdentityMatches(identity, saveTitle)) || talk.ScenarioID == "" || talk.ScenarioID == "none" {
			continue
		}
		areaStoryType := ""
		switch {
		case talk.Type == "limited":
			areaStoryType = StoryLabelAreaTalkExtra
		case talk.AddEventID <= 1:
			areaStoryType = StoryLabelAreaTalkInit
		case talk.AddEventID > 1:
			areaStoryType = StoryLabelAreaTalkUpgrade
		}
		areaIndex, areaIndexLabel, valid := canonicalAreaTalkCharacter(talk)
		if !valid || areaStoryType == "" {
			continue
		}
		for chapter, entry := range lm.getAreaTalkEntries(areaStoryType, "character", areaIndex) {
			if !entry.IsSeparator && entry.ID == talk.ID && entry.TalkID == talk.TalkID && entry.ScenarioID == talk.ScenarioID {
				appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, saveTitle, "", storyLabelResolution{
					storyType:  areaStoryType,
					index:      areaIndex,
					indexLabel: areaIndexLabel,
					chapter:    chapter,
				})
			}
		}
	}

	// Special-story titles are SaveTitles themselves and may contain spaces.
	for specialIdx, special := range specials {
		appendStoryLabelMatch(&exactResolutions, &legacyResolutions, identity, special.Title, "", storyLabelResolution{
			storyType:  StoryLabelSpecial,
			index:      strconv.Itoa(specialIdx),
			indexLabel: special.Title,
			chapter:    0,
		})
	}

	switch len(exactResolutions) {
	case 1:
		resolved := exactResolutions[0]
		return resolved.storyType, resolved.index, resolved.indexLabel, resolved.chapter, true, "exact", ""
	case 0:
		// Continue to the bounded legacy-title fallback below.
	default:
		return "", "", "", 0, false, "", "exact-ambiguous"
	}

	switch len(legacyResolutions) {
	case 1:
		resolved := legacyResolutions[0]
		return resolved.storyType, resolved.index, resolved.indexLabel, resolved.chapter, true, "legacy", ""
	case 0:
		return "", "", "", 0, false, "", "not-found"
	default:
		return "", "", "", 0, false, "", "legacy-ambiguous"
	}
}

func (lm *ListManager) ResolveLabel(label string) (storyType, index, indexLabel string, chapterIdx int, ok bool) {
	storyType, index, indexLabel, chapterIdx, ok, _, _ = lm.ResolveLabelDetailed(label)
	return
}

// IndexLabel returns the full index-dropdown label for a selection（活动为
// "<ID> <标题>"）。文稿目录按它命名；找不到时原样返回 index，调用方自行兜底。
func (lm *ListManager) IndexLabel(storyType, sort, index string) string {
	for _, it := range lm.GetStoryIndexList(storyType, sort) {
		if it.Value == index {
			return it.Label
		}
	}
	return index
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

// upgradeCardSlotIndex maps an upgrade-card navigator coordinate to the
// LevelUp festival card slot used by GetJsonPath. Coordinates 0..2 are the
// character's base card. Historical VIRTUAL SINGER documents may also carry a
// second/world-specific three-part coordinate; those targets are loadable and
// must participate in reverse-identity ambiguity checks even though the current
// navigator exposes only the base three entries.
func upgradeCardSlotIndex(characterIdx, chapterIdx, levelUpCardCount int) (int, bool) {
	if characterIdx < 0 || characterIdx >= 26 || chapterIdx < 0 || levelUpCardCount <= 0 {
		return 0, false
	}
	if chapterIdx < 3 {
		if characterIdx >= levelUpCardCount {
			return 0, false
		}
		return characterIdx, true
	}

	charID := characterIdx + 1
	if charID < 21 {
		return 0, false
	}
	var slot int
	switch charID {
	case 21: // Miku: one legacy group for each remaining VS/world card slot.
		slot = 24 + chapterIdx/3
	case 22: // Rin -> MMJ Miku slot.
		slot = len(model.CharacterDict) - 4
	case 23: // Len -> VBS Miku slot.
		slot = len(model.CharacterDict) - 3
	case 24: // Luka -> Leo/need Miku slot.
		slot = len(model.CharacterDict) - 5
	case 25: // MEIKO -> Wonderlands Miku slot.
		slot = len(model.CharacterDict) - 2
	case 26: // KAITO -> Nightcord Miku slot.
		slot = len(model.CharacterDict) - 1
	default:
		return 0, false
	}
	if slot < 0 || slot >= levelUpCardCount {
		return 0, false
	}
	return slot, true
}

// upgradeCardIdentityChapters enumerates one three-part coordinate for every
// distinct loadable card target. Some legacy VS coordinates repeat the same
// target forever; de-duplicate by card slot so the candidate set is finite while
// still detecting canonical 前篇/后篇/特殊篇 collisions.
func upgradeCardIdentityChapters(characterIdx int, levelUpCards []int, cards []CardEntry) []int {
	chapters := make([]int, 0, 6)
	seenSlots := make(map[int]bool)
	for group := 0; group <= len(levelUpCards)+1; group++ {
		chapter := group * 3
		slot, ok := upgradeCardSlotIndex(characterIdx, chapter, len(levelUpCards))
		if !ok || seenSlots[slot] {
			break
		}
		seenSlots[slot] = true
		cardID := levelUpCards[slot]
		if cardID < 1 || cardID > len(cards) {
			continue
		}
		chapters = append(chapters, chapter, chapter+1, chapter+2)
	}
	return chapters
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
	return lm.buildVoiceIDCluesLocked()
}

func (lm *ListManager) buildVoiceIDCluesLocked() map[string]EventEntry {
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

func (lm *ListManager) flashbackIndexes() (map[string]EventEntry, map[string]MainStoryEntry) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	clues := lm.buildVoiceIDCluesLocked()
	mainstory := make(map[string]MainStoryEntry, len(lm.MainStory))
	for _, ms := range lm.MainStory {
		mainstory[ms.Unit] = ms
	}
	return clues, mainstory
}

// InferVoiceEventID infers voice event IDs from area talks and stores them in events.
func (lm *ListManager) InferVoiceEventID() {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.inferVoiceEventIDLocked()
}

func (lm *ListManager) inferVoiceEventIDLocked() {
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
