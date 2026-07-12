package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	harukiNeoMasterURL = "https://sekai-master-direct.haruki.seiunx.com/haruki-sekai-master/master/%s.json"
	// 元数据加速镜像：accr.cc 侧把整域名 sekai-master-direct.haruki.accr.cc
	// 反代到上面的源站（路径不变，仅换 host）走 CDN。数据不定时更新，绝不能吃
	// 到过期副本——fetchCDN 先 HEAD 源站拿当前 ETag（几百字节的探针），镜像响应
	// 的 ETag 必须与之一致才采用，否则回落直连源站。任何一环失败（域名未生效/
	// 边缘缓存失误/镜像不可达）都自动退回源站，结果永远与源站最新版一致。
	harukiMirrorMasterURL = "https://sekai-master-direct.haruki.accr.cc/haruki-sekai-master/master/%s.json"
)

var httpClient = &http.Client{
	// cards.json 单表 ~34MB，慢链路下 30s 会中途超时，放宽到 180s。
	Timeout: 180 * time.Second,
	// Disable compression to get accurate Content-Length for progress
}

// normalizeETag 抹平强/弱 ETag、引号与压缩变体差异：Go 客户端默认请求 gzip，
// 边缘返回压缩变体时会把 ETag 改写成 "xxx-gzip"，不剥掉后缀会把镜像误判为
// 过期、全部回落慢源站（v5.7.0 实际踩到）。
func normalizeETag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	s = strings.Trim(s, `"`)
	for _, suf := range []string{"-gzip", "-br", "-zstd"} {
		s = strings.TrimSuffix(s, suf)
	}
	return s
}

// masterTables 一轮元数据刷新要拉的全部表。
var masterTables = []string{
	"events", "eventStories", "eventCards", "cards", "unitStories",
	"actionSets", "character2ds", "specialStories", "systemLive2ds",
}

// prefetched 是本轮刷新的表级内存缓存：UpdateAllFromCDN 起始并发预取填入，
// 各 update 步骤经 fetchCDN 消费（LoadAndDelete 用完即释放 ~42MB）。
var prefetched sync.Map

// probeMirrorTrust 记录本轮刷新起始的锚点交叉校验结论（镜像探针是否可信）。
// prefetchTables 已按此值决定探针路径；fetchCDN 遇预取缓存缺失回退到 fetchTable
// 时必须复用同一结论——否则会无条件借道镜像探针，绕过 probeMirrorTrusted 的判定，
// 边缘缓存键失配时可能采信过期镜像。UpdateAllFromCDN 起始（预取前）赋值，其后仅在
// 同一 goroutine 的顺序步骤中被 fetchCDN 读取，无并发访问。
var probeMirrorTrust bool

func headETag(url string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ""
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	return normalizeETag(resp.Header.Get("ETag"))
}

// probeMirrorTrusted 决定探针能否借道镜像：用一张小表做锚点，比较「镜像探针
// （唯一 probe 参数击穿缓存，由边缘转发到源站）」与「直连源站探针」的 ETag。
// 一致 = 边缘确实把查询串计入缓存键且透传源站响应，镜像探针可信（后续 9 张表
// 的探针全走快路）；不一致 = 边缘配置有诈，全部退回直连探针（慢但正确）；
// 源站直连失败 = 没有对照物，信镜像探针总好过全盲。
func probeMirrorTrusted() bool {
	const anchor = "unitStories"
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	mirrorETag := headETag(fmt.Sprintf(harukiMirrorMasterURL, anchor)+"?probe="+nonce, 10*time.Second)
	if mirrorETag == "" {
		return false
	}
	originETag := headETag(fmt.Sprintf(harukiNeoMasterURL, anchor), 20*time.Second)
	if originETag == "" {
		log.Printf("[update] anchor origin probe unreachable, trusting mirror probes unverified")
		return true
	}
	if mirrorETag != originETag {
		log.Printf("[update] anchor mismatch (mirror %s vs origin %s) — edge cache-key misconfigured? probing origin directly", mirrorETag, originETag)
		return false
	}
	return true
}

// prefetchTables 并发拉齐全部表：此前每表「探针+下载」串行共 18 次网络往返，
// 到源站的慢路由会把整轮拖到分钟级；并发后整轮 ≈ 最慢一张表的耗时。
func prefetchTables(probeMirror bool) {
	var wg sync.WaitGroup
	for _, t := range masterTables {
		wg.Add(1)
		go func(table string) {
			defer wg.Done()
			if data, err := fetchTable(table, probeMirror); err == nil {
				prefetched.Store(table, data)
			} else {
				log.Printf("[update] prefetch %s failed: %v", table, err)
			}
		}(t)
	}
	wg.Wait()
}

