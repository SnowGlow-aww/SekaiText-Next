package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	cfg           *config.AppConfig
	lm            *service.ListManager
	editor        *service.EditorService
	jsonLoader    *service.JsonLoaderService
	fb            *service.FlashbackAnalyzer
	dl            *service.Downloader
	progress      *service.ProgressTracker
	logBuf        *service.LogBuffer
	glossary      *service.GlossaryStore
	plugins       *service.PluginStore
	market        *service.MarketService
	team          *service.TeamService
	downloadTasks sync.Map // map[string]*model.DownloadTaskProgress
}

// NewHandler creates a new Handler with all services initialized.
func NewHandler(cfg *config.AppConfig, logBuf *service.LogBuffer) *Handler {
	lm := service.NewListManager(cfg.CatalogDir)
	fb := service.NewFlashbackAnalyzer(lm)
	dl := service.NewDownloader(cfg.DataDir)
	jsonLoader := service.NewJsonLoaderService(fb)
	jsonLoader.SetSourceLocator(dl, cfg.DataDir)
	pluginStore := service.NewPluginStore(cfg.PluginsDir)
	return &Handler{
		cfg:        cfg,
		lm:         lm,
		editor:     service.NewEditorService(),
		jsonLoader: jsonLoader,
		fb:         fb,
		dl:         dl,
		progress:   service.NewProgressTracker(),
		logBuf:     logBuf,
		glossary:   service.NewGlossaryStore(cfg.DataDir),
		plugins:    pluginStore,
		market:     service.NewMarketService(pluginStore),
		team:       service.NewTeamService(cfg.DataDir),
	}
}

// --- Story ---

func (h *Handler) StoryTypes(w http.ResponseWriter, r *http.Request) {
	types := h.lm.GetStoryTypes()
	writeJSON(w, http.StatusOK, types)
}

func (h *Handler) StorySorts(w http.ResponseWriter, r *http.Request) {
	storyType := r.URL.Query().Get("type")
	sorts := h.lm.GetStorySorts(storyType)
	if sorts == nil {
		sorts = []model.StorySort{}
	}
	writeJSON(w, http.StatusOK, sorts)
}

func (h *Handler) StoryIndex(w http.ResponseWriter, r *http.Request) {
	storyType := r.URL.Query().Get("type")
	sort := r.URL.Query().Get("sort")
	indices := h.lm.GetStoryIndexList(storyType, sort)
	if indices == nil {
		indices = []model.StoryIndex{}
	}
	writeJSON(w, http.StatusOK, indices)
}

func (h *Handler) StoryChapter(w http.ResponseWriter, r *http.Request) {
	storyType := r.URL.Query().Get("type")
	sort := r.URL.Query().Get("sort")
	index := r.URL.Query().Get("index")
	chapters := h.lm.GetStoryChapterList(storyType, sort, index)
	if chapters == nil {
		chapters = []model.StoryChapter{}
	}
	writeJSON(w, http.StatusOK, chapters)
}

func (h *Handler) JsonPath(w http.ResponseWriter, r *http.Request) {
	storyType := r.URL.Query().Get("type")
	sort := r.URL.Query().Get("sort")
	index := r.URL.Query().Get("index")
	chapter, _ := strconv.Atoi(r.URL.Query().Get("chapter"))
	source := r.URL.Query().Get("source")

	result := h.lm.GetJsonPath(storyType, sort, index, chapter, source)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) StoryLoad(w http.ResponseWriter, r *http.Request) {
	var req model.LoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get JSON path from CDN
	path := h.lm.GetJsonPath(req.StoryType, req.Sort, req.Index, req.Chapter, req.Source)
	if path.URL == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf("story not found: type=%s index=%s chapter=%d source=%s", req.StoryType, req.Index, req.Chapter, req.Source))
		return
	}

	// Download and parse
	filePath, err := h.dl.DownloadJSON(path.URL, path.FileName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "story download failed: "+err.Error())
		return
	}
	resp, err := h.jsonLoader.ParseFile(filePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "story parse failed: "+err.Error())
		return
	}

	resp.SaveTitle = path.SaveTitle
	resp.ChapterTitle = path.ChapterTitle
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) StoryLoadLocal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.jsonLoader.ParseBytes([]byte(req.Content))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "story parse failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ResolveLabel reverse-maps a filename label (e.g. "3rd-group3-01") to the story
// coordinates needed to auto-load its source. ok=false when the label can't be
// resolved (caller then keeps manual selection).
func (h *Handler) ResolveLabel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	storyType, index, chapter, ok := h.lm.ResolveLabel(req.Label)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        ok,
		"storyType": storyType,
		"index":     index,
		"chapter":   chapter,
	})
}

