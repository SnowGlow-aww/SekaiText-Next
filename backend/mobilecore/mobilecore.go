// Package mobilecore is the gomobile-facing boundary for SekaiText's pure Go
// editor logic. The exported surface intentionally uses strings only: complex
// requests and responses are JSON so gomobile does not need to bind slices of
// project-specific structs.
package mobilecore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

var editor = service.NewEditorService()

type runtimeServices struct {
	mu              sync.RWMutex
	catalogDir      string
	cacheDir        string
	listManager     *service.ListManager
	downloader      *service.Downloader
	jsonLoader      *service.JsonLoaderService
	updateProgress  *service.ProgressTracker
	updateInFlight  bool
	lastUpdateError string
}

var runtimeState runtimeServices

var runStoryCatalogUpdate = func(lm *service.ListManager, dir string, pt *service.ProgressTracker) error {
	return lm.UpdateAllFromCDNLowMemory(dir, pt)
}

func mobileCharacter2DPublishGuard(lm *service.ListManager) service.Character2DPublishGuard {
	return func(publish func()) {
		runtimeState.mu.RLock()
		defer runtimeState.mu.RUnlock()
		if runtimeState.listManager == lm {
			// Hold the runtime read lock through the package-global swap so a
			// cross-root Initialize cannot replace the runtime between the current
			// check and publication.
			publish()
		}
	}
}

type createTranslationRequest struct {
	SourceTalks []model.SourceTalk `json:"sourceTalks"`
	JP          bool               `json:"jp"`
}

type loadTranslationResponse struct {
	Talks []model.DstTalk     `json:"talks"`
	Meta  *model.SaveMetadata `json:"meta"`
}

type serializeTranslationRequest struct {
	Talks []model.DstTalk     `json:"talks"`
	SaveN bool                `json:"saveN"`
	Meta  *model.SaveMetadata `json:"meta,omitempty"`
}

type serializeTranslationResponse struct {
	Content string `json:"content"`
}

type editorMutationResponse struct {
	Talks    []model.DstTalk `json:"talks"`
	DstTalks []model.DstTalk `json:"dstTalks"`
}

type compareTextResponse struct {
	Talks    []model.DstTalk `json:"talks"`
	DstTalks []model.DstTalk `json:"dstTalks"`
}

type storyTypeRequest struct {
	Type string `json:"type"`
}

type storyListRequest struct {
	Type  string `json:"type"`
	Sort  string `json:"sort"`
	Index string `json:"index,omitempty"`
}

type storyPathRequest struct {
	Type    string `json:"type"`
	Sort    string `json:"sort"`
	Index   string `json:"index"`
	Chapter int    `json:"chapter"`
	Source  string `json:"source"`
}

type resolveLabelRequest struct {
	Label string `json:"label"`
}

type voiceURLRequest struct {
	ScenarioID string `json:"scenarioId"`
	VoiceID    string `json:"voiceId"`
	Source     string `json:"source"`
	Chara2d    int    `json:"chara2d,omitempty"`
}

type storyCatalogStatus struct {
	Ready      bool   `json:"ready"`
	Generation uint64 `json:"generation"`
	Updating   bool   `json:"updating"`
	Error      string `json:"error,omitempty"`
}

