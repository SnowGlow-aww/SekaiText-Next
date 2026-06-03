package service

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const harukiNeoMasterURL = "https://sekai-master-direct.haruki.seiunx.com/haruki-sekai-master/master/%s.json"

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	// Disable compression to get accurate Content-Length for progress
}

func fetchCDN(table string) ([]byte, error) {
	url := fmt.Sprintf(harukiNeoMasterURL, table)
	log.Printf("[update] downloading %s", url)
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", table, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", table, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func fetchCDNJSON(table string, target interface{}) error {
	data, err := fetchCDN(table)
	if err != nil {
		return err
	}
	// Some CDN responses wrap in {"data": [...]}
	var enveloped struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &enveloped) == nil && enveloped.Data != nil {
		return json.Unmarshal(enveloped.Data, target)
	}
	return json.Unmarshal(data, target)
}

func saveJSON(dir, filename string, v interface{}) error {
	path := filepath.Join(dir, filename)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// --- Raw CDN types ---

type cdnEvent struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	AssetbundleName string `json:"assetbundleName"`
}

type cdnEventStory struct {
	ID                int                `json:"id"`
	EventStoryEpisodes []cdnEventEpisode `json:"eventStoryEpisodes"`
}

type cdnEventEpisode struct {
	Title      string `json:"title"`
	ScenarioID string `json:"scenarioId"`
}

type cdnEventCard struct {
	ID      int `json:"id"`
	EventID int `json:"eventId"`
	CardID  int `json:"cardId"`
}

type cdnCard struct {
	ID              int    `json:"id"`
	CharacterID     int    `json:"characterId"`
	AssetbundleName string `json:"assetbundleName"`
	CardRarityType  string `json:"cardRarityType"`
}

type cdnUnitStory struct {
	Seq      int              `json:"seq"`
	Chapters []cdnUnitChapter `json:"chapters"`
}

type cdnUnitChapter struct {
	Unit            string            `json:"unit"`
	AssetbundleName string            `json:"assetbundleName"`
	Episodes        []cdnEventEpisode `json:"episodes"`
}

type cdnActionSet struct {
	ID                int    `json:"id"`
	AreaID            int    `json:"areaId"`
	CharacterIDs      []int  `json:"characterIds"`
	ScenarioID        string `json:"scenarioId"`
	ActionSetType     string `json:"actionSetType"`
	ReleaseConditionID int   `json:"releaseConditionId"`
}

type cdnCharacter2D struct {
	ID            int    `json:"id"`
	CharacterType string `json:"characterType"`
	CharacterID   int    `json:"characterId"`
	Unit          string `json:"unit"`
	AssetName     string `json:"assetName"`
}

type cdnSystemLive2D struct {
	ID          int    `json:"id"`
	CharacterID int    `json:"characterId"`
	Unit        string `json:"unit"`
	Serif       string `json:"serif"`
}

type cdnSpecialStory struct {
	ID              int                `json:"id"`
	Title           string             `json:"title"`
	AssetbundleName string             `json:"assetbundleName"`
	Episodes        []cdnEventEpisode `json:"episodes"`
}

// --- Update functions ---