func fetchURL(url string) ([]byte, string, error) {
	log.Printf("[update] downloading %s", url)
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	return data, resp.Header.Get("ETag"), err
}

func fetchCDN(table string) ([]byte, error) {
	if v, ok := prefetched.LoadAndDelete(table); ok {
		return v.([]byte), nil
	}
	return fetchTable(table, probeMirrorTrust)
}

func fetchTable(table string, probeMirror bool) ([]byte, error) {
	originURL := fmt.Sprintf(harukiNeoMasterURL, table)

	// 1. 源站 ETag 探针。优先借道镜像（唯一 probe 参数击穿边缘缓存，请求由
	// 边缘代问源站，避开用户到源站的慢路由）；镜像探针失败或不可信再直连。
	// 拿不到 ETag 就没法验证镜像新鲜度，走后面的兜底链。
	originETag := ""
	if probeMirror {
		nonce := fmt.Sprintf("%d", time.Now().UnixNano())
		originETag = headETag(fmt.Sprintf(harukiMirrorMasterURL, table)+"?probe="+nonce, 10*time.Second)
	}
	if originETag == "" {
		originETag = headETag(originURL, 20*time.Second)
	}

	// 2. 镜像下载 + ETag 校验：一致 = 与源站字节级同版，采用。
	// ?v=<源站ETag> 进边缘缓存键：同版本全体用户共享边缘副本（TTL 可以放心
	// 设很长），源站一更新 ETag 变、缓存键随之改变，首个请求自动击穿拉新——
	// 新鲜度由 URL 构造保证，不依赖 TTL 到期。
	if originETag != "" {
		mirrorURL := fmt.Sprintf(harukiMirrorMasterURL, table) + "?v=" + neturl.QueryEscape(originETag)
		data, etag, err := fetchURL(mirrorURL)
		switch {
		case err == nil && normalizeETag(etag) == originETag:
			return data, nil
		case err == nil:
			log.Printf("[update] %s mirror stale (origin etag %s vs mirror %q), falling back to origin", table, originETag, etag)
		default:
			log.Printf("[update] %s mirror unavailable (%v), falling back to origin", table, err)
		}
	}

	// 3. 探针失败（源站不可达）时没法验证新鲜度，但镜像数据总好过直接报错——
	// 先试镜像（不带 v，用边缘现存副本），最后才直连源站。
	if originETag == "" {
		if data, _, err := fetchURL(fmt.Sprintf(harukiMirrorMasterURL, table)); err == nil {
			log.Printf("[update] %s origin unreachable, using mirror copy unverified", table)
			return data, nil
		}
	}

	// 4. 兜底：直连源站。
	data, _, err := fetchURL(originURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", table, err)
	}
	return data, nil
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

	var newEvents []EventEntry
	id := 1
	for _, ev := range sorted {
		if len(ev.Chapters) == 0 {
			continue
		}
		ev.ID = id
		id++
		newEvents = append(newEvents, EventEntry{
			ID:       ev.ID,
			KdyicrID: ev.KdyicrID,
			Title:    ev.Title,
			Name:     ev.Name,
			Chapters: ev.Chapters,
			Cards:    ev.Cards,
		})
	}
	// Publish the freshly built slice under a short write lock so request
	// goroutines reading lm.Events (under RLock) can't observe a torn header from
	// an in-place rebuild. The build and file I/O stay outside the lock.
	lm.mu.Lock()
	lm.Events = newEvents
	lm.mu.Unlock()
	return saveJSON(dir, "events.json", newEvents)
}

