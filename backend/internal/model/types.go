package model

import "sync"

// SourceTalk represents a source text entry parsed from Unity JSON.
type SourceTalk struct {
	Speaker string   `json:"speaker"`
	Text    string   `json:"text"`
	Voices  []string `json:"voices,omitempty"`
	Volume  []int    `json:"volume,omitempty"`
	CharIdx int      `json:"charIndex"`
	// Chara2d is the speaking character's Character2dId for the FIRST voice clip
	// of this line (the one the player plays). Resolves the partvoice_ bundle
	// subdir for card stories; 0 when the line has no voice.
	Chara2d int      `json:"chara2d,omitempty"`
	Clues   []string `json:"clues,omitempty"`
	// FlashbackLines is parallel to Clues: FlashbackLines[k] is the 1-based
	// physical line where Clues[k]'s flashback sentence appears in its source
	// scenario file (0 = could not locate / not resolved).
	FlashbackLines []int `json:"flashbackLines,omitempty"`
}

// DstTalk represents a translation entry in the editor.
type DstTalk struct {
	Idx       int        `json:"idx"`
	Speaker   string     `json:"speaker"`
	Text      string     `json:"text"`
	Start     bool       `json:"start"`
	End       bool       `json:"end"`
	Checked   bool       `json:"checked"`
	Save      bool       `json:"save"`
	Message   string     `json:"message,omitempty"`
	DstIdx    int        `json:"dstidx"`
	ReferID   int        `json:"referid,omitempty"`
	Proofread *bool      `json:"proofread,omitempty"`
	CheckMode bool       `json:"checkmode,omitempty"`
	DiffParts []DiffPart `json:"diff,omitempty"`
	// Baseline holds the comparison source for this row: the original
	// translation (校对 mode) or the proofread draft (合意 mode). Diff is
	// computed against it. Empty in 翻译 mode.
	Baseline string `json:"baseline,omitempty"`
}

// EditorMode constants
const (
	ModeTranslate = 0
	ModeProofread = 1
	ModeCheck     = 2
)

// TalkColor represents row background color in the editor.
type TalkColor int

const (
	ColorWhite TalkColor = iota
	ColorRed
	ColorYellow
	ColorGreen
	ColorBlue
)

// StoryType defines the type of story.
type StoryType string

const (
	StoryTypeEvent     StoryType = "event"
	StoryTypeMainStory StoryType = "mainstory"
	StoryTypeCard      StoryType = "card"
	StoryTypeFestival  StoryType = "festival"
	StoryTypeAreaTalk  StoryType = "areatalk"
	StoryTypeGreet     StoryType = "greet"
	StoryTypeSpecial   StoryType = "special"
)

// StorySort represents a sorting/filter option for stories.
type StorySort struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// StoryIndex represents a story index entry (episode/book).
type StoryIndex struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	Chapters []int  `json:"chapters,omitempty"`
}

// StoryChapter represents a chapter within a story.
type StoryChapter struct {
	Number int    `json:"number"`
	Label  string `json:"label"`
}

// JsonPathResult contains CDN URL and filename for loading a story.
type JsonPathResult struct {
	URL          string `json:"url"`
	FileName     string `json:"fileName"`
	SaveTitle    string `json:"saveTitle"`
	ChapterTitle string `json:"chapterTitle"`
}