type storyUpdateProgressResponse struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Message string `json:"message,omitempty"`
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// Initialize binds the embedded story core to Android-private persistent and
// cache roots. Catalog generations survive cache eviction; downloaded stories
// live under the OS-managed cache directory.
func Initialize(persistentDir, cacheDir string) error {
	if strings.TrimSpace(persistentDir) == "" || strings.TrimSpace(cacheDir) == "" {
		return fmt.Errorf("mobile persistent and cache directories are required")
	}
	persistentRoot, err := filepath.Abs(persistentDir)
	if err != nil {
		return fmt.Errorf("resolve mobile persistent directory: %w", err)
	}
	cacheRoot, err := filepath.Abs(cacheDir)
	if err != nil {
		return fmt.Errorf("resolve mobile cache directory: %w", err)
	}
	catalogDir := filepath.Join(persistentRoot, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		return fmt.Errorf("create mobile catalog directory: %w", err)
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return fmt.Errorf("create mobile cache directory: %w", err)
	}

	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	if runtimeState.listManager != nil && runtimeState.catalogDir == catalogDir && runtimeState.cacheDir == cacheRoot {
		return nil
	}
	if runtimeState.updateInFlight && runtimeState.catalogDir == catalogDir {
		// Android can recreate its Activity while the update goroutine is still
		// publishing this catalog. Keep that runtime (and its progress) until the
		// update finishes instead of introducing a second ListManager for the same
		// on-disk generation directory.
		return nil
	}

	// Construct and install the services while holding the runtime lock so an
	// update start cannot capture a ListManager from one runtime and a directory
	// from another during Activity reinitialization.
	listManager := service.NewListManager(catalogDir)
	listManager.SetCharacter2DPublishGuard(mobileCharacter2DPublishGuard(listManager))
	downloader := service.NewDownloader(cacheRoot)
	flashbacks := service.NewFlashbackAnalyzer(listManager)
	jsonLoader := service.NewJsonLoaderService(flashbacks)
	// Keep flashback clues, but avoid hidden cascaded story downloads on mobile.
	// Source-line lookup can be re-enabled later with explicit progress/cancel UI.

	runtimeState.catalogDir = catalogDir
	runtimeState.cacheDir = cacheRoot
	runtimeState.listManager = listManager
	runtimeState.downloader = downloader
	runtimeState.jsonLoader = jsonLoader
	runtimeState.updateProgress = nil
	runtimeState.updateInFlight = false
	runtimeState.lastUpdateError = ""
	return nil
}

// Bootstrap returns the embedded-core feature contract for the Android shell.
func Bootstrap() string {
	runtimeState.mu.RLock()
	storyReady := runtimeState.listManager != nil
	runtimeState.mu.RUnlock()
	return fmt.Sprintf(
		`{"platform":"android","editor":true,"story":%t,"live2d":%t,"glossary":false,"team":false}`,
		storyReady,
		mobileLive2DReady(),
	)
}

func storyRuntime() (*service.ListManager, *service.Downloader, *service.JsonLoaderService, error) {
	runtimeState.mu.RLock()
	defer runtimeState.mu.RUnlock()
	if runtimeState.listManager == nil || runtimeState.downloader == nil || runtimeState.jsonLoader == nil {
		return nil, nil, nil, fmt.Errorf("mobile core is not initialized")
	}
	return runtimeState.listManager, runtimeState.downloader, runtimeState.jsonLoader, nil
}

// StoryTypes returns the same navigation labels as the desktop backend.
func StoryTypes() (string, error) {
	lm, _, _, err := storyRuntime()
	if err != nil {
		return "", err
	}
	return encode(lm.GetStoryTypes())
}

func StorySorts(requestJSON string) (string, error) {
	lm, _, _, err := storyRuntime()
	if err != nil {
		return "", err
	}
	var req storyTypeRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode story sorts request: %w", err)
	}
	sorts := lm.GetStorySorts(req.Type)
	if sorts == nil {
		sorts = []model.StorySort{}
	}
	return encode(sorts)
}

func StoryIndex(requestJSON string) (string, error) {
	lm, _, _, err := storyRuntime()
	if err != nil {
		return "", err
	}
	var req storyListRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode story index request: %w", err)
	}
	indices := lm.GetStoryIndexList(req.Type, req.Sort)
	if indices == nil {
		indices = []model.StoryIndex{}
	}
	return encode(indices)
}

