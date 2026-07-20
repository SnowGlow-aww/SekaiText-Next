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
	"strconv"
	"strings"
	"time"

	"sekaitext/backend/internal/fsutil"
)

const (
	harukiNeoMasterURL  = "https://sekai-master-direct.haruki.seiunx.com/haruki-sekai-master/master/%s.json"
	maxMasterTableBytes = 128 << 20
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

// tableBatch owns one refresh's prefetched tables. Keeping this state local to
// UpdateAllFromCDN prevents an unrelated lazy lookup or overlapping refresh from
// consuming another generation's bytes.
type tableBatch struct {
	data map[string][]byte
}

// catalogData is one complete, immutable metadata generation. Refreshes build
// this value off to the side and publish every field under one ListManager lock.
type catalogData struct {
	Events    []EventEntry
	Festivals []FestivalEntry
	Cards     []CardEntry
	MainStory []MainStoryEntry
	AreaTalks []AreaTalkEntry
	Greets    []GreetEntry
	Specials  []SpecialEntry
}

type catalogManifest struct {
	Version    int    `json:"version"`
	Generation uint64 `json:"generation"`
	Dir        string `json:"dir"`
}

const (
	catalogManifestFile  = "catalog-current.json"
	catalogGenerationDir = ".catalog-generations"
)

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
func prefetchTables(probeMirror bool) (*tableBatch, error) {
	type result struct {
		table string
		data  []byte
		err   error
	}
	results := make(chan result, len(masterTables))
	for _, t := range masterTables {
		go func(table string) {
			data, err := fetchTable(table, probeMirror)
			results <- result{table: table, data: data, err: err}
		}(t)
	}
	batch := &tableBatch{data: make(map[string][]byte, len(masterTables))}
	var failures []string
	for range masterTables {
		result := <-results
		if result.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", result.table, result.err))
			continue
		}
		batch.data[result.table] = result.data
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		return nil, fmt.Errorf("prefetch failed: %s", strings.Join(failures, "; "))
	}
	return batch, nil
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
	if resp.ContentLength > maxMasterTableBytes {
		return nil, "", fmt.Errorf("response exceeds %d byte limit", maxMasterTableBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMasterTableBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || len(data) > maxMasterTableBytes || !json.Valid(data) {
		return nil, "", fmt.Errorf("response is empty, oversized, or not JSON")
	}
	return data, resp.Header.Get("ETag"), nil
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
	data, err := fetchTable(table, probeMirrorTrusted())
	if err != nil {
		return err
	}
	return decodeCDNJSON(data, target)
}

func (batch *tableBatch) fetchJSON(table string, target interface{}) error {
	data, ok := batch.data[table]
	if !ok {
		return fmt.Errorf("table %s was not prefetched", table)
	}
	delete(batch.data, table)
	return decodeCDNJSON(data, target)
}

func decodeCDNJSON(data []byte, target interface{}) error {
	// Some CDN responses wrap in {"data": [...]}
	var enveloped struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &enveloped) == nil && enveloped.Data != nil {
		return json.Unmarshal(enveloped.Data, target)
	}
	return json.Unmarshal(data, target)
}

// --- Raw CDN types ---

type cdnEvent struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	AssetbundleName string `json:"assetbundleName"`
}

type cdnEventStory struct {
	ID                 int               `json:"id"`
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
	ID                 int    `json:"id"`
	AreaID             int    `json:"areaId"`
	CharacterIDs       []int  `json:"characterIds"`
	ScenarioID         string `json:"scenarioId"`
	ActionSetType      string `json:"actionSetType"`
	ReleaseConditionID int    `json:"releaseConditionId"`
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
	ID              int               `json:"id"`
	Title           string            `json:"title"`
	AssetbundleName string            `json:"assetbundleName"`
	Episodes        []cdnEventEpisode `json:"episodes"`
}

// --- Catalog builders ---

func fetchBatchSlice[T any](batch *tableBatch, table string) ([]T, error) {
	var values []T
	if err := batch.fetchJSON(table, &values); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("table %s is empty", table)
	}
	return values, nil
}

