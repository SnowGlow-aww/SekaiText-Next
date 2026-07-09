package service

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"sekaitext/backend/internal/model"
)

// UnityStoryData represents the Unity JSON structure for a story scenario.
type UnityStoryData struct {
	ScenarioID        string                   `json:"ScenarioId"`
	Snippets          []SnippetData            `json:"Snippets"`
	TalkData          []TalkData               `json:"TalkData"`
	SpecialEffectData []SpecialEffectData      `json:"SpecialEffectData"`
	AppearCharacters  []AppearCharacter        `json:"AppearCharacters"`
}

// AppearCharacter maps a Live2D model instance id to its costume name
// (e.g. 296 -> "v2_19ena_casual"); the costume embeds the romaji character.
type AppearCharacter struct {
	Character2dId int    `json:"Character2dId"`
	CostumeType   string `json:"CostumeType"`
}

// SnippetData represents an action snippet in the story.
type SnippetData struct {
	Action          int `json:"Action"`
	ReferenceIndex  int `json:"ReferenceIndex"`
}

// TalkData represents a dialogue entry.
type TalkData struct {
	WindowDisplayName     string    `json:"WindowDisplayName"`
	Body                  string    `json:"Body"`
	Voices                []VoiceData `json:"Voices"`
	WhenFinishCloseWindow int       `json:"WhenFinishCloseWindow"`
}

// VoiceData represents a voice clip reference.
type VoiceData struct {
	VoiceID       string  `json:"VoiceId"`
	Volume        float64 `json:"Volume"`
	Character2dId int     `json:"Character2dId"`
}

// SpecialEffectData represents a special effect (scene/option text).
type SpecialEffectData struct {
	EffectType int    `json:"EffectType"`
	StringVal  string `json:"StringVal"`
}

// JsonLoaderService parses Unity story JSON into SourceTalk entries.
type JsonLoaderService struct {
	fb *FlashbackAnalyzer

	// Source-line locator (optional): when set, checkFlashback resolves each
	// flashback clue to its 1-based line in the source scenario by downloading
	// and re-parsing that scenario. lineCache memoizes voiceID -> line so the
	// same flashback voice isn't re-fetched.
	dl        *Downloader
	dataDir   string
	source    string
	cacheMu   sync.Mutex // guards lineCache against concurrent story-load requests
	lineCache map[string]int
}

// NewJsonLoaderService creates a new JsonLoaderService.
func NewJsonLoaderService(fb *FlashbackAnalyzer) *JsonLoaderService {
	return &JsonLoaderService{fb: fb}
}

// SetSourceLocator enables flashback source-line lookup. source selects the CDN
// mirror (defaults to "haruki" when empty).
func (j *JsonLoaderService) SetSourceLocator(dl *Downloader, dataDir string) {
	j.dl = dl
	j.dataDir = dataDir
	j.source = "haruki"
	j.lineCache = make(map[string]int)
}

// ParseFile loads and parses a Unity story JSON file.
func (j *JsonLoaderService) ParseFile(path string) (*model.LoadResponse, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read story file: %w", err)
	}

	var story UnityStoryData
	if err := json.Unmarshal(data, &story); err != nil {
		return nil, fmt.Errorf("failed to parse story JSON: %w", err)
	}

	return j.parse(&story, false), nil
}

// ParseBytes parses Unity story JSON from raw bytes.
func (j *JsonLoaderService) ParseBytes(data []byte) (*model.LoadResponse, error) {
	var story UnityStoryData
	if err := json.Unmarshal(data, &story); err != nil {
		return nil, fmt.Errorf("failed to parse story JSON: %w", err)
	}
	return j.parse(&story, false), nil
}