func StoryChapters(requestJSON string) (string, error) {
	lm, _, _, err := storyRuntime()
	if err != nil {
		return "", err
	}
	var req storyListRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode story chapters request: %w", err)
	}
	chapters := lm.GetStoryChapterList(req.Type, req.Sort, req.Index)
	if chapters == nil {
		chapters = []model.StoryChapter{}
	}
	return encode(chapters)
}

func StoryJSONPath(requestJSON string) (string, error) {
	lm, _, _, err := storyRuntime()
	if err != nil {
		return "", err
	}
	var req storyPathRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode story JSON path request: %w", err)
	}
	return encode(lm.GetJsonPath(req.Type, req.Sort, req.Index, req.Chapter, req.Source))
}

// StoryLoad resolves, downloads, caches, and parses one selected story.
func StoryLoad(requestJSON string) (string, error) {
	lm, downloader, loader, err := storyRuntime()
	if err != nil {
		return "", err
	}
	var req model.LoadRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode story load request: %w", err)
	}
	path := lm.GetJsonPath(req.StoryType, req.Sort, req.Index, req.Chapter, req.Source)
	if path.URL == "" {
		return "", fmt.Errorf("story not found: type=%s index=%s chapter=%d source=%s", req.StoryType, req.Index, req.Chapter, req.Source)
	}
	filePath, err := downloader.DownloadJSON(path.URL, path.FileName)
	if err != nil {
		return "", fmt.Errorf("story download failed: %w", err)
	}
	response, err := loader.ParseFile(filePath)
	if err != nil {
		return "", fmt.Errorf("story parse failed: %w", err)
	}
	response.SaveTitle = path.SaveTitle
	response.ChapterTitle = path.ChapterTitle
	response.IndexLabel = lm.IndexLabel(req.StoryType, req.Sort, req.Index)
	if strings.Contains(req.StoryType, "卡面") {
		if name := scenarioAssetName(path.URL); name != "" {
			response.ScenarioID = name
		}
	}
	return encode(response)
}

func StoryLoadLocal(content string) (string, error) {
	_, _, loader, err := storyRuntime()
	if err != nil {
		return "", err
	}
	response, err := loader.ParseBytes([]byte(content))
	if err != nil {
		return "", fmt.Errorf("story parse failed: %w", err)
	}
	return encode(response)
}

var cardScenarioPattern = regexp.MustCompile(`^\d{6}_`)

// VoiceURL mirrors the desktop resolver. The selected story source is accepted
// for API compatibility, but audio always comes from the complete JP mirror.
func VoiceURL(requestJSON string) (string, error) {
	if _, _, _, err := storyRuntime(); err != nil {
		return "", err
	}
	var req voiceURLRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode voice URL request: %w", err)
	}
	const baseURL = "https://storage.exmeaning.com/sekai-jp-assets/"
	var url string
	switch {
	case strings.HasPrefix(req.VoiceID, "partvoice"):
		if character, ok := service.Character2dByID(req.Chara2d); ok {
			url = baseURL + "sound/scenario/voice/part_voice_" + character.AssetName + "_" + character.Unit + "/" + req.VoiceID + ".mp3"
		}
	case cardScenarioPattern.MatchString(req.ScenarioID):
		url = baseURL + "sound/card_scenario/voice/" + req.ScenarioID + "/" + req.VoiceID + ".mp3"
	default:
		url = baseURL + "sound/scenario/voice/" + req.ScenarioID + "/" + req.VoiceID + ".mp3"
	}
	return encode(model.VoiceURLResponse{URL: url})
}

func ResolveStoryLabel(requestJSON string) (string, error) {
	lm, _, _, err := storyRuntime()
	if err != nil {
		return "", err
	}
	var req resolveLabelRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode story label request: %w", err)
	}
	storyType, index, indexLabel, chapter, ok := lm.ResolveLabel(req.Label)
	return encode(map[string]any{
		"ok":         ok,
		"storyType":  storyType,
		"index":      index,
		"indexLabel": indexLabel,
		"chapter":    chapter,
	})
}