// --- Translation ---

func (h *Handler) TranslationCreate(w http.ResponseWriter, r *http.Request) {
	var req model.TranslationCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks := h.editor.CreateFile(req.SourceTalks, req.JP)
	writeJSON(w, http.StatusOK, talks)
}

func (h *Handler) TranslationLoad(w http.ResponseWriter, r *http.Request) {
	var req model.TranslationLoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks, meta, err := h.editor.LoadFile(req.FilePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "file load failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks": talks,
		"meta":  meta,
	})
}

func (h *Handler) TranslationLoadContent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks, meta, err := h.editor.LoadContent(req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "file load failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks": talks,
		"meta":  meta,
	})
}

// EnsureDir creates the directory (and parents) for a path so the native save
// dialog can default to it without macOS NSSavePanel rejecting a non-existent
// parent. Returns the directory that now exists.
func (h *Handler) EnsureDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dir := req.Path
	// If the path looks like a file (has an extension), use its parent dir.
	if filepath.Ext(dir) != "" {
		dir = filepath.Dir(dir)
	}
	if dir == "" || dir == "." {
		writeJSON(w, http.StatusOK, map[string]string{"dir": dir})
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[ensure-dir] mkdir error: %v", err)
		writeError(w, http.StatusInternalServerError, "create dir failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"dir": dir})
}

func (h *Handler) TranslationSave(w http.ResponseWriter, r *http.Request) {
	var req model.TranslationSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[save] decode error: %v", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	log.Printf("[save] writing %s (%d talks, saveN=%v, hasMeta=%v)", req.FilePath, len(req.Talks), req.SaveN, req.Meta != nil)
	content := h.editor.SerializeWithMeta(req.Talks, req.SaveN, req.Meta)
	if dir := filepath.Dir(req.FilePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[save] mkdir error: %v", err)
			writeError(w, http.StatusInternalServerError, "create dir failed: "+err.Error())
			return
		}
	}
	if err := os.WriteFile(req.FilePath, []byte(content), 0644); err != nil {
		log.Printf("[save] write error: %v", err)
		writeError(w, http.StatusInternalServerError, "file write failed: "+err.Error())
		return
	}
	log.Printf("[save] ok: %s (%d bytes)", req.FilePath, len(content))
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (h *Handler) TranslationSerialize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Talks []model.DstTalk     `json:"talks"`
		SaveN bool                `json:"saveN"`
		Meta  *model.SaveMetadata `json:"meta,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	content := h.editor.SerializeWithMeta(req.Talks, req.SaveN, req.Meta)
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (h *Handler) CheckLines(w http.ResponseWriter, r *http.Request) {
	var req model.CheckLinesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks := h.editor.CheckLines(req.SourceTalks, req.LoadedTalks)
	writeJSON(w, http.StatusOK, talks)
}

// --- Editor ---

func (h *Handler) ChangeText(w http.ResponseWriter, r *http.Request) {
	var req model.EditorChangeTextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks, dstTalks := h.editor.ChangeText(req.Row, req.Text, req.EditorMode,
		req.Talks, req.DstTalks, req.ReferTalks)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks":    talks,
		"dstTalks": dstTalks,
	})
}

func (h *Handler) AddLine(w http.ResponseWriter, r *http.Request) {
	var req model.EditorAddLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks, dstTalks := h.editor.AddLine(req.Row, req.Talks, req.DstTalks, req.IsProofread)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks":    talks,
		"dstTalks": dstTalks,
	})
}

func (h *Handler) RemoveLine(w http.ResponseWriter, r *http.Request) {
	var req model.EditorRemoveLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks, dstTalks := h.editor.RemoveLine(req.Row, req.Talks, req.DstTalks)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks":    talks,
		"dstTalks": dstTalks,
	})
}

func (h *Handler) Compare(w http.ResponseWriter, r *http.Request) {
	var req model.CompareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	talks := h.editor.CompareText(req.ReferTalks, req.CheckTalks, req.EditorMode)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks":    talks,
		"dstTalks": req.CheckTalks,
	})
}

func (h *Handler) ReplaceBrackets(w http.ResponseWriter, r *http.Request) {
	var req model.EditorReplaceBracketsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[editor] replace-brackets decode error: %v", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	log.Printf("[editor] replace-brackets row=%d brackets=%q (%d talks)", req.Row, req.Brackets, len(req.Talks))
	talks, dstTalks := h.editor.ReplaceBrackets(req.Talks, req.DstTalks, req.Row, req.Brackets)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"talks":    talks,
		"dstTalks": dstTalks,
	})
}

// --- Check Text ---

func (h *Handler) CheckText(w http.ResponseWriter, r *http.Request) {
	var req model.CheckTextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp := h.editor.GetTextCheck(req)
	writeJSON(w, http.StatusOK, resp)
}

// --- Flashback ---

func (h *Handler) FlashbackAnalyze(w http.ResponseWriter, r *http.Request) {
	var req model.FlashbackAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Analyze clues for each talk
	for i := range req.SourceTalks {
		if len(req.SourceTalks[i].Voices) == 0 {
			continue
		}
		clueSet := make(map[string]struct{})
		for _, voiceID := range req.SourceTalks[i].Voices {
			clue, ignore := h.fb.GetClueFromVoiceID(voiceID)
			if !ignore && clue != "" {
				clueSet[clue] = struct{}{}
			}
		}
		for clue := range clueSet {
			req.SourceTalks[i].Clues = append(req.SourceTalks[i].Clues, clue)
		}
	}

	writeJSON(w, http.StatusOK, model.FlashbackAnalyzeResponse{
		SourceTalks: req.SourceTalks,
	})
}

func (h *Handler) ClueHints(w http.ResponseWriter, r *http.Request) {
	clue := r.URL.Query().Get("clue")
	lang := r.URL.Query().Get("lang")

	hints := h.fb.GetClueHints(clue, lang)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clue":  clue,
		"hints": hints,
	})
}

func (h *Handler) VoiceClues(w http.ResponseWriter, r *http.Request) {
	clues := h.lm.BuildVoiceIDClues()
	writeJSON(w, http.StatusOK, clues)
}

// --- Live2D ---

var live2dAllowedHosts = []string{
	"https://storage2.exmeaning.com/",
	"https://storage.exmeaning.com/",
	"https://storage.sekai.best/",
	"https://assets.unipjsk.com/",
	"https://sekai-assets-bdf29c81.seiunx.net/",
}

// live2dLocalPath maps an upstream CDN asset URL to its path inside the local
// mirror (config.Live2DLocalDir), mirroring the layout the downloader script
// writes. Returns "" if the URL isn't a mirrorable Live2D asset.
//
// Layout:
//
//	exmeaning  .../live2d/model/{rest}        -> {root}/model/{rest}
//	sekai.best .../live2d/motion/{rest}       -> {root}/motion/{rest}
//	either     .../live2d/model_list.json     -> {root}/model_list.json
func live2dLocalPath(root, url string) string {
	if root == "" {
		return ""
	}
	// Strip protocol+host, keep the path.
	noScheme := url
	if i := strings.Index(noScheme, "://"); i >= 0 {
		noScheme = noScheme[i+3:]
	}
	slash := strings.IndexByte(noScheme, '/')
	if slash < 0 {
		return ""
	}
	path := noScheme[slash+1:] // e.g. sekai-live2d-assets/live2d/model/...
	// Find the "live2d/" segment and take everything after it.
	marker := "live2d/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}
	rest := path[idx+len(marker):] // model/... | motion/... | model_list.json
	if rest == "" || strings.Contains(rest, "..") {
		return ""
	}
	if rest == "model_list.json" || strings.HasPrefix(rest, "model/") || strings.HasPrefix(rest, "motion/") {
		return filepath.Join(root, filepath.FromSlash(rest))
	}
	return ""
}

// Live2DProxy streams a Live2D asset (model3.json / moc3 / textures / motions)
// from the upstream CDN through the local backend. The frontend cannot fetch
// some CDNs directly (CORS / webview sandbox network rules) but the backend can,
// so all Live2D asset requests are proxied here. live2dAllowedHosts restricts
// targets to known asset hosts (anti-SSRF).
//
// Local-first: if the asset exists in the local mirror (config.Live2DLocalDir),
// it is served from disk and the CDN is not contacted at all.
func (h *Handler) Live2DProxy(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	allowed := false
	for _, host := range live2dAllowedHosts {
		if strings.HasPrefix(url, host) {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "url host not allowed")
		return
	}

	// Try the local mirror first.
	if local := live2dLocalPath(h.cfg.Live2DLocalDir, url); local != "" {
		if info, err := os.Stat(local); err == nil && !info.IsDir() && info.Size() > 0 {
			if f, err := os.Open(local); err == nil {
				defer f.Close()
				w.Header().Set("Content-Type", live2dContentType(local))
				w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
				w.Header().Set("X-Live2D-Source", "local")
				w.WriteHeader(http.StatusOK)
				io.Copy(w, f)
				return
			}
		}
	}

	resp, err := h.dl.Get(url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream fetch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("X-Live2D-Source", "cdn")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// live2dContentType picks a Content-Type for a locally-served Live2D asset.
func live2dContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".json"), strings.HasSuffix(path, ".model3"),
		strings.HasSuffix(path, ".motion3.json"), strings.HasSuffix(path, ".physics3"):
		return "application/json"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".moc3"):
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

// --- Voice ---

func (h *Handler) VoiceURL(w http.ResponseWriter, r *http.Request) {
	scenarioID := r.URL.Query().Get("scenarioId")
	voiceID := r.URL.Query().Get("voiceId")

	// Voice audio is always served from the moesekai-jp mirror regardless of the
	// story's selected source. The default source (HarukiBot NEO) and unipjsk do
	// not host voice clips at all, and moesekai-jp is a full JP mirror, so routing
	// every voice request here is the only reliable option.
	baseURL := "https://storage.exmeaning.com/sekai-jp-assets/"

	url := baseURL + "sound/scenario/voice/" + scenarioID + "/" + voiceID + ".mp3"
	writeJSON(w, http.StatusOK, model.VoiceURLResponse{URL: url})
}

// --- Speaker ---

func (h *Handler) SpeakerCount(w http.ResponseWriter, r *http.Request) {
	var req model.SpeakerCountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Count speakers
	speakerMap := make(map[string]struct {
		japanese string
		count    int
	})

	for _, talk := range req.Talks {
		if talk.Speaker == "" || talk.Speaker == "场景" || talk.Speaker == "左上场景" || talk.Speaker == "选项" {
			continue
		}
		var srcSpeaker string
		if talk.Idx > 0 && talk.Idx-1 < len(req.SourceTalks) {
			srcSpeaker = req.SourceTalks[talk.Idx-1].Speaker
		}
		if srcSpeaker == "" {
			srcSpeaker = talk.Speaker
		}
		entry := speakerMap[srcSpeaker]
		entry.japanese = srcSpeaker
		entry.count++
		speakerMap[srcSpeaker] = entry
	}

	var speakers []model.SpeakerEntry
	for _, entry := range speakerMap {
		speakers = append(speakers, model.SpeakerEntry{
			Japanese: entry.japanese,
			Chinese:  "",
			Count:    entry.count,
		})
	}

	writeJSON(w, http.StatusOK, model.SpeakerCountResponse{Speakers: speakers})
}

// --- Settings ---

func (h *Handler) settingsPath() string {
	return h.cfg.CatalogDir + "/settings.json"
}

func (h *Handler) loadSettings() (model.Settings, error) {
	data, err := os.ReadFile(h.settingsPath())
	if err != nil {
		return model.Settings{}, err
	}
	var s model.Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return model.Settings{}, err
	}
	return s, nil
}

func (h *Handler) saveSettings(s model.Settings) error {
	os.MkdirAll(h.cfg.CatalogDir, 0755)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(h.settingsPath(), data, 0644)
}

// ImportLive2D moves a user-picked folder of Live2D assets (model/ + motion/ +
// model_list.json, as produced by the downloader) into the app's local mirror
// (config.Live2DLocalDir). After import, scenario playback serves these from
// disk instead of the CDN. The source is MOVED (removed after) and merged into
// any existing local library (same-named files overwritten, dirs merged).
func (h *Handler) ImportLive2D(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SrcDir string `json:"srcDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	src := strings.TrimSpace(req.SrcDir)
	if src == "" {
		writeError(w, http.StatusBadRequest, "missing srcDir")
		return
	}
	if abs, err := filepath.Abs(src); err == nil {
		src = abs
	}
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "source folder not found")
		return
	}
	dst := h.cfg.Live2DLocalDir
	if dst == "" {
		writeError(w, http.StatusInternalServerError, "live2d local dir not configured")
		return
	}
	if absDst, err := filepath.Abs(dst); err == nil {
		dst = absDst
	}
	// Guard against importing the target into itself, or a parent of the target.
	if src == dst || strings.HasPrefix(dst+string(os.PathSeparator), src+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "cannot import this folder into itself")
		return
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "create target failed: "+err.Error())
		return
	}

	base := filepath.Base(src)
	moved := 0
	// If the user picked a `model` or `motion` folder directly, move it under the
	// matching subdir; otherwise move every top-level entry into the target root
	// (covers both the asset root containing model/motion/model_list.json and any
	// loose layout).
	if base == "model" || base == "motion" {
		if err := mergeMove(src, filepath.Join(dst, base)); err != nil {
			writeError(w, http.StatusInternalServerError, "import failed: "+err.Error())
			return
		}
		moved = 1
	} else {
		entries, err := os.ReadDir(src)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read source failed: "+err.Error())
			return
		}
		for _, e := range entries {
			if err := mergeMove(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				writeError(w, http.StatusInternalServerError, "import failed at "+e.Name()+": "+err.Error())
				return
			}
			moved++
		}
		// The now-empty source folder is removed for the "move" semantics.
		os.Remove(src)
	}
	log.Printf("[live2d-import] moved %d entries from %s into %s", moved, src, dst)
	writeJSON(w, http.StatusOK, map[string]interface{}{"dir": dst, "moved": moved})
}