// parse converts a parsed Unity story into a LoadResponse. locating is true only
// when re-parsing a source scenario solely to count lines; it is request-local
// (threaded as a parameter, not shared state) so concurrent loads stay isolated.
func (j *JsonLoaderService) parse(story *UnityStoryData, locating bool) *model.LoadResponse {
	var talks []model.SourceTalk
	talkIndex := make(map[int]*TalkData)
	for i := range story.TalkData {
		talkIndex[i] = &story.TalkData[i]
	}

	// Live2D model instance id -> CharacterDict index (-1 for mob/sub models).
	// The same id repeats once per costume of the same character; first wins.
	c2dChar := make(map[int]int, len(story.AppearCharacters))
	for _, a := range story.AppearCharacters {
		if _, ok := c2dChar[a.Character2dId]; !ok {
			c2dChar[a.Character2dId] = charIndexForCostume(a.CostumeType)
		}
	}

	for _, snippet := range story.Snippets {
		switch snippet.Action {
		case 1: // TalkData
			if snippet.ReferenceIndex < 0 || snippet.ReferenceIndex >= len(story.TalkData) {
				continue
			}
			talkdata := story.TalkData[snippet.ReferenceIndex]
			speaker := splitSpeaker(talkdata.WindowDisplayName)
			text := talkdata.Body

			var voices []string
			var volume []int
			chara2d := 0
			for i, v := range talkdata.Voices {
				voices = append(voices, v.VoiceID)
				volume = append(volume, int(v.Volume))
				if i == 0 {
					chara2d = v.Character2dId
				}
			}

			charIdx := charIndexForSpeaker(speaker, chara2d, c2dChar)

			talk := model.SourceTalk{
				Speaker:  speaker,
				Text:     text,
				Voices:   voices,
				Volume:   volume,
				CharIdx:  charIdx,
				Chara2d:  chara2d,
			}

			talks = append(talks, talk)

			// Window close separator
			if talkdata.WhenFinishCloseWindow != 0 {
				talks = append(talks, model.SourceTalk{
					Speaker: "",
					Text:    "",
				})
			}

		case 6: // SpecialEffectData
			if snippet.ReferenceIndex < 0 || snippet.ReferenceIndex >= len(story.SpecialEffectData) {
				continue
			}
			effect := story.SpecialEffectData[snippet.ReferenceIndex]

			// EffectType 8=location, 18=upper-left scene, 23=choice
			if effect.EffectType == 8 || effect.EffectType == 18 || effect.EffectType == 23 {
				speaker := "场景"
				if effect.EffectType == 18 {
					speaker = "左上场景"
				} else if effect.EffectType == 23 {
					speaker = "选项"
				}

				talks = append(talks, model.SourceTalk{
					Speaker: speaker,
					Text:    effect.StringVal,
				})

				// Separator after effect
				talks = append(talks, model.SourceTalk{
					Speaker: "",
					Text:    "",
				})
			}
		}
	}

	// Remove trailing empty
	if len(talks) > 0 && talks[len(talks)-1].Speaker == "" {
		talks = talks[:len(talks)-1]
	}

	// Flashback analysis. Skipped while locating a flashback's source line: that
	// path re-parses a source scenario only to count lines, and running flashback
	// analysis there would recursively download yet more scenarios.
	if !locating {
		talks = j.checkFlashback(talks)
	}

	return &model.LoadResponse{
		ScenarioID:  story.ScenarioID,
		SourceTalks: talks,
	}
}

// checkFlashback analyzes voice IDs for flashback clues.
func (j *JsonLoaderService) checkFlashback(talks []model.SourceTalk) []model.SourceTalk {
	if j.fb == nil {
		return talks
	}

	for i, talk := range talks {
		if len(talk.Voices) == 0 {
			continue
		}
		// Map clue -> a representative voiceID that produced it, so we can later
		// locate that voice's physical line in its source scenario.
		clueVoice := make(map[string]string)
		clueOrder := make([]string, 0)
		for _, voiceID := range talk.Voices {
			clue, ignore := j.fb.GetClueFromVoiceID(voiceID)
			if ignore || clue == "" {
				continue
			}
			if _, seen := clueVoice[clue]; !seen {
				clueVoice[clue] = voiceID
				clueOrder = append(clueOrder, clue)
			}
		}
		for _, clue := range clueOrder {
			talks[i].Clues = append(talks[i].Clues, clue)
			talks[i].FlashbackLines = append(talks[i].FlashbackLines, j.locateVoiceLine(clueVoice[clue]))
		}
	}

	return talks
}

// locateVoiceLine returns the 1-based physical line where voiceID appears in its
// source scenario, downloading + parsing that scenario as needed. Returns 0 when
// the locator isn't configured, the source can't be resolved, or anything fails
// (so flashback hints degrade gracefully to "no line number").
func (j *JsonLoaderService) locateVoiceLine(voiceID string) int {
	if j.dl == nil || j.fb == nil {
		return 0
	}
	// lineCache is shared across concurrent story-load goroutines, so guard the
	// read and the write. The lock is not held across locateVoiceLineUncached
	// (which downloads + re-parses) to avoid serializing unrelated lookups.
	j.cacheMu.Lock()
	line, ok := j.lineCache[voiceID]
	j.cacheMu.Unlock()
	if ok {
		return line
	}
	line = j.locateVoiceLineUncached(voiceID)
	j.cacheMu.Lock()
	j.lineCache[voiceID] = line
	j.cacheMu.Unlock()
	return line
}