// Settings represents application settings.
type Settings struct {
	FontSize       int  `json:"fontSize"`
	UIFontSize     int  `json:"uiFontSize"`
	SaveLineBreakN bool `json:"saveN"`
	// Deprecated: retained only so existing settings JSON remains compatible.
	// Network clients always verify TLS regardless of this value.
	DisableSSL                  bool   `json:"disableSSL"`
	JsonDownloadDir             string `json:"jsonDownloadDir,omitempty"`
	SaveBaseDir                 string `json:"saveBaseDir,omitempty"`
	DebugEnabled                bool   `json:"debugEnabled"`
	IndexOrder                  string `json:"indexOrder"`
	UndoDepth                   int    `json:"undoDepth"`
	KeepHighlightWhenCompareOff bool   `json:"keepHighlightWhenCompareOff"`

	// Shortcuts maps a shortcut action id to a combo string (e.g. "mod+o").
	// Empty/absent entries fall back to the frontend registry defaults.
	Shortcuts map[string]string `json:"shortcuts,omitempty"`

	// HideAgreementImportHint suppresses the "请先导入翻译稿再导入校对稿" dialog
	// shown when entering 合意 mode.
	HideAgreementImportHint bool `json:"hideAgreementImportHint,omitempty"`

	LastStoryType  string `json:"lastStoryType,omitempty"`
	LastStorySort  string `json:"lastStorySort,omitempty"`
	LastStoryIndex string `json:"lastStoryIndex,omitempty"`
	LastChapter    int    `json:"lastChapter,omitempty"`
	LastDataSource string `json:"lastDataSource,omitempty"`

	// Live2DPosition places the Live2D dock relative to the editor:
	// "top" | "right" | "bottom" | "window". Empty falls back to "right".
	Live2DPosition string `json:"live2dPosition,omitempty"`

	// PluginMarketURL overrides the plugin marketplace index URL. Empty falls
	// back to the built-in default (service.DefaultMarketURL).
	PluginMarketURL string `json:"pluginMarketUrl,omitempty"`

	// AppUpdateURL overrides the app-release manifest URL. Empty falls back to the
	// built-in default (service.DefaultAppUpdateURL).
	AppUpdateURL string `json:"appUpdateUrl,omitempty"`

	// DownloadMirror picks the source for app updates & plugin-market downloads:
	// ""/"cdn" = 国内边缘 CDN 加速（默认）, "github" = GitHub 直连。所选源优先，
	// 另一侧自动兜底（见 service.routeDownloadURL）。
	DownloadMirror string `json:"downloadMirror,omitempty"`

	// SeenTours lists onboarding-tour ids the user has completed or skipped
	// ("app-welcome", "plugin:live2d", "whatsnew:5.3.0", …) so each shows once.
	SeenTours []string `json:"seenTours,omitempty"`

	// LastSeenVersion is the app version at last launch; a mismatch on boot
	// triggers the one-time what's-new tour for the new version.
	LastSeenVersion string `json:"lastSeenVersion,omitempty"`
}

// DefaultSettings returns sensible defaults.
func DefaultSettings() Settings {
	// JsonDownloadDir and SaveBaseDir are intentionally left empty: a hardcoded
	// default would be developer-specific and invalid on the user's machine (and
	// on Windows). They are resolved per-user at runtime — the JSON download
	// handler falls back to {DataDir}/json (absolute, under the app data dir)
	// when empty, and the save flow falls back to the OS save-dialog default
	// location.
	return Settings{
		FontSize:                    18,
		UIFontSize:                  16,
		SaveLineBreakN:              true,
		DisableSSL:                  false,
		DebugEnabled:                false,
		IndexOrder:                  "asc",
		UndoDepth:                   20,
		KeepHighlightWhenCompareOff: true,
		Live2DPosition:              "right",
	}
}

// UpdateProgress tracks metadata refresh progress.
type UpdateProgress struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Message string `json:"message,omitempty"`
	Done    bool   `json:"done"`
}

// LoadRequest is the request body for loading a story JSON.
type LoadRequest struct {
	StoryType string `json:"storyType" validate:"required"`
	Sort      string `json:"sort"`
	Index     string `json:"index"`
	Chapter   int    `json:"chapter"`
	Source    string `json:"source"`
}

// LoadResponse contains source talks after loading.
type LoadResponse struct {
	ScenarioID   string       `json:"scenarioId"`
	SourceTalks  []SourceTalk `json:"sourceTalks"`
	SaveTitle    string       `json:"saveTitle"`
	ChapterTitle string       `json:"chapterTitle"`
	// 索引下拉框的完整标签（活动为 "<ID> <标题>"），文稿目录用它命名。前端各
	// 载入路径（导航/打开txt/恢复）都以这里为准，避免列表未加载时兜底成裸 ID。
	IndexLabel string `json:"indexLabel"`
}

// TranslationCreateRequest creates a new translation from source talks.
type TranslationCreateRequest struct {
	SourceTalks []SourceTalk `json:"sourceTalks" validate:"required"`
	JP          bool         `json:"jp"`
}

// TranslationLoadRequest loads a translation file.
type TranslationLoadRequest struct {
	FilePath string `json:"filePath" validate:"required"`
}

// TranslationSaveRequest saves a translation file.
type TranslationSaveRequest struct {
	FilePath string        `json:"filePath" validate:"required"`
	Talks    []DstTalk     `json:"talks" validate:"required"`
	SaveN    bool          `json:"saveN"`
	Meta     *SaveMetadata `json:"meta,omitempty"`
}