func StoryCatalogStatus() (string, error) {
	runtimeState.mu.RLock()
	lm := runtimeState.listManager
	initialized := lm != nil && runtimeState.downloader != nil && runtimeState.jsonLoader != nil
	updating := runtimeState.updateInFlight
	lastError := runtimeState.lastUpdateError
	runtimeState.mu.RUnlock()
	if !initialized {
		return "", fmt.Errorf("mobile core is not initialized")
	}
	ready, generation := lm.CatalogState()
	return encode(storyCatalogStatus{
		Ready: ready, Generation: generation, Updating: updating, Error: lastError,
	})
}

// UpdateStoryCatalog starts one background metadata refresh. The current valid
// generation remains readable until a complete new generation is committed.
func UpdateStoryCatalog() (string, error) {
	runtimeState.mu.Lock()
	if runtimeState.listManager == nil || runtimeState.downloader == nil || runtimeState.jsonLoader == nil {
		runtimeState.mu.Unlock()
		return "", fmt.Errorf("mobile core is not initialized")
	}
	if runtimeState.updateInFlight {
		runtimeState.mu.Unlock()
		return encode(map[string]string{"status": "running"})
	}
	lm := runtimeState.listManager
	catalogDir := runtimeState.catalogDir
	progress := service.NewProgressTracker()
	runner := runStoryCatalogUpdate
	runtimeState.updateProgress = progress
	runtimeState.updateInFlight = true
	runtimeState.lastUpdateError = ""
	runtimeState.mu.Unlock()

	go func() {
		updateErr := runner(lm, catalogDir, progress)
		runtimeState.mu.Lock()
		if runtimeState.listManager == lm && runtimeState.updateProgress == progress {
			runtimeState.updateInFlight = false
			if updateErr != nil {
				runtimeState.lastUpdateError = updateErr.Error()
			}
		}
		runtimeState.mu.Unlock()
	}()
	return encode(map[string]string{"status": "started"})
}

func StoryUpdateProgress() (string, error) {
	runtimeState.mu.RLock()
	initialized := runtimeState.listManager != nil && runtimeState.downloader != nil && runtimeState.jsonLoader != nil
	progress := runtimeState.updateProgress
	updating := runtimeState.updateInFlight
	lastError := runtimeState.lastUpdateError
	runtimeState.mu.RUnlock()
	if !initialized {
		return "", fmt.Errorf("mobile core is not initialized")
	}
	if progress == nil {
		status := "idle"
		if lastError != "" {
			status = "error"
		}
		return encode(storyUpdateProgressResponse{
			Done: true, Status: status, Message: lastError, Error: lastError,
		})
	}
	current, total, message, trackerDone := progress.Status()
	status := "running"
	done := false
	if !updating {
		done = true
		switch {
		case lastError != "":
			status = "error"
			message = lastError
		case trackerDone:
			status = "done"
		default:
			status = "idle"
		}
	}
	return encode(storyUpdateProgressResponse{
		Current: current,
		Total:   total,
		Message: message,
		Done:    done,
		Status:  status,
		Error:   lastError,
	})
}

func scenarioAssetName(rawURL string) string {
	value := rawURL
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		value = value[:index]
	}
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		value = value[index+1:]
	}
	if index := strings.LastIndexByte(value, '.'); index >= 0 {
		value = value[:index]
	}
	return value
}

// CreateTranslation builds an editable destination template from source talks.
func CreateTranslation(requestJSON string) (string, error) {
	var req createTranslationRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode create translation request: %w", err)
	}
	return encode(editor.CreateFile(req.SourceTalks, req.JP))
}

// LoadTranslation parses an existing SekaiText translation document from text.
func LoadTranslation(content string) (string, error) {
	talks, meta, err := editor.LoadContent(content)
	if err != nil {
		return "", err
	}
	return encode(loadTranslationResponse{Talks: talks, Meta: meta})
}