func buildEvents(batch *tableBatch) ([]EventEntry, error) {
	events, err := fetchBatchSlice[cdnEvent](batch, "events")
	if err != nil {
		return nil, err
	}
	stories, err := fetchBatchSlice[cdnEventStory](batch, "eventStories")
	if err != nil {
		return nil, err
	}
	eventCards, err := fetchBatchSlice[cdnEventCard](batch, "eventCards")
	if err != nil {
		return nil, err
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

	eventCardIDs := make(map[int][]int)
	for _, card := range eventCards {
		if card.EventID <= 0 || card.CardID <= 0 {
			return nil, fmt.Errorf("eventCards contains invalid ids: event=%d card=%d", card.EventID, card.CardID)
		}
		eventCardIDs[card.EventID] = append(eventCardIDs[card.EventID], card.CardID)
	}
	for _, e := range events {
		if e.ID <= 0 {
			return nil, fmt.Errorf("events contains invalid id %d", e.ID)
		}
		if _, exists := allEvents[e.ID]; exists {
			return nil, fmt.Errorf("events contains duplicate id %d", e.ID)
		}
		allEvents[e.ID] = &eventBuilder{
			KdyicrID: e.ID,
			ID:       -1,
			Title:    e.Name,
			Name:     e.AssetbundleName,
			Cards:    append([]int(nil), eventCardIDs[e.ID]...),
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
	if len(newEvents) == 0 {
		return nil, fmt.Errorf("events produced no usable stories")
	}
	return newEvents, nil
}

func buildCards(batch *tableBatch) ([]CardEntry, error) {
	cards, err := fetchBatchSlice[cdnCard](batch, "cards")
	if err != nil {
		return nil, err
	}

	maxID := 0
	seen := make(map[int]struct{}, len(cards))
	for _, c := range cards {
		if c.ID <= 0 || c.ID > 1_000_000 {
			return nil, fmt.Errorf("cards contains invalid id %d", c.ID)
		}
		if _, exists := seen[c.ID]; exists {
			return nil, fmt.Errorf("cards contains duplicate id %d", c.ID)
		}
		seen[c.ID] = struct{}{}
		if c.ID > maxID {
			maxID = c.ID
		}
	}
	newCards := make([]CardEntry, maxID)
	for i := range newCards {
		newCards[i] = CardEntry{ID: i + 1, CharacterID: -1, CardNo: "000"}
	}
	for _, c := range cards {
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
		newCards[c.ID-1] = entry
	}
	return newCards, nil
}

func buildFestivals(events []EventEntry, cards []CardEntry) ([]FestivalEntry, error) {
	var newFestivals []FestivalEntry
	if len(events) == 0 || len(cards) == 0 {
		return nil, fmt.Errorf("cannot build festivals without events and cards")
	}

	specialCards := []int{}
	birthdayCards := []int{}
	fesIdx := 1
	birthdayIdx := 1

	// Find the smallest card id owned by any event; lm.Events[0] (smallest
	// KdyicrID with chapters) is not guaranteed to have any cards, so do not
	// blindly index [0][0].
	firstCardID := 0
	eventCardSet := make(map[int]struct{})
	for _, ev := range events {
		for _, c := range ev.Cards {
			if c < 1 || c > len(cards) {
				return nil, fmt.Errorf("event %d references invalid card id %d", ev.ID, c)
			}
			eventCardSet[c] = struct{}{}
			if firstCardID == 0 || c < firstCardID {
				firstCardID = c
			}
		}
	}
	if firstCardID == 0 {
		return newFestivals, nil
	}
	lastCardID := cards[len(cards)-1].ID

	i := firstCardID
	for i <= lastCardID {
		// Event ownership is a set membership relation. The upstream eventCards
		// table is not guaranteed to be grouped by event or card id.
		if _, isEventCard := eventCardSet[i]; isEventCard {
			i++
			continue
		}
		if i > lastCardID {
			break
		}

		if cards[i-1].Birthday {
			birthdayCards = append(birthdayCards, i)
			// Flush birthday group for specific characters
			if containsInt(birthdayCards, i) && containsInt([]int{7, 16, 14, 23}, cards[i-1].CharacterID) || len(birthdayCards) >= 26 {
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
			_, isEventCard := eventCardSet[i]
			if isEventCard || cards[i-1].Birthday {
				break
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

	return newFestivals, nil
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func buildMainStory(batch *tableBatch) ([]MainStoryEntry, error) {
	stories, err := fetchBatchSlice[cdnUnitStory](batch, "unitStories")
	if err != nil {
		return nil, err
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
	if len(newMainStory) == 0 {
		return nil, fmt.Errorf("unitStories produced no chapters")
	}
	return newMainStory, nil
}

func buildAreaTalks(batch *tableBatch, events []EventEntry) ([]AreaTalkEntry, map[int]cdnCharacter2D, error) {
	actions, err := fetchBatchSlice[cdnActionSet](batch, "actionSets")
	if err != nil {
		return nil, nil, err
	}
	char2ds, err := fetchBatchSlice[cdnCharacter2D](batch, "character2ds")
	if err != nil {
		return nil, nil, err
	}

	char2DLookup := make(map[int]cdnCharacter2D, len(char2ds))
	for _, c := range char2ds {
		if c.ID < 0 {
			return nil, nil, fmt.Errorf("character2ds contains invalid id %d", c.ID)
		}
		if _, exists := char2DLookup[c.ID]; exists {
			return nil, nil, fmt.Errorf("character2ds contains duplicate id %d", c.ID)
		}
		char2DLookup[c.ID] = c
	}

	var newAreaTalks []AreaTalkEntry
	actionCount := 0
	areatalkCount := 0
	specialAreatalkCount := 0
	addEventID := 1

	// Build event lookup: kdyicr_id → output id
	eventIDMap := make(map[int]int)
	for _, ev := range events {
		eventIDMap[ev.KdyicrID] = ev.ID
	}

	for _, action := range actions {
		if action.ID <= actionCount {
			return nil, nil, fmt.Errorf("actionSets ids are not strictly increasing at %d", action.ID)
		}
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
			if c, ok := char2DLookup[cid]; ok {
				charIDs = append(charIDs, c.CharacterID)
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
			Type:           actionType,
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

	if len(newAreaTalks) == 0 {
		return nil, nil, fmt.Errorf("actionSets produced no area talks")
	}
	return newAreaTalks, char2DLookup, nil
}

func buildSpecials(batch *tableBatch) ([]SpecialEntry, error) {
	stories, err := fetchBatchSlice[cdnSpecialStory](batch, "specialStories")
	if err != nil {
		return nil, err
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
	if len(newSpecials) == 0 {
		return nil, fmt.Errorf("specialStories produced no episodes")
	}
	return newSpecials, nil
}

func buildGreets(batch *tableBatch, previous []GreetEntry) ([]GreetEntry, error) {
	if _, err := fetchBatchSlice[cdnSystemLive2D](batch, "systemLive2ds"); err != nil {
		return nil, err
	}
	// Greet processing is extremely complex (hundreds of lines in Python).
	// Until it is ported, carry the current validated generation forward.
	return append([]GreetEntry(nil), previous...), nil
}

func buildCatalog(batch *tableBatch, previousGreets []GreetEntry, advance func(string)) (*catalogData, map[int]cdnCharacter2D, error) {
	catalog := &catalogData{}
	var err error
	advance("events")
	if catalog.Events, err = buildEvents(batch); err != nil {
		return nil, nil, fmt.Errorf("build events: %w", err)
	}
	advance("cards")
	if catalog.Cards, err = buildCards(batch); err != nil {
		return nil, nil, fmt.Errorf("build cards: %w", err)
	}
	advance("festivals")
	if catalog.Festivals, err = buildFestivals(catalog.Events, catalog.Cards); err != nil {
		return nil, nil, fmt.Errorf("build festivals: %w", err)
	}
	advance("mainStory")
	if catalog.MainStory, err = buildMainStory(batch); err != nil {
		return nil, nil, fmt.Errorf("build mainStory: %w", err)
	}
	advance("areaTalks")
	var char2ds map[int]cdnCharacter2D
	if catalog.AreaTalks, char2ds, err = buildAreaTalks(batch, catalog.Events); err != nil {
		return nil, nil, fmt.Errorf("build areaTalks: %w", err)
	}
	advance("specials")
	if catalog.Specials, err = buildSpecials(batch); err != nil {
		return nil, nil, fmt.Errorf("build specials: %w", err)
	}
	advance("greets")
	if catalog.Greets, err = buildGreets(batch, previousGreets); err != nil {
		return nil, nil, fmt.Errorf("build greets: %w", err)
	}
	if err := validateCatalog(catalog); err != nil {
		return nil, nil, err
	}
	return catalog, char2ds, nil
}

func validateCatalog(catalog *catalogData) error {
	if catalog == nil || len(catalog.Events) == 0 || len(catalog.Cards) == 0 ||
		len(catalog.MainStory) == 0 || len(catalog.AreaTalks) == 0 || len(catalog.Specials) == 0 {
		return fmt.Errorf("catalog validation failed: required output is empty")
	}
	for i, card := range catalog.Cards {
		if card.ID != i+1 {
			return fmt.Errorf("catalog validation failed: card slot %d has id %d", i+1, card.ID)
		}
	}
	return nil
}

func marshalCatalog(catalog *catalogData) (map[string][]byte, error) {
	values := map[string]interface{}{
		"events.json":    catalog.Events,
		"festivals.json": catalog.Festivals,
		"cards.json":     catalog.Cards,
		"mainStory.json": catalog.MainStory,
		"areatalks.json": catalog.AreaTalks,
		"greets.json":    catalog.Greets,
		"specials.json":  catalog.Specials,
	}
	files := make(map[string][]byte, len(values))
	for name, value := range values {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", name, err)
		}
		files[name] = data
	}
	return files, nil
}

func loadCatalogDataStrict(dir string) (*catalogData, error) {
	catalog := &catalogData{}
	files := []struct {
		name   string
		target interface{}
	}{
		{"events.json", &catalog.Events},
		{"festivals.json", &catalog.Festivals},
		{"cards.json", &catalog.Cards},
		{"mainStory.json", &catalog.MainStory},
		{"areatalks.json", &catalog.AreaTalks},
		{"greets.json", &catalog.Greets},
		{"specials.json", &catalog.Specials},
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file.name, err)
		}
		if err := json.Unmarshal(data, file.target); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file.name, err)
		}
	}
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

func readCatalogManifest(dir string) (catalogManifest, error) {
	var manifest catalogManifest
	data, err := os.ReadFile(filepath.Join(dir, catalogManifestFile))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Version != 1 || manifest.Generation == 0 || manifest.Dir == "" ||
		manifest.Dir != filepath.Base(manifest.Dir) || !strings.HasPrefix(manifest.Dir, "generation-") {
		return catalogManifest{}, fmt.Errorf("invalid catalog generation manifest")
	}
	if generation, ok := parseCatalogGenerationName(manifest.Dir); !ok || generation != manifest.Generation {
		return catalogManifest{}, fmt.Errorf("catalog manifest generation does not match directory")
	}
	return manifest, nil
}

func loadCatalogGeneration(dir string) (*catalogData, uint64, error) {
	manifest, err := readCatalogManifest(dir)
	if err == nil {
		catalog, loadErr := loadCatalogDataStrict(filepath.Join(dir, catalogGenerationDir, manifest.Dir))
		if loadErr == nil {
			return catalog, manifest.Generation, nil
		}
		err = loadErr
	}
	catalog, recovered, recoverErr := recoverCatalogGeneration(dir, manifest.Dir)
	if recoverErr != nil {
		return nil, 0, err
	}
	if writeErr := writeCatalogManifest(dir, recovered); writeErr != nil {
		log.Printf("[update] recovered catalog generation %d but could not repair manifest: %v", recovered.Generation, writeErr)
	} else {
		log.Printf("[update] recovered previous catalog generation %d", recovered.Generation)
	}
	return catalog, recovered.Generation, nil
}

func recoverCatalogGeneration(dir, exclude string) (*catalogData, catalogManifest, error) {
	root := filepath.Join(dir, catalogGenerationDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, catalogManifest{}, err
	}
	type candidate struct {
		name       string
		generation uint64
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == exclude {
			continue
		}
		generation, ok := parseCatalogGenerationName(entry.Name())
		if ok {
			candidates = append(candidates, candidate{name: entry.Name(), generation: generation})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].generation == candidates[j].generation {
			return candidates[i].name > candidates[j].name
		}
		return candidates[i].generation > candidates[j].generation
	})
	for _, candidate := range candidates {
		catalog, err := loadCatalogDataStrict(filepath.Join(root, candidate.name))
		if err == nil {
			manifest := catalogManifest{Version: 1, Generation: candidate.generation, Dir: candidate.name}
			return catalog, manifest, nil
		}
	}
	return nil, catalogManifest{}, fmt.Errorf("no valid retained catalog generation")
}

func parseCatalogGenerationName(name string) (uint64, bool) {
	rest, ok := strings.CutPrefix(name, "generation-")
	if !ok {
		return 0, false
	}
	parts := strings.Split(rest, "-")
	if len(parts) != 2 || len(parts[0]) != 20 {
		return 0, false
	}
	generation, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || generation == 0 {
		return 0, false
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return 0, false
	}
	return generation, true
}

func writeCatalogManifest(dir string, manifest catalogManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, catalogManifestFile), data, 0o644)
}

func commitCatalogManifest(dir, root, finalDir string, manifest catalogManifest, write func(string, catalogManifest) error) error {
	err := write(dir, manifest)
	if err == nil {
		return nil
	}
	// WriteFileAtomic can fail while syncing the parent after its rename already
	// committed. Never delete a generation that the visible manifest references.
	committed := fsutil.IsWriteCommitted(err)
	if !committed {
		if current, readErr := readCatalogManifest(dir); readErr == nil && current == manifest {
			committed = true
		}
	}
	if !committed {
		_ = os.RemoveAll(finalDir)
		_ = fsutil.SyncDir(root)
	}
	return fmt.Errorf("switch catalog generation: %w", err)
}

func persistCatalogGeneration(dir string, generation uint64, catalog *catalogData) (catalogManifest, error) {
	files, err := marshalCatalog(catalog)
	if err != nil {
		return catalogManifest{}, err
	}
	root := filepath.Join(dir, catalogGenerationDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return catalogManifest{}, err
	}
	if err := fsutil.SyncDir(dir); err != nil {
		return catalogManifest{}, fmt.Errorf("sync catalog directory: %w", err)
	}
	tmp, err := os.MkdirTemp(root, ".building-*")
	if err != nil {
		return catalogManifest{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()
	for name, data := range files {
		if err := fsutil.WriteFileAtomic(filepath.Join(tmp, name), data, 0o644); err != nil {
			return catalogManifest{}, fmt.Errorf("write %s: %w", name, err)
		}
	}
	if _, err := loadCatalogDataStrict(tmp); err != nil {
		return catalogManifest{}, fmt.Errorf("verify staged catalog: %w", err)
	}
	if err := fsutil.SyncDir(tmp); err != nil {
		return catalogManifest{}, fmt.Errorf("sync staged catalog: %w", err)
	}

	name := fmt.Sprintf("generation-%020d-%d", generation, time.Now().UnixNano())
	finalDir := filepath.Join(root, name)
	if err := os.Rename(tmp, finalDir); err != nil {
		return catalogManifest{}, fmt.Errorf("publish generation directory: %w", err)
	}
	cleanup = false
	if err := fsutil.SyncDir(root); err != nil {
		return catalogManifest{}, fmt.Errorf("sync generation directory: %w", err)
	}
	previous, _ := readCatalogManifest(dir)
	manifest := catalogManifest{Version: 1, Generation: generation, Dir: name}
	if err := commitCatalogManifest(dir, root, finalDir, manifest, writeCatalogManifest); err != nil {
		return catalogManifest{}, err
	}
	cleanupCatalogGenerations(root, name, previous.Dir)
	return manifest, nil
}

func cleanupCatalogGenerations(root string, keep ...string) {
	kept := make(map[string]struct{}, len(keep))
	for _, name := range keep {
		if name != "" {
			kept[name] = struct{}{}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "generation-") {
			continue
		}
		if _, ok := kept[entry.Name()]; !ok {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

func (lm *ListManager) publishCatalog(catalog *catalogData, generation uint64, char2ds map[int]cdnCharacter2D) {
	lm.mu.Lock()
	lm.Events = catalog.Events
	lm.Festivals = catalog.Festivals
	lm.Cards = catalog.Cards
	lm.MainStory = catalog.MainStory
	lm.AreaTalks = catalog.AreaTalks
	lm.Greets = catalog.Greets
	lm.Specials = catalog.Specials
	lm.AreaTalkByTime = nil
	lm.generation = generation
	// Keep derived voice lookup state in the same atomic generation swap. Readers
	// can never observe new Events/AreaTalks paired with old voice-clue indexes.
	lm.inferVoiceEventIDLocked()
	lm.mu.Unlock()

	char2dMu.Lock()
	char2dMap = char2ds
	char2dMu.Unlock()

	lm.refreshFlashbackAnalyzers()
}

func (pt *ProgressTracker) fail(message string) {
	pt.mu.Lock()
	pt.done = true
	pt.message = message
	pt.mu.Unlock()
}

// UpdateAllFromCDN downloads, builds, validates, and persists one complete
// generation. Any failure leaves both the current manifest and memory untouched.
func (lm *ListManager) UpdateAllFromCDN(dir string, pt *ProgressTracker) error {
	pt.SetTotal(9)
	pt.Advance("并发下载元数据...")
	batch, err := prefetchTables(probeMirrorTrusted())
	if err != nil {
		pt.fail("元数据下载失败: " + err.Error())
		return err
	}

	lm.mu.RLock()
	previousGreets := append([]GreetEntry(nil), lm.Greets...)
	generation := lm.generation + 1
	lm.mu.RUnlock()
	catalog, char2ds, err := buildCatalog(batch, previousGreets, func(name string) {
		pt.Advance("正在构建 " + name + "...")
	})
	if err != nil {
		pt.fail("元数据校验失败: " + err.Error())
		return err
	}
	pt.Advance("正在发布元数据...")
	manifest, err := persistCatalogGeneration(dir, generation, catalog)
	if err != nil {
		pt.fail("元数据发布失败: " + err.Error())
		return err
	}
	lm.publishCatalog(catalog, manifest.Generation, char2ds)
	pt.Done()
	log.Printf("[update] metadata refresh complete (generation %d)", manifest.Generation)
	return nil
}