func (lm *ListManager) updateEvents(dir string) error {
	var events []cdnEvent
	if err := fetchCDNJSON("events", &events); err != nil {
		return err
	}
	var stories []cdnEventStory
	if err := fetchCDNJSON("eventStories", &stories); err != nil {
		return err
	}
	var eventCards []cdnEventCard
	if err := fetchCDNJSON("eventCards", &eventCards); err != nil {
		return err
	}

	// Build all_events map
	type eventBuilder struct {
		KdyicrID int
		ID       int
		Title    string
		Name     string
		Chapters []EventChapter
		Cards    []int
	}
	allEvents := make(map[int]*eventBuilder)

	cardIdx := 0
	for _, e := range events {
		ec := []int{}
		for cardIdx < len(eventCards) && eventCards[cardIdx].EventID < e.ID {
			cardIdx++
		}
		for cardIdx < len(eventCards) && eventCards[cardIdx].EventID == e.ID {
			ec = append(ec, eventCards[cardIdx].CardID)
			cardIdx++
		}
		allEvents[e.ID] = &eventBuilder{
			KdyicrID: e.ID,
			ID:       -1,
			Title:    e.Name,
			Name:     e.AssetbundleName,
			Cards:    ec,
		}
	}

	for _, es := range stories {
		if ev, ok := allEvents[es.ID]; ok {
			chapters := make([]EventChapter, len(es.EventStoryEpisodes))
			for i, ep := range es.EventStoryEpisodes {
				chapters[i] = EventChapter{Title: ep.Title, AssetName: ep.ScenarioID}
			}
			ev.Chapters = chapters
		}
	}

	// Build sorted output
	sorted := make([]*eventBuilder, 0, len(allEvents))
	for _, ev := range allEvents {
		sorted = append(sorted, ev)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].KdyicrID < sorted[j].KdyicrID })

	lm.Events = nil
	id := 1
	for _, ev := range sorted {
		if len(ev.Chapters) == 0 {
			continue
		}
		ev.ID = id
		id++
		lm.Events = append(lm.Events, EventEntry{
			ID:       ev.ID,
			KdyicrID: ev.KdyicrID,
			Title:    ev.Title,
			Name:     ev.Name,
			Chapters: ev.Chapters,
			Cards:    ev.Cards,
		})
	}
	return saveJSON(dir, "events.json", lm.Events)
}

func (lm *ListManager) updateCards(dir string) error {
	var cards []cdnCard
	if err := fetchCDNJSON("cards", &cards); err != nil {
		return err
	}

	lm.Cards = nil
	cardCount := 1 // 1-based filling
	for _, c := range cards {
		for cardCount < c.ID {
			lm.Cards = append(lm.Cards, CardEntry{
				ID:          cardCount,
				CharacterID: -1,
				CardNo:      "000",
				Birthday:    false,
			})
			cardCount++
		}
		assetName := c.AssetbundleName
		cardNo := "000"
		if len(assetName) >= 3 {
			cardNo = assetName[len(assetName)-3:]
		}
		bday := c.CardRarityType == "rarity_birthday"
		entry := CardEntry{
			ID:          c.ID,
			CharacterID: c.CharacterID,
			CardNo:      cardNo,
			Birthday:    bday,
		}
		if c.ID >= 724 && c.ID <= 759 {
			entry.LevelUp = true
		}
		lm.Cards = append(lm.Cards, entry)
		cardCount = c.ID + 1
	}
	return saveJSON(dir, "cards.json", lm.Cards)
}