// SerializeTranslation converts editor talks back to the desktop-compatible txt
// format. File picking and URI writes remain owned by the Android shell.
func SerializeTranslation(requestJSON string) (string, error) {
	var req serializeTranslationRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode serialize translation request: %w", err)
	}
	return encode(serializeTranslationResponse{
		Content: editor.SerializeWithMeta(req.Talks, req.SaveN, req.Meta),
	})
}

// CheckText applies the same validation/fixup rules as the desktop editor.
func CheckLines(requestJSON string) (string, error) {
	var req model.CheckLinesRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode check lines request: %w", err)
	}
	return encode(editor.CheckLines(req.SourceTalks, req.LoadedTalks))
}

func CompareText(requestJSON string) (string, error) {
	var req model.CompareRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode compare text request: %w", err)
	}
	return encode(compareTextResponse{
		Talks:    editor.CompareText(req.ReferTalks, req.CheckTalks, req.EditorMode),
		DstTalks: req.CheckTalks,
	})
}

func ChangeText(requestJSON string) (string, error) {
	var req model.EditorChangeTextRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode change text request: %w", err)
	}
	talks, dstTalks := editor.ChangeText(
		req.Row,
		req.Text,
		req.EditorMode,
		req.Talks,
		req.DstTalks,
		req.ReferTalks,
	)
	return encode(editorMutationResponse{Talks: talks, DstTalks: dstTalks})
}

func AddLine(requestJSON string) (string, error) {
	var req model.EditorAddLineRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode add line request: %w", err)
	}
	talks, dstTalks := editor.AddLine(req.Row, req.Talks, req.DstTalks, req.IsProofread)
	return encode(editorMutationResponse{Talks: talks, DstTalks: dstTalks})
}

func RemoveLine(requestJSON string) (string, error) {
	var req model.EditorRemoveLineRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode remove line request: %w", err)
	}
	talks, dstTalks := editor.RemoveLine(req.Row, req.Talks, req.DstTalks)
	return encode(editorMutationResponse{Talks: talks, DstTalks: dstTalks})
}

func ReplaceBrackets(requestJSON string) (string, error) {
	var req model.EditorReplaceBracketsRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode replace brackets request: %w", err)
	}
	talks, dstTalks := editor.ReplaceBrackets(req.Talks, req.DstTalks, req.Row, req.Brackets)
	return encode(editorMutationResponse{Talks: talks, DstTalks: dstTalks})
}

func SpeakerCount(requestJSON string) (string, error) {
	var req model.SpeakerCountRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode speaker count request: %w", err)
	}

	type speakerCount struct {
		japanese string
		count    int
	}
	counts := make(map[string]speakerCount)
	for _, talk := range req.Talks {
		if talk.Speaker == "" || talk.Speaker == "场景" || talk.Speaker == "左上场景" || talk.Speaker == "选项" {
			continue
		}
		sourceSpeaker := talk.Speaker
		if talk.Idx > 0 && talk.Idx-1 < len(req.SourceTalks) && req.SourceTalks[talk.Idx-1].Speaker != "" {
			sourceSpeaker = req.SourceTalks[talk.Idx-1].Speaker
		}
		entry := counts[sourceSpeaker]
		entry.japanese = sourceSpeaker
		entry.count++
		counts[sourceSpeaker] = entry
	}

	speakers := make([]model.SpeakerEntry, 0, len(counts))
	for _, entry := range counts {
		speakers = append(speakers, model.SpeakerEntry{
			Japanese: entry.japanese,
			Chinese:  "",
			Count:    entry.count,
		})
	}
	return encode(model.SpeakerCountResponse{Speakers: speakers})
}

func CheckText(requestJSON string) (string, error) {
	var req model.CheckTextRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode check text request: %w", err)
	}
	return encode(editor.GetTextCheck(req))
}

func encode(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode mobile response: %w", err)
	}
	return string(encoded), nil
}
