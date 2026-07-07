package service

import (
	"regexp"
	"strconv"
	"strings"

	"sekaitext/backend/internal/model"
)

// FlashbackAnalyzer analyzes voice IDs to identify flashback scenes.
type FlashbackAnalyzer struct {
	lm         *ListManager
	flashbackRe *regexp.Regexp
	areatalkRe  *regexp.Regexp
	mainstoryEpRe *regexp.Regexp
	cardrarityEpRe *regexp.Regexp
	clueDict    map[string]EventEntry
	mainstory   map[string]MainStoryEntry
	voiceMsToMainStory map[string]string
}

// NewFlashbackAnalyzer creates a new FlashbackAnalyzer.
func NewFlashbackAnalyzer(lm *ListManager) *FlashbackAnalyzer {
	fb := &FlashbackAnalyzer{
		lm:          lm,
		flashbackRe: regexp.MustCompile(`voice_(.+)_\d+[a-z]?_\d+(?:_?.*)?$`),
		areatalkRe:  regexp.MustCompile(`areatalk_(ev|wl)_(.+)_\d+$`),
		mainstoryEpRe: regexp.MustCompile(`(.*?)(\d+)$`),
		cardrarityEpRe: regexp.MustCompile(`(\d+)(.*?)$`),
		mainstory:   make(map[string]MainStoryEntry),
		voiceMsToMainStory: map[string]string{
			"band":   "light_sound",
			"idol":   "idol",
			"street": "street",
			"wonder": "theme_park",
			"night":  "school_refusal",
			"piapro": "piapro",
		},
	}
	fb.updateClues()
	return fb
}

// ResolveVoiceSourceURL maps a flashback voice ID to the source scenario it came
// from: returns the CDN URL, a cache filename, and the source scenario's asset
// name. ok=false when the event/episode can't be resolved (e.g. mainstory/card
// flashbacks, which aren't covered yet). source selects the CDN mirror.
func (fb *FlashbackAnalyzer) ResolveVoiceSourceURL(voiceID, source string) (url, fileName, assetName string, ok bool) {
	clue, ignore := fb.GetClueFromVoiceID(voiceID)
	if ignore || clue == "" {
		return "", "", "", false
	}
	// Strip a leading "sc_" the same way GetClueHints does.
	words := strings.Split(clue, "_")
	if len(words) > 0 && words[0] == "sc" {
		words = words[1:]
	}
	// Only event ("ev") flashbacks are resolved to a source file for now.
	if len(words) < 2 || words[0] != "ev" {
		return "", "", "", false
	}
	body := words[1:]
	// Trailing number = episode.
	ep := -1
	if v, err := strconv.Atoi(body[len(body)-1]); err == nil {
		ep = v
		body = body[:len(body)-1]
	}
	if ep < 0 {
		return "", "", "", false
	}
	clueKey := strings.Join(body, "_")
	ev, found := fb.clueDict[clueKey]
	if !found {
		return "", "", "", false
	}
	ep += choffsetValue(ev.InferredVoiceIDs)
	if ep < 1 || ep > len(ev.Chapters) {
		return "", "", "", false
	}
	asset := ev.Chapters[ep-1].AssetName
	base := fb.lm.baseUrls["haruki"]
	prefix := "ondemand/"
	if source == "sekai.best" || source == "moesekai-jp" || source == "moesekai-cn" {
		prefix = ""
		switch source {
		case "sekai.best":
			base = fb.lm.baseUrls["best"]
		case "moesekai-jp":
			base = fb.lm.baseUrls["moesekai-jp"]
		case "moesekai-cn":
			base = fb.lm.baseUrls["moesekai-cn"]
		}
	} else if source == "unipjsk" {
		base = fb.lm.baseUrls["uni"]
	}
	url = base + prefix + "event_story/" + ev.Name + "/scenario/" + asset + ".json"
	return url, asset + ".json", asset, true
}