// EditorChangeTextRequest edits text in a talk entry.
type EditorChangeTextRequest struct {
	Row        int       `json:"row"`
	Text       string    `json:"text"`
	EditorMode int       `json:"editorMode"`
	Talks      []DstTalk `json:"talks"`
	DstTalks   []DstTalk `json:"dstTalks"`
	ReferTalks []DstTalk `json:"referTalks"`
}

// EditorAddLineRequest adds a sub-line.
type EditorAddLineRequest struct {
	Row         int       `json:"row"`
	Talks       []DstTalk `json:"talks"`
	DstTalks    []DstTalk `json:"dstTalks"`
	IsProofread bool      `json:"isProofreading"`
}

// EditorRemoveLineRequest removes a sub-line.
type EditorRemoveLineRequest struct {
	Row      int       `json:"row"`
	Talks    []DstTalk `json:"talks"`
	DstTalks []DstTalk `json:"dstTalks"`
}

// EditorReplaceBracketsRequest replaces brackets in all sub-lines of a source row.
type EditorReplaceBracketsRequest struct {
	Row      int       `json:"row"`
	Brackets string    `json:"brackets"`
	Talks    []DstTalk `json:"talks"`
	DstTalks []DstTalk `json:"dstTalks"`
}

// CheckLinesRequest aligns and validates loaded talks.
type CheckLinesRequest struct {
	SourceTalks []SourceTalk `json:"sourceTalks" validate:"required"`
	LoadedTalks []DstTalk    `json:"loadedTalks" validate:"required"`
}

// CheckTextRequest validates text content.
type CheckTextRequest struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// CheckTextResponse returns validation results.
type CheckTextResponse struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
	Message string `json:"message,omitempty"`
}

// CompareRequest compares referTalks and checkTalks.
type CompareRequest struct {
	ReferTalks []DstTalk `json:"referTalks"`
	CheckTalks []DstTalk `json:"checkTalks"`
	EditorMode int       `json:"editorMode"`
}

// SpeakerCountRequest counts lines per speaker.
type SpeakerCountRequest struct {
	Talks       []DstTalk    `json:"talks"`
	SourceTalks []SourceTalk `json:"sourceTalks"`
}

// SpeakerCountResponse contains per-speaker counts.
type SpeakerCountResponse struct {
	Speakers []SpeakerEntry `json:"speakers"`
}

// SpeakerEntry represents a single speaker's count.
type SpeakerEntry struct {
	Japanese string `json:"japanese"`
	Chinese  string `json:"chinese"`
	Count    int    `json:"count"`
}

// FlashbackAnalyzeRequest analyzes flashbacks for source talks.
type FlashbackAnalyzeRequest struct {
	SourceTalks []SourceTalk `json:"sourceTalks"`
}

// FlashbackAnalyzeResponse contains flashback analysis results.
type FlashbackAnalyzeResponse struct {
	SourceTalks []SourceTalk `json:"sourceTalks"`
}

// VoiceURLResponse contains the voice playback URL.
type VoiceURLResponse struct {
	URL string `json:"url"`
}

// CharacterInfo from characterDict.
type CharacterInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	NameJ string `json:"nameJ"`
	NameC string `json:"nameC"`
}

// UnitInfo from unitDict.
type UnitInfo struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// JsonDownloadRequest downloads a story JSON to a directory.
type JsonDownloadRequest struct {
	StoryType string `json:"storyType"`
	Sort      string `json:"sort"`
	Index     string `json:"index"`
	Chapter   int    `json:"chapter"`
	Source    string `json:"source"`
	OutputDir string `json:"outputDir"`
}

// DownloadTaskProgress tracks progress of an async download.
// Mu guards the mutable fields below, which are written by the download
// goroutine and read concurrently by the progress HTTP handler.
type DownloadTaskProgress struct {
	Mu                sync.Mutex `json:"-"`
	TaskID            string     `json:"taskId"`
	Status            string     `json:"status"` // downloading, done, error
	Read              int64      `json:"read"`
	Total             int64      `json:"total"`
	FilePath          string     `json:"filePath,omitempty"`
	Error             string     `json:"error,omitempty"`
	Purpose           string     `json:"-"`
	Digest            string     `json:"-"`
	ExpectedSize      int64      `json:"-"`
	IntegrityVerified bool       `json:"-"`
	FinishedAt        int64      `json:"-"` // UnixNano; terminal-task GC grace starts here
}