// mergeMove moves src to dst, merging into an existing dst (files overwritten,
// directories merged recursively). Tries a fast os.Rename first; on failure
// (e.g. cross-volume EXDEV, or dst exists) falls back to copy + remove.
func mergeMove(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if si.IsDir() {
		if err := os.MkdirAll(dst, si.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := mergeMove(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Remove(src) // now empty
	}
	// File: copy then remove the source.
	if err := copyFile(src, dst, si.Mode()); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// OpenDataDir reveals the app's writable data directory (DataBaseDir) in the OS
// file manager, so users can reach downloaded JSON, the Live2D asset mirror, etc.
func (h *Handler) OpenDataDir(w http.ResponseWriter, r *http.Request) {
	dir := h.cfg.DataBaseDir
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[open-data-dir] mkdir error: %v", err)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[open-data-dir] launch error: %v", err)
		writeError(w, http.StatusInternalServerError, "open failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"dir": dir})
}

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.loadSettings()
	if err != nil {
		s = model.DefaultSettings()
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var s model.Settings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.saveSettings(s); err != nil {
		writeError(w, http.StatusInternalServerError, "settings save failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// --- Update ---

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	go h.lm.UpdateAll(h.cfg.CatalogDir, h.progress)
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (h *Handler) UpdateProgress(w http.ResponseWriter, r *http.Request) {
	current, total, message, done := h.progress.Status()
	writeJSON(w, http.StatusOK, model.UpdateProgress{
		Current: current,
		Total:   total,
		Message: message,
		Done:    done,
	})
}

// --- JSON Download ---

func (h *Handler) DownloadStoryJSON(w http.ResponseWriter, r *http.Request) {
	var req model.JsonDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	path := h.lm.GetJsonPath(req.StoryType, req.Sort, req.Index, req.Chapter, req.Source)
	if path.URL == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf("story not found: type=%s index=%s chapter=%d source=%s", req.StoryType, req.Index, req.Chapter, req.Source))
		return
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = h.cfg.DataDir + "/json"
	}

	taskID := strconv.FormatInt(time.Now().UnixNano(), 36)
	task := &model.DownloadTaskProgress{TaskID: taskID, Status: "downloading"}
	h.downloadTasks.Store(taskID, task)

	go func() {
		dlPath, err := h.dl.DownloadJSONToDir(path.URL, outputDir, path.FileName, func(read, total int64) {
			task.Read = read
			task.Total = total
		})
		if err != nil {
			task.Status = "error"
			task.Error = err.Error()
		} else {
			task.Status = "done"
			task.FilePath = dlPath
		}
	}()

	writeJSON(w, http.StatusOK, map[string]string{"taskId": taskID})
}

func (h *Handler) DownloadProgress(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}

	val, ok := h.downloadTasks.Load(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	task := val.(*model.DownloadTaskProgress)
	writeJSON(w, http.StatusOK, task)

	// Clean up completed tasks after serving
	if task.Status == "done" || task.Status == "error" {
		h.downloadTasks.Delete(taskID)
	}
}

// --- Assets ---

func (h *Handler) Characters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.CharacterDict)
}

func (h *Handler) CharacterIcon(w http.ResponseWriter, r *http.Request) {
	indexStr := chi.URLParam(r, "index")
	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 1 || index > 31 {
		writeError(w, http.StatusBadRequest, "invalid character index")
		return
	}
	iconPath := h.cfg.ImagesChrDir + "/chr_" + indexStr + ".png"
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, iconPath)
}

func (h *Handler) Units(w http.ResponseWriter, r *http.Request) {
	units := make([]model.UnitInfo, 0, len(model.UnitDict))
	for k, v := range model.UnitDict {
		units = append(units, model.UnitInfo{Key: k, Name: v})
	}
	writeJSON(w, http.StatusOK, units)
}

func (h *Handler) Areas(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.AreaDict)
}