func (lm *ListManager) updateFestivals(dir string) error {
	lm.Festivals = nil
	if len(lm.Events) == 0 || len(lm.Cards) == 0 {
		return saveJSON(dir, "festivals.json", lm.Festivals)
	}

	eventIdx := 0
	specialCards := []int{}
	birthdayCards := []int{}
	fesIdx := 1
	birthdayIdx := 1

	firstCardID := lm.Events[0].Cards[0]
	lastCardID := lm.Cards[len(lm.Cards)-1].ID

	i := firstCardID
	for i <= lastCardID {
		// Skip cards that belong to events
		for eventIdx < len(lm.Events) && containsInt(lm.Events[eventIdx].Cards, i) {
			for i <= lastCardID && containsInt(lm.Events[eventIdx].Cards, i) {
				i++
			}
			eventIdx++
		}
		if i > lastCardID {
			break
		}

		if lm.Cards[i-1].Birthday {
			birthdayCards = append(birthdayCards, i)
			// Flush birthday group for specific characters
			if containsInt(birthdayCards, i) && containsInt([]int{7, 16, 14, 23}, lm.Cards[i-1].CharacterID) || len(birthdayCards) >= 26 {
				lm.Festivals = append(lm.Festivals, FestivalEntry{
					ID:         birthdayIdx,
					IsBirthday: true,
					Cards:      birthdayCards,
				})
				birthdayIdx++
				birthdayCards = nil
			}
			i++
			continue
		}

		// Collect non-event, non-birthday cards
		for i <= lastCardID {
			if eventIdx < len(lm.Events) {
				if containsInt(lm.Events[eventIdx].Cards, i) || lm.Cards[i-1].Birthday {
					break
				}
			}
			specialCards = append(specialCards, i)
			i++
		}

		if len(specialCards) > 0 {
			fest := FestivalEntry{
				ID:         fesIdx,
				IsBirthday: false,
				Cards:      specialCards,
			}
			// Special cases from Python logic
			if containsInt(specialCards, 335) {
				fest.ID = 1
				fest.Collaboration = "悪ノ大罪"
				fest.Cards = specialCards[:len(specialCards)-1]
			} else if containsInt(specialCards, 724) {
				fest.ID = 1
				fest.LevelUp = true
				fest.Cards = specialCards[:len(specialCards)-2]
				lm.Festivals = append(lm.Festivals, fest)
				fest = FestivalEntry{ID: fesIdx, IsBirthday: false, Cards: specialCards[len(specialCards)-2:]}
			} else {
				fesIdx++
			}
			lm.Festivals = append(lm.Festivals, fest)
			specialCards = nil
		}
	}

	if len(specialCards) > 0 {
		lm.Festivals = append(lm.Festivals, FestivalEntry{ID: fesIdx, IsBirthday: false, Cards: specialCards})
	}
	if len(birthdayCards) > 0 {
		lm.Festivals = append(lm.Festivals, FestivalEntry{ID: birthdayIdx, IsBirthday: true, Cards: birthdayCards})
	}

	return saveJSON(dir, "festivals.json", lm.Festivals)
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func (lm *ListManager) updateMainStory(dir string) error {
	var stories []cdnUnitStory
	if err := fetchCDNJSON("unitStories", &stories); err != nil {
		return err
	}

	sort.Slice(stories, func(i, j int) bool { return stories[i].Seq < stories[j].Seq })

	lm.MainStory = nil
	for _, us := range stories {
		for _, ch := range us.Chapters {
			chapters := make([]EventChapter, len(ch.Episodes))
			for i, ep := range ch.Episodes {
				chapters[i] = EventChapter{Title: ep.Title, AssetName: ep.ScenarioID}
			}
			lm.MainStory = append(lm.MainStory, MainStoryEntry{
				Unit:      ch.Unit,
				AssetName: ch.AssetbundleName,
				Chapters:  chapters,
			})
		}
	}
	return saveJSON(dir, "mainStory.json", lm.MainStory)
}

func (lm *ListManager) updateAreaTalks(dir string) error {
	var actions []cdnActionSet
	if err := fetchCDNJSON("actionSets", &actions); err != nil {
		return err
	}
	var char2ds []cdnCharacter2D
	if err := fetchCDNJSON("character2ds", &char2ds); err != nil {
		return err
	}

	// Build character2D lookup (fill gaps)
	char2DLookup := make([]cdnCharacter2D, 0)
	count := 0
	for _, c := range char2ds {
		for count < c.ID {
			char2DLookup = append(char2DLookup, cdnCharacter2D{
				ID: count, CharacterType: "none", CharacterID: 0, Unit: "none", AssetName: "none",
			})
			count++
		}
		char2DLookup = append(char2DLookup, c)
		count = c.ID + 1
	}

	lm.AreaTalks = nil
	lm.AreaTalkByTime = nil
	actionCount := 0
	areatalkCount := 0
	specialAreatalkCount := 0
	addEventID := 1

	// Build event lookup: kdyicr_id → output id
	eventIDMap := make(map[int]int)
	for _, ev := range lm.Events {
		eventIDMap[ev.KdyicrID] = ev.ID
	}

	for _, action := range actions {
		actionCount++
		for actionCount < action.ID {
			lm.AreaTalks = append(lm.AreaTalks, AreaTalkEntry{
				ID: actionCount, TalkID: "-1", AreaID: -1,
				ScenarioID: "none", Type: "none", AddEventID: -1, ReleaseEventID: -1,
			})
			actionCount++
		}

		releaseEventID := action.ReleaseConditionID
		if releaseEventID > 100000 {
			releaseEventID = ((releaseEventID % 100000) / 100) + 1
		}
		if releaseEventID > 1000 {
			releaseEventID = -1
		}
		if mapped, ok := eventIDMap[releaseEventID]; ok {
			releaseEventID = mapped
		}
		if action.ID == 618 {
			releaseEventID = 1
		}
		if releaseEventID > addEventID {
			addEventID = releaseEventID
		}

		charIDs := make([]int, 0)
		for _, cid := range action.CharacterIDs {
			if cid < len(char2DLookup) {
				charIDs = append(charIDs, char2DLookup[cid].CharacterID)
			}
		}

		scenarioID := action.ScenarioID
		if scenarioID == "" {
			scenarioID = "none"
		}
		actionType := action.ActionSetType
		if actionType == "" {
			actionType = "none"
		}

		entry := AreaTalkEntry{
			ID:             actionCount,
			AreaID:         action.AreaID,
			CharacterIDs:   charIDs,
			ScenarioID:     scenarioID,
			Type:            actionType,
			AddEventID:     addEventID,
			ReleaseEventID: releaseEventID,
		}

		if entry.ScenarioID != "none" {
			if entry.Type == "normal" {
				areatalkCount++
				entry.TalkID = fmt.Sprintf("%04d", areatalkCount)
			} else if entry.Type != "none" {
				specialAreatalkCount++
				entry.TalkID = fmt.Sprintf("S%04d", specialAreatalkCount)
			}
		}
		if entry.TalkID == "" {
			entry.TalkID = "-1"
		}

		lm.AreaTalks = append(lm.AreaTalks, entry)
	}

	return saveJSON(dir, "areatalks.json", lm.AreaTalks)
}

func (lm *ListManager) updateSpecials(dir string) error {
	var stories []cdnSpecialStory
	if err := fetchCDNJSON("specialStories", &stories); err != nil {
		return err
	}

	lm.Specials = nil
	for _, s := range stories {
		if len(s.Episodes) == 0 {
			continue
		}
		ep := s.Episodes[0]
		lm.Specials = append(lm.Specials, SpecialEntry{
			Title:    s.Title,
			DirName:  s.AssetbundleName,
			FileName: ep.ScenarioID,
		})
	}
	return saveJSON(dir, "specials.json", lm.Specials)
}

func (lm *ListManager) updateGreets(dir string) error {
	var greets []cdnSystemLive2D
	if err := fetchCDNJSON("systemLive2ds", &greets); err != nil {
		return err
	}
	_ = greets
	// Greet processing is extremely complex (hundreds of lines in Python).
	// For now, keep the existing greets.json if it exists.
	path := filepath.Join(dir, "greets.json")
	if _, err := os.Stat(path); err == nil {
		lm.Greets = loadJSONFile[[]GreetEntry](dir, "greets.json")
	return nil
	}
	lm.Greets = nil
	return saveJSON(dir, "greets.json", lm.Greets)
}

// UpdateAllFromCDN downloads and processes all metadata from the haruki neo CDN.
func (lm *ListManager) UpdateAllFromCDN(dir string, pt *ProgressTracker) {
	steps := []struct {
		name string
		fn   func(string) error
	}{
		{"events", lm.updateEvents},
		{"cards", lm.updateCards},
		{"festivals", lm.updateFestivals},
		{"mainStory", lm.updateMainStory},
		{"areaTalks", lm.updateAreaTalks},
		{"specials", lm.updateSpecials},
		{"greets", lm.updateGreets},
	}

	pt.SetTotal(len(steps))

	for _, step := range steps {
		pt.Advance("正在更新 " + step.name + "...")
		if err := step.fn(dir); err != nil {
			log.Printf("[update] %s failed: %v", step.name, err)
		} else {
			log.Printf("[update] %s updated", step.name)
		}
	}

	// Reload from files
	lm.loadAll()
	pt.Done()
	log.Println("[update] metadata refresh complete")
}