// Live2DSyncProgress tracks an async Live2D online-asset sync (download the
// locally-missing models + their motion data from the CDNs into the local
// mirror). Mu guards the mutable fields, written by the sync goroutine and read
// concurrently by the progress HTTP handler.
type Live2DSyncProgress struct {
	Mu           sync.Mutex `json:"-"`
	TaskID       string     `json:"taskId"`
	Status       string     `json:"status"`  // checking|downloading|done|error|canceled
	Total        int        `json:"total"`   // number of missing models to download
	Current      int        `json:"current"` // models processed so far
	CurrentModel string     `json:"currentModel"`
	Files        int        `json:"files"`  // files written so far
	Bytes        int64      `json:"bytes"`  // bytes written so far
	Failed       int        `json:"failed"` // models still incomplete after download (asset missing upstream)
	Error        string     `json:"error,omitempty"`
}

// RecoveryData is the autosave recovery payload stored on disk.
type RecoveryData struct {
	Version    int                `json:"version,omitempty"`
	ActiveMode int                `json:"activeMode,omitempty"`
	Modes      []RecoveryModeData `json:"modes,omitempty"`
	// Legacy active-mode mirror. Kept so old single-mode autosave.json files load
	// unchanged and older frontends can still offer a V2 recovery.
	Content      string `json:"content"`
	FilePath     string `json:"filePath"`
	EditorMode   int    `json:"editorMode"`
	SavedAt      string `json:"savedAt"`
	StoryType    string `json:"storyType,omitempty"`
	StorySort    string `json:"storySort,omitempty"`
	StoryIndex   string `json:"storyIndex,omitempty"`
	StoryChapter int    `json:"storyChapter,omitempty"`
	StorySource  string `json:"storySource,omitempty"`
}

type RecoveryDocMeta struct {
	SaveTitle    string `json:"saveTitle"`
	ChapterTitle string `json:"chapterTitle"`
	StoryType    string `json:"type"`
	Sort         string `json:"sort"`
	Index        string `json:"index"`
	IndexLabel   string `json:"indexLabel"`
	Chapter      int    `json:"chapter"`
	Source       string `json:"source"`
	ScenarioID   string `json:"scenarioId"`
}

type RecoveryModeData struct {
	Content           string           `json:"content"`
	FilePath          string           `json:"filePath"`
	EditorMode        int              `json:"editorMode"`
	TitleOverride     string           `json:"titleOverride,omitempty"`
	HasUnsavedChanges bool             `json:"hasUnsavedChanges"`
	SourceTalks       []SourceTalk     `json:"sourceTalks,omitempty"`
	DocMeta           *RecoveryDocMeta `json:"docMeta,omitempty"`
}

type RecoveryModeSaveRequest struct {
	Talks             []DstTalk        `json:"talks"`
	FilePath          string           `json:"filePath"`
	EditorMode        int              `json:"editorMode"`
	TitleOverride     string           `json:"titleOverride,omitempty"`
	HasUnsavedChanges bool             `json:"hasUnsavedChanges"`
	SourceTalks       []SourceTalk     `json:"sourceTalks,omitempty"`
	DocMeta           *RecoveryDocMeta `json:"docMeta,omitempty"`
}

// RecoverySaveRequest saves editor state for crash recovery.
type RecoverySaveRequest struct {
	Version      int                       `json:"version,omitempty"`
	ActiveMode   int                       `json:"activeMode,omitempty"`
	Modes        []RecoveryModeSaveRequest `json:"modes,omitempty"`
	Talks        []DstTalk                 `json:"talks"`
	SaveN        bool                      `json:"saveN"`
	FilePath     string                    `json:"filePath"`
	EditorMode   int                       `json:"editorMode"`
	StoryType    string                    `json:"storyType,omitempty"`
	StorySort    string                    `json:"storySort,omitempty"`
	StoryIndex   string                    `json:"storyIndex,omitempty"`
	StoryChapter int                       `json:"storyChapter,omitempty"`
	StorySource  string                    `json:"storySource,omitempty"`
}

// SaveMetadata is embedded in save files so the app can auto-navigate on open.
type SaveMetadata struct {
	StoryType  string `json:"type"`
	Sort       string `json:"sort,omitempty"`
	Index      string `json:"index"`
	Chapter    int    `json:"chapter"`
	Source     string `json:"source"`
	ScenarioID string `json:"scenarioId"`
	Mode       int    `json:"mode,omitempty"`
}

// DiffPart represents a segment of a text diff for 合意 highlighting.
type DiffPart struct {
	Text string `json:"text"`
	Type string `json:"type"` // "same", "add", "remove"
}