// --- Debug ---

func (h *Handler) DebugLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.logBuf.Lines())
}

func (h *Handler) DebugSaveLogs(w http.ResponseWriter, r *http.Request) {
	entries := h.logBuf.Lines()
	f, err := os.Create("debug.log")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "log file create failed: "+err.Error())
		return
	}
	defer f.Close()

	f.WriteString("=== SekaiText Debug Log === " + time.Now().Format("2006-01-02 15:04:05") + " ===\n\n")
	for _, e := range entries {
		f.WriteString("[" + e.Timestamp + "] " + e.Message + "\n")
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "saved",
		"lines":  len(entries),
	})
}

// --- Recovery (autosave) ---

func (h *Handler) recoveryPath() string {
	return h.cfg.DataDir + "/autosave.json"
}

func (h *Handler) RecoverySave(w http.ResponseWriter, r *http.Request) {
	var req model.RecoverySaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[recovery] save decode error: %v", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	log.Printf("[recovery] saving autosave (%d talks, path=%s)", len(req.Talks), req.FilePath)
	content := h.editor.SerializeContent(req.Talks, req.SaveN)
	data := model.RecoveryData{
		Content:      content,
		FilePath:     req.FilePath,
		EditorMode:   req.EditorMode,
		SavedAt:      time.Now().Format("2006-01-02 15:04:05"),
		StoryType:    req.StoryType,
		StorySort:    req.StorySort,
		StoryIndex:   req.StoryIndex,
		StoryChapter: req.StoryChapter,
		StorySource:  req.StorySource,
	}

	f, err := os.Create(h.recoveryPath())
	if err != nil {
		log.Printf("[recovery] save write error: %v", err)
		writeError(w, http.StatusInternalServerError, "autosave write failed: "+err.Error())
		return
	}
	defer f.Close()
	json.NewEncoder(f).Encode(data)
	log.Printf("[recovery] autosave ok (%d bytes)", len(content))
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (h *Handler) RecoveryLoad(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(h.recoveryPath())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"exists": false})
		return
	}
	defer f.Close()

	var data model.RecoveryData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		log.Printf("[recovery] load decode error: %v", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{"exists": false})
		return
	}

	log.Printf("[recovery] found autosave from %s", data.SavedAt)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"exists":       true,
		"content":      data.Content,
		"filePath":     data.FilePath,
		"editorMode":   data.EditorMode,
		"savedAt":      data.SavedAt,
		"storyType":    data.StoryType,
		"storySort":    data.StorySort,
		"storyIndex":   data.StoryIndex,
		"storyChapter": data.StoryChapter,
		"storySource":  data.StorySource,
	})
}

func (h *Handler) RecoveryClear(w http.ResponseWriter, r *http.Request) {
	log.Printf("[recovery] clearing autosave")
	os.Remove(h.recoveryPath())
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