func (fb *FlashbackAnalyzer) updateClues() {
	for _, ms := range fb.lm.MainStory {
		fb.mainstory[ms.Unit] = ms
	}
	// Populate each event's InferredVoiceIDs (voice-prefix -> event) from the
	// area-talk scenario IDs BEFORE building the clue dict. Without this call
	// InferredVoiceIDs stays nil for every event, so BuildVoiceIDClues returns
	// an empty map and every event flashback renders "未知活动".
	fb.lm.InferVoiceEventID()
	fb.clueDict = fb.lm.BuildVoiceIDClues()
}

// choffsetValue reads the "choffset" chapter offset out of an event's
// InferredVoiceIDs map. InferVoiceEventID stores it as a native Go int (event 9
// / shuffle_03 uses 1), but the same map may arrive as float64/int64 if it was
// ever round-tripped through JSON. Accept every numeric shape so the offset is
// actually applied — a plain chOff.(float64) assertion silently fails on the
// int that lives in memory at runtime.
func choffsetValue(m map[string]interface{}) int {
	switch x := m["choffset"].(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// GetClueFromVoiceID analyzes a voice ID string and returns a clue.
// Returns: ("", false) = no idea, ("", true) = ignore, (clue, false) = scenario clue.
func (fb *FlashbackAnalyzer) GetClueFromVoiceID(voiceID string) (string, bool) {
	if strings.Contains(voiceID, "partvoice") {
		return "", true
	}
	match := fb.flashbackRe.FindStringSubmatch(voiceID)
	if match != nil {
		return match[1], false
	}
	return "", false
}

// GetClueHints returns human-readable hints for a clue.
func (fb *FlashbackAnalyzer) GetClueHints(clue, lang string) []string {
	if lang == "" {
		lang = "zh-cn"
	}
	words := strings.Split(clue, "_")
	var hints []string

	// Skip 'sc'
	firstIdx := 0
	if len(words) > 0 && words[0] == "sc" {
		firstIdx = 1
	}

	if firstIdx >= len(words) {
		return hints
	}

	first := words[firstIdx]

	// Check if it's a card label
	if first == "ev" && len(words) > 0 {
		lastWord := words[len(words)-1]
		if strings.HasSuffix(lastWord, "a") || strings.HasSuffix(lastWord, "b") {
			first = "card"
		}
	}

	switch first {
	case "ev":
		return fb.getEventHints(words[firstIdx+1:])

	case "ms", "op", "unit":
		// Strip the leading "ms"/"op"/"unit" keyword (like the "ev" branch does),
		// so getMainStoryHints sees the team/episode body rather than the keyword.
		return fb.getMainStoryHints(words[firstIdx+1:], first)

	case "card":
		return fb.getCardHints(words[firstIdx+1:])

	default:
		hints = append(hints, "闪回：未知来源")
		hints = append(hints, "原始匹配: "+clue)
	}

	return hints
}

func (fb *FlashbackAnalyzer) getEventHints(words []string) []string {
	var hints []string
	ep := -1
	if len(words) > 0 {
		if v, err := strconv.Atoi(words[len(words)-1]); err == nil {
			ep = v
			words = words[:len(words)-1]
		}
	}

	clueKey := strings.Join(words, "_")
	eventInfo, ok := fb.clueDict[clueKey]
	if !ok {
		hints = append(hints, "未知活动")
		hints = append(hints, "原始匹配: "+clueKey)
		return hints
	}

	chOffset := choffsetValue(eventInfo.InferredVoiceIDs)

	if ep >= 0 {
		ep += chOffset
	}

	// Only append the "id-episode" label when we actually parsed an episode
	// number; otherwise ep is still -1 and padZero(-1) would emit a bogus
	// "id-0-1" label.
	if ep >= 0 {
		hints = append(hints, strconv.Itoa(eventInfo.ID)+"-"+padZero(ep))
	} else {
		hints = append(hints, strconv.Itoa(eventInfo.ID))
	}
	hints = append(hints, eventInfo.Title)
	if ep > 0 && ep <= len(eventInfo.Chapters) {
		hints = append(hints, eventInfo.Chapters[ep-1].Title)
	} else {
		hints = append(hints, "未知章节")
	}
	return hints
}

func (fb *FlashbackAnalyzer) getMainStoryHints(words []string, first string) []string {
	var hints []string
	if len(words) == 0 {
		return append(hints, "未知主线剧情")
	}

	// The clue body splits the team and episode into separate underscore
	// segments (e.g. ["night","05",...]), but mainstoryEpRe expects them in a
	// single token like "night05". When words[0] is an alphabetic team name,
	// rejoin it with the following numeric episode segment so the regex (and
	// the voiceMsToMainStory lookup below) resolve the real chapter.
	w := words[0]
	if _, err := strconv.Atoi(words[0]); err != nil && len(words) > 1 {
		if _, err := strconv.Atoi(words[1]); err == nil {
			w = words[0] + words[1]
		}
	}
	match := fb.mainstoryEpRe.FindStringSubmatch(w)
	if match == nil {
		hints = append(hints, "未知主线剧情")
		hints = append(hints, "原始匹配: "+strings.Join(words, "_"))
		return hints
	}

	team := match[1]
	ep, _ := strconv.Atoi(match[2])

	if unitKey, ok := fb.voiceMsToMainStory[team]; ok {
		unitName := model.UnitDict[unitKey]
		hints = append(hints, unitName+" 主线剧情 - "+padZero(ep)+"话")

		if ms, ok := fb.mainstory[unitKey]; ok {
			if first == "unit" {
				epHints := []string{"ln", "mmj", "vbs", "ws", "25时"}
				epIdx := (ep - 1) / 4
				epSub := ((ep - 1) % 4) + 1
				if epIdx >= 0 && epIdx < len(epHints) && ep-1 >= 0 && ep-1 < len(ms.Chapters) {
					hints = append(hints, "("+epHints[epIdx]+"-"+strconv.Itoa(epSub)+") "+ms.Chapters[ep-1].Title)
				} else {
					hints = append(hints, "未知章节")
				}
			} else {
				// ep is 1-based here (same as the "话" label and the unit
				// branch's ms.Chapters[ep-1]); index with ep-1 so non-unit
				// main-story flashbacks don't take the next chapter's title and
				// the final episode (ep==len) resolves instead of "未知章节".
				if ep >= 1 && ep <= len(ms.Chapters) {
					hints = append(hints, ms.Chapters[ep-1].Title)
				} else {
					hints = append(hints, "未知章节")
				}
			}
		}
	} else {
		hints = append(hints, "未知主线剧情")
		hints = append(hints, "原始匹配: "+strings.Join(words, "_"))
	}
	return hints
}

func (fb *FlashbackAnalyzer) getCardHints(words []string) []string {
	var hints []string
	if len(words) < 2 {
		hints = append(hints, "未知卡面")
		hints = append(hints, "原始匹配: "+strings.Join(words, "_"))
		return hints
	}

	// Last word has star rating and episode
	starsep := words[len(words)-1]
	charIDStr := words[len(words)-2]

	match := fb.cardrarityEpRe.FindStringSubmatch(starsep)
	stars := "?"
	ep := "未知章节"
	if match != nil {
		stars = match[1]
		epPart := match[2]
		if epPart == "a" {
			ep = "前篇"
		} else if epPart == "b" {
			ep = "后篇"
		}
	}

	charID, _ := strconv.Atoi(charIDStr)
	charName := charIDStr
	if charID >= 1 && charID <= len(model.CharacterDict) {
		charName = model.CharacterDict[charID-1].NameJ
	}

	eventID := strings.Join(words[:len(words)-2], "_")

	eventHints := "卡面来自 " + eventID
	if eventID == "" {
		eventHints = "初期卡面"
	} else if ev, ok := fb.clueDict[eventID]; ok {
		eventHints = strconv.Itoa(ev.ID) + " - " + ev.Title
	}

	hints = append(hints, eventHints)
	hints = append(hints, charName+" ☆"+stars+" "+ep)
	return hints
}