func (j *JsonLoaderService) locateVoiceLineUncached(voiceID string) int {
	url, fileName, _, ok := j.fb.ResolveVoiceSourceURL(voiceID, j.source)
	if !ok {
		return 0
	}
	path, err := j.dl.DownloadJSON(url, fileName)
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var story UnityStoryData
	if err := json.Unmarshal(data, &story); err != nil {
		return 0
	}
	// Reuse the real parser so line counting matches exactly what the translator
	// sees when loading this source scenario. locating=true suppresses recursive
	// flashback analysis on this source-only re-parse.
	resp := j.parse(&story, true)
	target := normalizeVoiceID(voiceID)
	for idx, t := range resp.SourceTalks {
		for _, v := range t.Voices {
			if v == voiceID || normalizeVoiceID(v) == target {
				return idx + 1
			}
		}
	}
	return 0
}

// normalizeVoiceID makes a flashback voice ID comparable to its source-scenario
// counterpart. A line referenced as a flashback in one episode can carry a
// variant suffix on its line-number segment (e.g. the source has
// "..._03_03_34_9999" while the flashback reuse is "..._03_03_34b_9999"), and
// the trailing segment is a per-clip character/voice id that also differs. We
// drop that trailing segment and strip any trailing letters from the (now last)
// line-number segment, so both forms reduce to "...wl_shuffle_03_03_34".
func normalizeVoiceID(v string) string {
	parts := strings.Split(v, "_")
	if len(parts) < 2 {
		return v
	}
	// Drop the trailing character/voice id segment.
	parts = parts[:len(parts)-1]
	// Strip trailing letters from the line-number segment (e.g. "34b" -> "34").
	last := parts[len(parts)-1]
	last = strings.TrimRight(last, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	parts[len(parts)-1] = last
	return strings.Join(parts, "_")
}

// splitSpeaker extracts the speaker name from WindowDisplayName (strip _ suffix).
func splitSpeaker(displayName string) string {
	parts := strings.SplitN(displayName, "_", 2)
	return parts[0]
}

// charIndexForSpeaker resolves a talk's display speaker to a CharacterDict index
// for the avatar icon. Story speakers are often decorated ("絵名の声",
// "奏のメッセージ", role-play names like "子供の神使"), so enumeration by name
// alone can't cover them. Resolution order:
//
//  1. "？？？"-style speakers stay -1: the game hides the identity on purpose.
//  2. Exact Japanese name.
//  3. The speaking Live2D model (Voices[0].Character2dId → costume): covers any
//     decoration and role-play names without a suffix list, and a mob/sub model
//     correctly resolves to -1 instead of leaking onto a look-alike name
//     ("穂波の母" must not get 穂波's icon). Only known model ids short-circuit;
//     group-voice pseudo-ids (900000) fall through.
//  4. Multi-speaker windows ("司・えむ・寧々・類", group voice): first part that
//     resolves by name.
//  5. Voiceless lines ("奏のメッセージ" carries no Voices at all): a
//     "{NameJ}の…" prefix names the character generically.
func charIndexForSpeaker(speaker string, chara2d int, c2dChar map[int]int) int {
	if speaker == "" || strings.Trim(speaker, "？?") == "" {
		return -1
	}
	if c, ok := model.FindCharacterByJapaneseName(speaker); ok {
		return c.Index
	}
	if idx, ok := c2dChar[chara2d]; ok {
		return idx
	}
	for _, sep := range []string{"・", "＆"} {
		if !strings.Contains(speaker, sep) {
			continue
		}
		for _, part := range strings.Split(speaker, sep) {
			if c, ok := model.FindCharacterByJapaneseName(part); ok {
				return c.Index
			}
			if idx := charIndexForNamePrefix(part); idx >= 0 {
				return idx
			}
		}
	}
	return charIndexForNamePrefix(speaker)
}

// charIndexForNamePrefix matches "{NameJ}の…" decorations ("絵名の声",
// "奏のメッセージ", "みのりのスマホ", …) without enumerating suffixes.
func charIndexForNamePrefix(speaker string) int {
	for _, c := range model.CharacterDict {
		if strings.HasPrefix(speaker, c.NameJ+"の") {
			return c.Index
		}
	}
	return -1
}

// costumeCharRe extracts the romaji character token from a costume name:
// "v2_19ena_casual" -> "ena". Mob/sub costumes ("mob001", "sub_sakaki_black")
// have no NN+romaji segment and don't match.
var costumeCharRe = regexp.MustCompile(`(?:^|_)\d{2}([a-z]+)`)

// charIndexForCostume resolves a Live2D costume name to a CharacterDict index.
func charIndexForCostume(costume string) int {
	m := costumeCharRe.FindStringSubmatch(costume)
	if m == nil {
		return -1
	}
	for _, c := range model.CharacterDict {
		if c.Name == m[1] {
			return c.Index
		}
	}
	return -1
}