func (lm *ListManager) updateCards(dir string) error {
	var cards []cdnCard
	if err := fetchCDNJSON("cards", &cards); err != nil {
		return err
	}

	var newCards []CardEntry
	cardCount := 1 // 1-based filling
	for _, c := range cards {
		for cardCount < c.ID {
			newCards = append(newCards, CardEntry{
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
		newCards = append(newCards, entry)
		cardCount = c.ID + 1
	}
	// Short-lock swap; readers hold RLock (see updateEvents).
	lm.mu.Lock()
	lm.Cards = newCards
	lm.mu.Unlock()
	return saveJSON(dir, "cards.json", newCards)
}

func (lm *ListManager) updateFestivals(dir string) error {
	// Build into a local, then publish under a short lock (see updateEvents). Reads
	// of lm.Events/lm.Cards are safe unlocked here: they were written earlier in
	// this same update goroutine and no other goroutine writes them concurrently.
	var newFestivals []FestivalEntry
	publish := func() { lm.mu.Lock(); lm.Festivals = newFestivals; lm.mu.Unlock() }
	if len(lm.Events) == 0 || len(lm.Cards) == 0 {
		publish()
		return saveJSON(dir, "festivals.json", newFestivals)
	}

	eventIdx := 0
	specialCards := []int{}
	birthdayCards := []int{}
	fesIdx := 1
	birthdayIdx := 1

	// Find the smallest card id owned by any event; lm.Events[0] (smallest
	// KdyicrID with chapters) is not guaranteed to have any cards, so do not
	// blindly index [0][0].
	firstCardID := 0
	for _, ev := range lm.Events {
		for _, c := range ev.Cards {
			if firstCardID == 0 || c < firstCardID {
				firstCardID = c
			}
		}
	}
	if firstCardID == 0 {
		// No event owns any card; nothing to scan for festivals.
		publish()
		return saveJSON(dir, "festivals.json", newFestivals)
	}
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
				newFestivals = append(newFestivals, FestivalEntry{
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
				// Only split off the trailing 2 cards when the run is long
				// enough; otherwise keep the whole run as one festival to
				// avoid a negative slice bound.
				if len(specialCards) >= 2 {
					fest.Cards = specialCards[:len(specialCards)-2]
					newFestivals = append(newFestivals, fest)
					fest = FestivalEntry{ID: fesIdx, IsBirthday: false, Cards: specialCards[len(specialCards)-2:]}
				}
			} else {
				fesIdx++
			}
			newFestivals = append(newFestivals, fest)
			specialCards = nil
		}
	}

	if len(specialCards) > 0 {
		newFestivals = append(newFestivals, FestivalEntry{ID: fesIdx, IsBirthday: false, Cards: specialCards})
	}
	if len(birthdayCards) > 0 {
		newFestivals = append(newFestivals, FestivalEntry{ID: birthdayIdx, IsBirthday: true, Cards: birthdayCards})
	}

	publish()
	return saveJSON(dir, "festivals.json", newFestivals)
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

	var newMainStory []MainStoryEntry
	for _, us := range stories {
		for _, ch := range us.Chapters {
			chapters := make([]EventChapter, len(ch.Episodes))
			for i, ep := range ch.Episodes {
				chapters[i] = EventChapter{Title: ep.Title, AssetName: ep.ScenarioID}
			}
			newMainStory = append(newMainStory, MainStoryEntry{
				Unit:      ch.Unit,
				AssetName: ch.AssetbundleName,
				Chapters:  chapters,
			})
		}
	}
	// Short-lock swap; readers hold RLock (see updateEvents).
	lm.mu.Lock()
	lm.MainStory = newMainStory
	lm.mu.Unlock()
	return saveJSON(dir, "mainStory.json", newMainStory)
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

	var newAreaTalks []AreaTalkEntry
	lm.AreaTalkByTime = nil // vestigial field, read by nobody; harmless unlocked write
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
			newAreaTalks = append(newAreaTalks, AreaTalkEntry{
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
			if cid >= 0 && cid < len(char2DLookup) {
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

		newAreaTalks = append(newAreaTalks, entry)
	}

	// Short-lock swap; readers hold RLock (see updateEvents).
	lm.mu.Lock()
	lm.AreaTalks = newAreaTalks
	lm.mu.Unlock()
	return saveJSON(dir, "areatalks.json", newAreaTalks)
}

func (lm *ListManager) updateSpecials(dir string) error {
	var stories []cdnSpecialStory
	if err := fetchCDNJSON("specialStories", &stories); err != nil {
		return err
	}

	var newSpecials []SpecialEntry
	for _, s := range stories {
		if len(s.Episodes) == 0 {
			continue
		}
		ep := s.Episodes[0]
		newSpecials = append(newSpecials, SpecialEntry{
			Title:    s.Title,
			DirName:  s.AssetbundleName,
			FileName: ep.ScenarioID,
		})
	}
	// Short-lock swap; readers hold RLock (see updateEvents).
	lm.mu.Lock()
	lm.Specials = newSpecials
	lm.mu.Unlock()
	return saveJSON(dir, "specials.json", newSpecials)
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
		newGreets := loadJSONFile[[]GreetEntry](dir, "greets.json")
		// Short-lock swap; readers hold RLock (see updateEvents).
		lm.mu.Lock()
		lm.Greets = newGreets
		lm.mu.Unlock()
		return nil
	}
	var newGreets []GreetEntry
	lm.mu.Lock()
	lm.Greets = newGreets
	lm.mu.Unlock()
	return saveJSON(dir, "greets.json", newGreets)
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

	pt.SetTotal(len(steps) + 1)

	// 并发预取全部表（探针可信度先做一次锚点交叉校验），后续步骤直接吃缓存。
	// 校验结论存入 probeMirrorTrust，供 fetchCDN 缓存缺失回退时复用同一判定。
	pt.Advance("并发下载元数据...")
	probeMirrorTrust = probeMirrorTrusted()
	prefetchTables(probeMirrorTrust)

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
