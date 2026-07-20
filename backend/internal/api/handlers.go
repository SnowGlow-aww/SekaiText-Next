package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/fsutil"
	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	cfg               *config.AppConfig
	lm                *service.ListManager
	editor            *service.EditorService
	jsonLoader        *service.JsonLoaderService
	fb                *service.FlashbackAnalyzer
	dl                *service.Downloader
	progress          *service.ProgressTracker
	logBuf            *service.LogBuffer
	glossary          *service.GlossaryStore
	plugins           *service.PluginStore
	market            *service.MarketService
	appUpdate         *service.AppUpdateService
	team              *service.TeamService
	engine            *service.EngineManager
	voiceAlign        *service.VoiceAligner
	downloadTasks     sync.Map // map[string]*model.DownloadTaskProgress
	appUpdateMu       sync.Mutex
	saveDirMu         sync.RWMutex
	saveDirGeneration atomic.Uint64
	writeSettingsFile func(string, []byte, os.FileMode) error
	moveFileNoReplace func(string, string) error
	live2dSyncMu      sync.Mutex
	live2dSyncTasks   map[string]*live2dSyncTask
	live2dSyncRoots   map[string]string
}

// NewHandler creates a new Handler with all services initialized.
func NewHandler(cfg *config.AppConfig, logBuf *service.LogBuffer) *Handler {
	lm := service.NewListManager(cfg.CatalogDir)
	fb := service.NewFlashbackAnalyzer(lm)
	dl := service.NewDownloader(cfg.DataDir)
	jsonLoader := service.NewJsonLoaderService(fb)
	jsonLoader.SetSourceLocator(dl, cfg.DataDir)
	pluginStore := service.NewPluginStore(cfg.PluginsDir)
	h := &Handler{
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
		appUpdate:  service.NewAppUpdateService(cfg.DataDir),
		team:       service.NewTeamService(cfg.DataDir),
		engine:     service.NewEngineManager(cfg.EnginePath, cfg.FfmpegPath, filepath.Join(cfg.DataBaseDir, "logs")),
		voiceAlign: service.NewVoiceAligner(cfg.DataDir, cfg.FfmpegPath),
	}
	h.startDownloadTaskGC()
	h.startLive2DTaskGC()
	h.cleanupUpdateLaunchDirs()
	h.logInterruptedSaveDirMigration()
	// 让「下载源」设置（CDN 加速 / GitHub 直连）在启动时即生效。
	if s, err := h.loadSettings(); err == nil {
		service.SetDownloadMirror(s.DownloadMirror)
	}
	return h
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
	resp.IndexLabel = h.lm.IndexLabel(req.StoryType, req.Sort, req.Index)

	// Card-story scenario JSON often carries a broken / Japanese internal
	// ScenarioId (e.g. "★4冬弥・泉_前半") that does NOT match the on-CDN voice
	// folder name. The voice clips instead live under the scenario ASSET base name
	// (e.g. 012043_touya01) — the last path segment of the download URL — which is
	// also a clean "\d{6}_name" id that cardScenarioRe matches. Use it as the
	// scenarioId so VoiceURL can build a resolvable card_scenario / partvoice path.
	// Only cards are remapped; event / main scenario ids are already correct.
	if strings.Contains(req.StoryType, "卡面") {
		if name := scenarioAssetName(path.URL); name != "" {
			resp.ScenarioID = name
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// scenarioAssetName extracts the scenario asset's base name (no directory, no
// extension) from its download URL, e.g.
//
//	".../character/member/res012_no043/012043_touya01.asset" -> "012043_touya01".
//
// Uses plain string ops (not filepath) so URL "/" separators are handled the
// same on Windows as on macOS/Linux.
func scenarioAssetName(rawURL string) string {
	u := rawURL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndexByte(u, '/'); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.LastIndexByte(u, '.'); i >= 0 {
		u = u[:i]
	}
	return u
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
	storyType, index, indexLabel, chapter, ok := h.lm.ResolveLabel(req.Label)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         ok,
		"storyType":  storyType,
		"index":      index,
		"indexLabel": indexLabel,
		"chapter":    chapter,
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
	pathGeneration := h.saveDirGeneration.Load()
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.saveDirMu.RLock()
	defer h.saveDirMu.RUnlock()
	if pathGeneration != h.saveDirGeneration.Load() {
		writeError(w, http.StatusConflict, "save path changed during a directory migration; retry with the current path")
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

// RenameFile renames a saved document in place when its canonical name changes
// (mode label / translated title). Refuses to overwrite a different existing
// file — the caller falls back to writing the old path, so content is never lost.
func (h *Handler) RenameFile(w http.ResponseWriter, r *http.Request) {
	pathGeneration := h.saveDirGeneration.Load()
	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OldPath == "" || req.NewPath == "" {
		writeError(w, http.StatusBadRequest, "oldPath/newPath required")
		return
	}
	h.saveDirMu.RLock()
	defer h.saveDirMu.RUnlock()
	if pathGeneration != h.saveDirGeneration.Load() {
		writeError(w, http.StatusConflict, "save path changed during a directory migration; retry with the current path")
		return
	}
	if req.OldPath == req.NewPath {
		writeJSON(w, http.StatusOK, map[string]string{"path": req.NewPath})
		return
	}
	if _, err := os.Stat(req.NewPath); err == nil {
		writeError(w, http.StatusConflict, "target already exists")
		return
	} else if !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, "target check failed: "+err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(req.NewPath), 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "create dir failed: "+err.Error())
		return
	}
	move := fsutil.MoveFileNoReplace
	if h.moveFileNoReplace != nil {
		move = h.moveFileNoReplace
	}
	if err := move(req.OldPath, req.NewPath); errors.Is(err, os.ErrExist) {
		writeError(w, http.StatusConflict, "target already exists")
		return
	} else if err != nil {
		log.Printf("[rename-file] rename error: %v", err)
		writeError(w, http.StatusInternalServerError, "rename failed: "+err.Error())
		return
	}
	// 跨目录改名（索引标签修正后归位文件夹）会留下空的旧目录——只删空目录，
	// 非空时 Remove 自身失败，绝不误删内容。
	if filepath.Dir(req.OldPath) != filepath.Dir(req.NewPath) {
		_ = os.Remove(filepath.Dir(req.OldPath))
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": req.NewPath})
}

func (h *Handler) TranslationSave(w http.ResponseWriter, r *http.Request) {
	// Capture before decoding or waiting on saveDirMu. A migration that overtakes
	// this already-queued request advances the generation and makes its old path
	// stale instead of letting it recreate/write the retired directory.
	pathGeneration := h.saveDirGeneration.Load()
	var req model.TranslationSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[save] decode error: %v", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.saveDirMu.RLock()
	defer h.saveDirMu.RUnlock()
	if pathGeneration != h.saveDirGeneration.Load() {
		writeError(w, http.StatusConflict, "save path changed during a directory migration; retry with the current path")
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
	// Atomic write (temp + fsync + rename), same as the autosave path: a plain
	// os.WriteFile O_TRUNCs the user's translation file FIRST, so a crash /
	// disk-full / kill mid-write destroys the only copy of their work. With the
	// rename the previous file stays intact until the new content is durable.
	if err := fsutil.WriteFileAtomic(req.FilePath, []byte(content), 0644); err != nil {
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
	"https://sakimizuki.accr.cc/", // project edge CDN (mirror-caches model bodies from exmeaning)
	"https://storage2.exmeaning.com/",
	"https://storage.exmeaning.com/",
	"https://storage.sekai.best/",
	"https://assets.unipjsk.com/",
	"https://sekai-assets-bdf29c81.seiunx.net/",
}

// live2dCDNUpstream rewrites an exmeaning asset URL to the project's edge CDN so
// runtime playback fetches go through the mirror cache (the CDN falls back to
// exmeaning on a miss). 例外：/sound/ 音频路径（语音/BGM）一律直连 exmeaning——
// 镜像回源会把音频持久化进自家 OSS 桶白吃存储（桶里曾因此长出 43MB 的
// sekai-jp-assets/sound/；用户拍板：背景等图片可以镜像，只有音频不写）。
// Non-exmeaning URLs (sekai.best model_list/motion) pass through unchanged.
func live2dCDNUpstream(url string) string {
	const exm = "https://storage2.exmeaning.com/"
	const cdn = "https://sakimizuki.accr.cc/"
	if rest, ok := strings.CutPrefix(url, exm); ok && !strings.Contains(rest, "/sound/") {
		return cdn + rest
	}
	return url
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
	if !live2dHostAllowed(url) {
		writeError(w, http.StatusForbidden, "url host not allowed")
		return
	}

	// Try the local mirror first.
	if local := live2dLocalPath(h.cfg.Live2DLocalDir, url); local != "" {
		if info, err := os.Stat(local); err == nil && !info.IsDir() && info.Size() > 0 {
			if info.Size() > maxLive2DAssetBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "live2d asset exceeds response limit")
				return
			}
			if live2dCachedFileValid(local, url) {
				f, err := os.Open(local)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "open local live2d asset: "+err.Error())
					return
				}
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

	// Fetch through the redirect-guarded client (live2dSyncHTTP): the host allowlist
	// above only vets the INITIAL url, so a compromised/misconfigured CDN returning a
	// 3xx to an internal address (169.254.169.254, 127.0.0.1, …) would otherwise be
	// followed by the shared downloader — the same SSRF the sync path guards against.
	// live2dSyncHTTP re-runs live2dHostAllowed on every redirect hop.
	resp, err := live2dSyncHTTP.Get(live2dCDNUpstream(url))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream fetch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode))
		return
	}
	body, err := readLive2DBoundedBody(resp.Body, resp.ContentLength, maxLive2DAssetBytes)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream asset invalid: "+err.Error())
		return
	}
	if !live2dAssetBodyValid(url, body) {
		writeError(w, http.StatusBadGateway, "upstream response content does not match asset type")
		return
	}

	w.Header().Set("Content-Type", live2dContentType(url))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Live2D-Source", "cdn")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// live2dContentType picks a Content-Type for a locally-served Live2D asset.
func live2dContentType(path string) string {
	if u, err := neturl.Parse(path); err == nil && u.Path != "" {
		path = u.Path
	}
	path = strings.ToLower(path)
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

// cardScenarioRe matches card-story (活动卡面 / member card episode) scenario IDs,
// which are 6 digits + "_" + name (e.g. 013056_tsukasa01). Event / main / world-link
// scenario IDs start with letters (wl_, event_, ...), so they never match.
var cardScenarioRe = regexp.MustCompile(`^\d{6}_`)

func (h *Handler) VoiceURL(w http.ResponseWriter, r *http.Request) {
	scenarioID := r.URL.Query().Get("scenarioId")
	voiceID := r.URL.Query().Get("voiceId")
	chara2d, _ := strconv.Atoi(r.URL.Query().Get("chara2d"))

	// Voice audio is always served from the moesekai-jp mirror regardless of the
	// story's selected source. The default source (HarukiBot NEO) and unipjsk do
	// not host voice clips at all, and moesekai-jp is a full JP mirror, so routing
	// every voice request here is the only reliable option.
	baseURL := "https://storage.exmeaning.com/sekai-jp-assets/"

	// Voice clips live under different directories depending on the line, not the
	// story type. Verified against storage.exmeaning.com / storage.sekai.best:
	//   - any "partvoice_*" line -> a shared per-speaking-character bundle
	//       sound/scenario/voice/part_voice_{assetName}_{unit}/{vid}.mp3, keyed by
	//       the talking character's chara2d (resolved via the character2ds table).
	//       This is checked FIRST and independently of the story type, because a
	//       partvoice can appear in card, event or main stories alike.
	//   - card scenario ids (\d{6}_name) -> sound/card_scenario/voice/{sid}/{vid}.mp3
	//   - everything else                -> sound/scenario/voice/{sid}/{vid}.mp3
	// (Card scenarioIds reach here as the asset base name, set in StoryLoad, not the
	// raw JSON ScenarioId — see scenarioAssetName.)
	var url string
	switch {
	case strings.HasPrefix(voiceID, "partvoice"):
		if c, ok := service.Character2dByID(chara2d); ok {
			url = baseURL + "sound/scenario/voice/part_voice_" + c.AssetName + "_" + c.Unit + "/" + voiceID + ".mp3"
		} else {
			url = ""
		}
	case cardScenarioRe.MatchString(scenarioID):
		url = baseURL + "sound/card_scenario/voice/" + scenarioID + "/" + voiceID + ".mp3"
	default:
		url = baseURL + "sound/scenario/voice/" + scenarioID + "/" + voiceID + ".mp3"
	}
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

	speakers := []model.SpeakerEntry{} // non-nil so the JSON is [] not null (FE does .map on it)
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
	if err := os.MkdirAll(h.cfg.CatalogDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	write := fsutil.WriteFileAtomic
	if h.writeSettingsFile != nil {
		write = h.writeSettingsFile
	}
	writeErr := write(h.settingsPath(), data, 0644)
	if writeErr == nil || fsutil.IsWriteCommitted(writeErr) {
		service.SetDownloadMirror(s.DownloadMirror) // 下载源切换即时生效，无需重启
	}
	return writeErr
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
	// Guard both containment directions: walking either tree while writing into
	// the other can recurse into newly-created output or remove the mirror itself.
	if src == dst || pathWithin(dst, src) || pathWithin(src, dst) {
		writeError(w, http.StatusBadRequest, "cannot import this folder into itself")
		return
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "create target failed: "+err.Error())
		return
	}
	unlockRoot, err := live2dLockPath(r.Context(), dst)
	if err != nil {
		writeError(w, http.StatusRequestTimeout, "import canceled: "+err.Error())
		return
	}
	defer unlockRoot()
	if overlap, err := pathsPhysicallyOverlap(src, dst); err != nil {
		writeError(w, http.StatusBadRequest, "cannot verify import paths: "+err.Error())
		return
	} else if overlap {
		writeError(w, http.StatusBadRequest, "cannot import this folder into itself")
		return
	}

	base := filepath.Base(src)
	moved := 0
	// If the user picked a `model` or `motion` folder directly, move it under the
	// matching subdir; otherwise move every top-level entry into the target root
	// (covers both the asset root containing model/motion/model_list.json and any
	// loose layout).
	if base == "model" || base == "motion" {
		if err := mergeMove(r.Context(), src, filepath.Join(dst, base)); err != nil {
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
			if err := mergeMove(r.Context(), filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				writeError(w, http.StatusInternalServerError, "import failed at "+e.Name()+": "+err.Error())
				return
			}
			moved++
		}
		// The now-empty source folder is removed for the "move" semantics.
		_ = os.Remove(src)
	}
	log.Printf("[live2d-import] moved %d entries from %s into %s", moved, src, dst)
	writeJSON(w, http.StatusOK, map[string]interface{}{"dir": dst, "moved": moved})
}

// mergeMove moves src to dst, merging directories recursively. File replacement
// is always atomic; a cross-volume move copies through a sibling temporary file
// before removing the source, so an interrupted import retains both the previous
// destination and the source asset.
func mergeMove(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	si, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if si.IsDir() {
		if di, err := os.Lstat(dst); err == nil && !di.IsDir() {
			return fmt.Errorf("target is not a directory: %s", dst)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(dst, si.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := mergeMove(ctx, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Remove(src) // now empty
	}
	if !si.Mode().IsRegular() {
		return fmt.Errorf("unsupported asset type: %s", src)
	}

	unlock, err := live2dLockPath(ctx, dst)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := fsutil.CopyFileAtomic(ctx, src, dst, si.Mode().Perm()); err != nil {
		return err
	}
	return os.Remove(src)
}

// OpenURL opens an external http/https link in the system browser. The Tauri
// webview has no window.open/target=_blank handler, so外链全部走这里。Scheme is
// whitelisted so a page can't launch arbitrary local protocols/executables.
func (h *Handler) OpenURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	u, err := neturl.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "仅支持 http/https 链接")
		return
	}
	target := u.String()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "open failed: "+err.Error())
		return
	}
	go func() { _ = cmd.Wait() }() // reap the launcher so it doesn't linger as a zombie
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	go func() { _ = cmd.Wait() }() // reap the launcher so it doesn't linger as a zombie
	writeJSON(w, http.StatusOK, map[string]string{"dir": dir})
}

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.loadSettings()
	if err != nil {
		s = model.DefaultSettings()
	}
	// SaveBaseDir must resolve per-user at runtime (DefaultSettings can't hard-
	// code a machine path). Filled here — not persisted until the user saves
	// settings — it makes 自动建档/autosave and the layered save default work out
	// of the box instead of silently doing nothing while the setting is empty.
	if s.SaveBaseDir == "" {
		s.SaveBaseDir = defaultSaveBaseDir()
	}
	writeJSON(w, http.StatusOK, s)
}

// resolveSaveBaseDir 返回当前生效的译文保存根目录（空设置回填默认值）。
func (h *Handler) resolveSaveBaseDir() string {
	s, err := h.loadSettings()
	if err != nil {
		s = model.DefaultSettings()
	}
	return effectiveSaveBaseDir(s)
}

func effectiveSaveBaseDir(s model.Settings) string {
	if s.SaveBaseDir != "" {
		return s.SaveBaseDir
	}
	return defaultSaveBaseDir()
}

// OpenSaveDir 在系统文件管理器中打开译文保存根目录（顶栏「文稿目录」按钮）。
// 目录还没生成时先建好再打开，首次点击也能落到正确位置。
func (h *Handler) OpenSaveDir(w http.ResponseWriter, r *http.Request) {
	dir := h.resolveSaveBaseDir()
	if dir == "" {
		writeError(w, http.StatusInternalServerError, "无法确定保存目录")
		return
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[open-save-dir] mkdir error: %v", err)
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
		writeError(w, http.StatusInternalServerError, "open failed: "+err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()
	writeJSON(w, http.StatusOK, map[string]string{"dir": dir})
}

// MigrateSaveDir copies and verifies translations before switching settings.
// The old tree is intentionally retained: verification followed by deletion has
// an unavoidable source-change race, so cleanup is left to the user. Translation
// saves and settings writes serialize with this transaction through saveDirMu.
func (h *Handler) MigrateSaveDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NewDir string `json:"newDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.NewDir) == "" {
		writeError(w, http.StatusBadRequest, "newDir required")
		return
	}
	newAbs, err := filepath.Abs(strings.TrimSpace(req.NewDir))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid newDir: "+err.Error())
		return
	}
	h.saveDirMu.Lock()
	defer h.saveDirMu.Unlock()
	settings, settingsErr := h.loadSettings()
	if settingsErr != nil {
		if !errors.Is(settingsErr, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "无法读取当前设置，迁移已中止: "+settingsErr.Error())
			return
		}
		settings = model.DefaultSettings()
	}

	oldAbs := ""
	if d := effectiveSaveBaseDir(settings); d != "" {
		if a, err := filepath.Abs(d); err == nil {
			oldAbs = a
		}
	}
	oldExists := false
	if oldAbs != "" {
		info, statErr := os.Stat(oldAbs)
		switch {
		case statErr == nil && !info.IsDir():
			writeError(w, http.StatusBadRequest, "旧保存路径不是目录")
			return
		case statErr == nil:
			oldExists = true
		case !os.IsNotExist(statErr):
			writeError(w, http.StatusBadRequest, "无法读取旧保存目录: "+statErr.Error())
			return
		}
	}
	samePhysical := oldAbs != "" && oldAbs == newAbs
	if oldExists && !samePhysical {
		if newInfo, statErr := os.Stat(newAbs); statErr == nil {
			if !newInfo.IsDir() {
				writeError(w, http.StatusBadRequest, "新保存路径不是目录")
				return
			}
			oldPhysical, oldErr := physicalFilesystemPath(oldAbs)
			newPhysical, newErr := physicalFilesystemPath(newAbs)
			if err := errors.Join(oldErr, newErr); err != nil {
				writeError(w, http.StatusBadRequest, "无法验证新旧目录: "+err.Error())
				return
			}
			samePhysical = oldPhysical == newPhysical
		} else if !os.IsNotExist(statErr) {
			writeError(w, http.StatusBadRequest, "无法读取新保存目录: "+statErr.Error())
			return
		}
	}
	// A configured path that never existed is an empty source, not an overlap.
	if oldExists && !samePhysical && (pathWithin(newAbs, oldAbs) || pathWithin(oldAbs, newAbs)) {
		writeError(w, http.StatusBadRequest, "新旧保存目录不能互相包含")
		return
	}
	journal := saveDirMigrationJournal{
		Version:   1,
		OldDir:    oldAbs,
		NewDir:    newAbs,
		Phase:     "copying",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := h.writeSaveDirMigrationJournal(journal); err != nil {
		writeError(w, http.StatusInternalServerError, "migration journal failed: "+err.Error())
		return
	}
	if err := os.MkdirAll(newAbs, 0755); err != nil {
		journal.Phase, journal.Error = "failed", err.Error()
		_ = h.writeSaveDirMigrationJournal(journal)
		writeError(w, http.StatusInternalServerError, "无法创建新目录: "+err.Error())
		return
	}
	if oldExists && !samePhysical {
		oldPhysical, oldErr := physicalFilesystemPath(oldAbs)
		newPhysical, newErr := physicalFilesystemPath(newAbs)
		if err := errors.Join(oldErr, newErr); err != nil {
			journal.Phase, journal.Error = "failed", err.Error()
			_ = h.writeSaveDirMigrationJournal(journal)
			writeError(w, http.StatusBadRequest, "无法验证新旧目录: "+err.Error())
			return
		}
		samePhysical = oldPhysical == newPhysical
		if !samePhysical && (pathWithin(oldPhysical, newPhysical) || pathWithin(newPhysical, oldPhysical)) {
			journal.Phase, journal.Error = "failed", "physical paths overlap"
			_ = h.writeSaveDirMigrationJournal(journal)
			writeError(w, http.StatusBadRequest, "新旧保存目录不能互相包含")
			return
		}
	}
	result := newSaveDirCopyResult()
	if oldExists && !samePhysical {
		var err error
		result, err = copySaveDir(r.Context(), oldAbs, newAbs)
		if err != nil {
			journal.Phase, journal.Error = "failed", err.Error()
			_ = h.writeSaveDirMigrationJournal(journal)
			writeError(w, http.StatusInternalServerError, "migration copy failed: "+err.Error())
			return
		}
	}
	journal.Phase = "verified"
	journal.SkippedPaths = append([]string(nil), result.skippedPaths...)
	if err := h.writeSaveDirMigrationJournal(journal); err != nil {
		writeError(w, http.StatusInternalServerError, "migration journal failed: "+err.Error())
		return
	}
	settings.SaveBaseDir = newAbs
	settingsErr = h.saveSettings(settings)
	settingsWarning := ""
	if settingsErr != nil && !fsutil.IsWriteCommitted(settingsErr) {
		journal.Phase, journal.Error = "failed", settingsErr.Error()
		_ = h.writeSaveDirMigrationJournal(journal)
		writeError(w, http.StatusInternalServerError, "settings save failed: "+settingsErr.Error())
		return
	}
	if settingsErr != nil {
		settingsWarning = "settings committed, but directory durability confirmation failed: " + settingsErr.Error()
		log.Printf("[migrate-save-dir] %s", settingsWarning)
	}
	h.saveDirGeneration.Add(1)
	journal.Phase = "complete"
	if err := h.writeSaveDirMigrationJournal(journal); err != nil {
		log.Printf("[migrate-save-dir] record complete phase: %v", err)
	}
	if err := os.Remove(h.saveDirMigrationJournalPath()); err != nil && !os.IsNotExist(err) {
		log.Printf("[migrate-save-dir] remove journal: %v", err)
	}
	moved, skipped := result.counts()
	log.Printf("[migrate-save-dir] %s -> %s (copied %d, skipped %d, source retained)", oldAbs, newAbs, moved, skipped)
	response := map[string]interface{}{
		"oldDir": oldAbs, "newDir": newAbs, "moved": moved, "skipped": skipped, "skippedPaths": result.skippedPaths,
		"sourceRetained": oldExists && !samePhysical,
	}
	if settingsWarning != "" {
		response["warning"] = settingsWarning
	}
	writeJSON(w, http.StatusOK, response)
}

type saveDirMigrationJournal struct {
	Version      int      `json:"version"`
	OldDir       string   `json:"oldDir"`
	NewDir       string   `json:"newDir"`
	Phase        string   `json:"phase"`
	StartedAt    string   `json:"startedAt"`
	SkippedPaths []string `json:"skippedPaths,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type saveDirCopyResult struct {
	copiedFiles  []string
	dirs         []string
	skippedPaths []string
	topCopied    map[string]bool
	topSkipped   map[string]bool
}

func newSaveDirCopyResult() *saveDirCopyResult {
	return &saveDirCopyResult{topCopied: make(map[string]bool), topSkipped: make(map[string]bool)}
}

func (r *saveDirCopyResult) counts() (moved, skipped int) {
	for top := range r.topCopied {
		if !r.topSkipped[top] {
			moved++
		}
	}
	return moved, len(r.topSkipped)
}

func copySaveDir(ctx context.Context, srcRoot, dstRoot string) (*saveDirCopyResult, error) {
	result := newSaveDirCopyResult()
	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if err := copySaveDirEntry(ctx, filepath.Join(srcRoot, entry.Name()), filepath.Join(dstRoot, entry.Name()), entry.Name(), entry.Name(), result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func copySaveDirEntry(ctx context.Context, src, dst, rel, top string, result *saveDirCopyResult) error {
	return copySaveDirEntryWithCopy(ctx, src, dst, rel, top, result, fsutil.CopyFileNoReplaceAtomic)
}

func copySaveDirEntryWithCopy(
	ctx context.Context,
	src, dst, rel, top string,
	result *saveDirCopyResult,
	copyFile func(context.Context, string, string, os.FileMode) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if dstInfo, err := os.Lstat(dst); err == nil && !dstInfo.IsDir() {
			return fmt.Errorf("target type conflict at %s", rel)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		result.dirs = append(result.dirs, rel)
		result.topCopied[top] = true
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			childRel := rel + "/" + entry.Name()
			if err := copySaveDirEntryWithCopy(ctx, filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()), childRel, top, result, copyFile); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported source type at %s", rel)
	}
	if dstInfo, err := os.Lstat(dst); err == nil {
		if dstInfo.IsDir() {
			return fmt.Errorf("target type conflict at %s", rel)
		}
		result.skippedPaths = append(result.skippedPaths, filepath.ToSlash(rel))
		result.topSkipped[top] = true
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := copyFile(ctx, src, dst, info.Mode().Perm()); errors.Is(err, os.ErrExist) {
		result.skippedPaths = append(result.skippedPaths, filepath.ToSlash(rel))
		result.topSkipped[top] = true
		return nil
	} else if err != nil {
		return err
	}
	if err := verifySameFile(src, dst); err != nil {
		return fmt.Errorf("verify %s: %w", rel, err)
	}
	result.copiedFiles = append(result.copiedFiles, rel)
	result.topCopied[top] = true
	return nil
}

func verifySameFile(a, b string) error {
	aSum, aSize, err := fileDigest(a)
	if err != nil {
		return err
	}
	bSum, bSize, err := fileDigest(b)
	if err != nil {
		return err
	}
	if aSize != bSize || aSum != bSum {
		return fmt.Errorf("content mismatch")
	}
	return nil
}

func fileDigest(path string) ([sha256.Size]byte, int64, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, 0, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(hash, f)
	closeErr := f.Close()
	if copyErr != nil {
		return sum, n, copyErr
	}
	if closeErr != nil {
		return sum, n, closeErr
	}
	copy(sum[:], hash.Sum(nil))
	return sum, n, nil
}

func pathWithin(path, parent string) bool {
	path, parent = comparableFilesystemPath(path), comparableFilesystemPath(parent)
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func comparableFilesystemPath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

func pathsPhysicallyOverlap(a, b string) (bool, error) {
	a, err := physicalFilesystemPath(a)
	if err != nil {
		return false, err
	}
	b, err = physicalFilesystemPath(b)
	if err != nil {
		return false, err
	}
	return a == b || pathWithin(a, b) || pathWithin(b, a), nil
}

func physicalFilesystemPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return comparableFilesystemPath(resolved), nil
}

func (h *Handler) saveDirMigrationJournalPath() string {
	return filepath.Join(h.cfg.CatalogDir, "save-dir-migration.json")
}

func (h *Handler) writeSaveDirMigrationJournal(journal saveDirMigrationJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(h.saveDirMigrationJournalPath(), data, 0o644)
}

func (h *Handler) logInterruptedSaveDirMigration() {
	data, err := os.ReadFile(h.saveDirMigrationJournalPath())
	if err != nil {
		return
	}
	var journal saveDirMigrationJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		log.Printf("[migrate-save-dir] unreadable migration journal: %v", err)
		return
	}
	log.Printf("[migrate-save-dir] interrupted migration detected: phase=%s old=%s new=%s error=%s", journal.Phase, journal.OldDir, journal.NewDir, journal.Error)
}

// defaultSaveBaseDir picks a user-visible home for translation output:
// ~/Documents/SekaiText when Documents exists (macOS/Windows), else ~/SekaiText.
func defaultSaveBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	docs := filepath.Join(home, "Documents")
	if info, err := os.Stat(docs); err == nil && info.IsDir() {
		return filepath.Join(docs, "SekaiText")
	}
	return filepath.Join(home, "SekaiText")
}

func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var s model.Settings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.saveDirMu.Lock()
	defer h.saveDirMu.Unlock()
	oldSettings, oldErr := h.loadSettings()
	if oldErr != nil {
		oldSettings = model.DefaultSettings()
	}
	saveErr := h.saveSettings(s)
	if saveErr != nil && !fsutil.IsWriteCommitted(saveErr) {
		writeError(w, http.StatusInternalServerError, "settings save failed: "+saveErr.Error())
		return
	}
	if !sameConfiguredSaveDir(effectiveSaveBaseDir(oldSettings), effectiveSaveBaseDir(s)) {
		h.saveDirGeneration.Add(1)
	}
	if saveErr != nil {
		warning := "settings committed, but directory durability confirmation failed: " + saveErr.Error()
		log.Printf("[settings] %s", warning)
		writeJSON(w, http.StatusOK, struct {
			model.Settings
			Warning string `json:"warning"`
		}{Settings: s, Warning: warning})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func sameConfiguredSaveDir(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr == nil && bErr == nil {
		a, b = aAbs, bAbs
	}
	return comparableFilesystemPath(a) == comparableFilesystemPath(b)
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
			task.Mu.Lock()
			task.Read = read
			task.Total = total
			task.Mu.Unlock()
		})
		task.Mu.Lock()
		if err != nil {
			task.Status = "error"
			task.Error = err.Error()
		} else {
			task.Status = "done"
			task.FilePath = dlPath
		}
		task.FinishedAt = time.Now().UnixNano()
		task.Mu.Unlock()
	}()

	writeJSON(w, http.StatusOK, map[string]string{"taskId": taskID})
}

// ExportStoryOriginalTxt 一键导出「原文 txt」：把选中章节的剧情 JSON 下到 dataDir 缓存
// （不占用户输出目录）后解析，译文槽位直接填日文原文，用与正式翻译档同一个序列化器
// （场景行裸文本+空行分隔、对话行「说话人：」前缀、CRLF）写 <输出目录>/<json同名>.txt。
// 用一个不带 FlashbackAnalyzer 的裸解析器：闪回注解对纯文本导出无意义，还会级联下载
// 一堆来源剧本拖慢响应。
func (h *Handler) ExportStoryOriginalTxt(w http.ResponseWriter, r *http.Request) {
	var req model.JsonDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OutputDir == "" {
		writeError(w, http.StatusBadRequest, "outputDir 必填")
		return
	}
	path := h.lm.GetJsonPath(req.StoryType, req.Sort, req.Index, req.Chapter, req.Source)
	if path.URL == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf("story not found: type=%s index=%s chapter=%d source=%s", req.StoryType, req.Index, req.Chapter, req.Source))
		return
	}
	jsonPath, err := h.dl.DownloadJSON(path.URL, path.FileName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "下载剧情 JSON 失败: "+err.Error())
		return
	}
	resp, err := service.NewJsonLoaderService(nil).ParseFile(jsonPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	content := h.editor.SerializeContent(service.SourceTalksAsDst(resp.SourceTalks), false)
	name := strings.TrimSuffix(path.FileName, filepath.Ext(path.FileName)) + ".txt"
	outPath := filepath.Join(req.OutputDir, name)
	if err := os.MkdirAll(req.OutputDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "create dir failed: "+err.Error())
		return
	}
	if err := fsutil.WriteFileAtomic(outPath, []byte(content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "file write failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"filePath": outPath})
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

	// Snapshot the mutable fields under the lock so we don't race with the
	// download goroutine while encoding/reading Status. Use a separate value
	// (no embedded mutex) so encoding happens off-lock and go vet is happy.
	task.Mu.Lock()
	snap := struct {
		TaskID   string `json:"taskId"`
		Status   string `json:"status"`
		Read     int64  `json:"read"`
		Total    int64  `json:"total"`
		FilePath string `json:"filePath,omitempty"`
		Error    string `json:"error,omitempty"`
	}{
		TaskID:   task.TaskID,
		Status:   task.Status,
		Read:     task.Read,
		Total:    task.Total,
		FilePath: task.FilePath,
		Error:    task.Error,
	}
	task.Mu.Unlock()

	// Terminal tasks are deliberately NOT deleted here. Coupling cleanup to "a
	// poll happened to observe the terminal state" both leaked tasks (the frontend
	// stops polling before/after the task finishes) and turned the first
	// observer's Delete into spurious 404s for concurrent/retried polls. A
	// time-based background GC (startDownloadTaskGC) reaps stale tasks instead,
	// keeping terminal ones pollable for a grace window.
	writeJSON(w, http.StatusOK, snap)
}

const (
	// downloadTaskGrace is how long a terminal (done/error) task lingers, measured
	// from creation, before the GC reaps it — long enough that overlapping or
	// retried progress polls after completion still get the final snapshot instead
	// of a 404.
	downloadTaskGrace = 5 * time.Minute
	// downloadTaskMaxAge is a hard cap for any task (e.g. one whose download
	// goroutine wedged and never reached a terminal state) so the shared table
	// cannot grow without bound.
	downloadTaskMaxAge = 60 * time.Minute
	// downloadTaskSweep is how often the GC scans the task table.
	downloadTaskSweep = 1 * time.Minute
)

// taskCreatedAt recovers a download task's creation time from its ID, which both
// creators (story JSON download + app self-update) set to the base-36 UnixNano
// stamp at creation. Returns the zero time (treated as "very old", so reaped) if
// the ID isn't a parseable stamp.
func taskCreatedAt(taskID string) time.Time {
	ns, err := strconv.ParseInt(taskID, 36, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// startDownloadTaskGC periodically reaps stale entries from the shared
// downloadTasks table (used by both story JSON and app self-update downloads).
// Cleanup is time-based rather than coupled to a progress poll observing the
// terminal state, so entries can't leak when the frontend stops polling, while
// terminal tasks stay pollable for downloadTaskGrace.
func (h *Handler) startDownloadTaskGC() {
	go func() {
		ticker := time.NewTicker(downloadTaskSweep)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			h.downloadTasks.Range(func(key, val interface{}) bool {
				taskID, okKey := key.(string)
				task, okVal := val.(*model.DownloadTaskProgress)
				if !okKey || !okVal {
					h.downloadTasks.Delete(key)
					return true
				}
				age := now.Sub(taskCreatedAt(taskID))
				task.Mu.Lock()
				done := task.Status == "done"
				terminal := done || task.Status == "error"
				purpose := task.Purpose
				if terminal && task.FinishedAt > 0 {
					age = now.Sub(time.Unix(0, task.FinishedAt))
				}
				task.Mu.Unlock()
				// A completed app installer remains authorized for the lifetime of this
				// backend process; unlike story downloads there is at most one active
				// updater task, and deleting it makes a delayed Install click fail.
				if done && purpose == "app-update" {
					return true
				}
				if (terminal && age > downloadTaskGrace) || (!terminal && age > downloadTaskMaxAge) {
					h.downloadTasks.Delete(key)
				}
				return true
			})
		}
	}()
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
	if custom := filepath.Join(h.customChrDir(), "chr_"+indexStr+".png"); fileExists(custom) {
		iconPath = custom
	}
	// no-cache = revalidate every load (ServeFile answers 304 via mtime), so a
	// replaced texture shows up without restarting the webview.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, iconPath)
}

// customChrDir is where user-imported character avatar textures live; its
// existence is the whole "custom avatars active" state (no settings entry).
func (h *Handler) customChrDir() string {
	dir := h.cfg.DataBaseDir
	if dir == "" {
		dir = h.cfg.DataDir
	}
	return filepath.Join(dir, "images", "chr-custom")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (h *Handler) CharacterIconCustomStatus(w http.ResponseWriter, r *http.Request) {
	count := 0
	for i := 1; i <= 31; i++ {
		if fileExists(filepath.Join(h.customChrDir(), "chr_"+strconv.Itoa(i)+".png")) {
			count++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": count > 0, "count": count})
}

// CharacterIconCustomImport copies chr_1.png..chr_31.png from a user-picked
// directory into customChrDir. Copy (not reference) so the source folder can be
// moved or deleted afterwards. The swap goes through a temp dir so a failed copy
// can't leave a half-replaced set.
func (h *Handler) CharacterIconCustomImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Dir) == "" {
		writeError(w, http.StatusBadRequest, "missing dir")
		return
	}
	tmp := h.customChrDir() + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	count := 0
	for i := 1; i <= 31; i++ {
		name := "chr_" + strconv.Itoa(i) + ".png"
		data, err := os.ReadFile(filepath.Join(req.Dir, name))
		if err != nil || len(data) == 0 || len(data) > 10<<20 {
			continue
		}
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o644); err != nil {
			os.RemoveAll(tmp)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		count++
	}
	if count == 0 {
		os.RemoveAll(tmp)
		writeError(w, http.StatusBadRequest, "所选文件夹中没有 chr_1.png ~ chr_31.png 命名的图片")
		return
	}
	if err := os.RemoveAll(h.customChrDir()); err != nil {
		os.RemoveAll(tmp)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(tmp, h.customChrDir()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "count": count})
}

func (h *Handler) CharacterIconCustomReset(w http.ResponseWriter, r *http.Request) {
	if err := os.RemoveAll(h.customChrDir()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": false, "count": 0})
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

var (
	debugQuotedSecretRE   = regexp.MustCompile(`(?i)(["']?(?:password|passwd|token|access[_-]?token|refresh[_-]?token|authorization|cookie|secret|api[_-]?key)["']?\s*[:=]\s*["'])[^"']*(["'])`)
	debugUnquotedSecretRE = regexp.MustCompile(`(?i)(\b(?:password|passwd|token|access[_-]?token|refresh[_-]?token|authorization|cookie|secret|api[_-]?key)\b\s*[:=]\s*)(?:Bearer\s+)?[^\s,;}&\]]+`)
	debugBearerRE         = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	debugMacUserRE        = regexp.MustCompile(`/Users/[^/\s]+`)
	debugWindowsUserRE    = regexp.MustCompile(`(?i)([A-Z]:\\Users\\)[^\\/\s]+`)
)

func redactDebugLine(line string) string {
	line = debugQuotedSecretRE.ReplaceAllString(line, `${1}[REDACTED]${2}`)
	line = debugUnquotedSecretRE.ReplaceAllString(line, `${1}[REDACTED]`)
	line = debugBearerRE.ReplaceAllString(line, `Bearer [REDACTED]`)
	line = debugMacUserRE.ReplaceAllString(line, `/Users/[USER]`)
	return debugWindowsUserRE.ReplaceAllString(line, `${1}[USER]`)
}

func (h *Handler) DebugSaveLogs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lines []string `json:"lines"`
	}
	if r.Body != nil {
		err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req)
		if err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if len(req.Lines) > 2000 {
		writeError(w, http.StatusBadRequest, "too many log lines")
		return
	}
	lines := req.Lines
	if len(lines) == 0 {
		for _, entry := range h.logBuf.Lines() {
			lines = append(lines, "["+entry.Timestamp+"] [server] "+entry.Message)
		}
	}

	// Write into the app's known-writable data dir, not the process CWD: a bare
	// relative "debug.log" lands in an unknown/unwritable place under the packaged
	// Tauri sidecar (CWD is often "/" on macOS), so os.Create fails or the file is
	// unreachable. Mirror OpenDataDir and hand the absolute path back to the UI.
	dir := h.cfg.DataBaseDir
	if dir == "" {
		dir = h.cfg.DataDir
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[debug-logs] mkdir error: %v", err)
		writeError(w, http.StatusInternalServerError, "log dir create failed: "+err.Error())
		return
	}
	logPath := filepath.Join(dir, "debug.log")
	var buf strings.Builder
	buf.WriteString("=== SekaiText Debug Log === " + time.Now().Format("2006-01-02 15:04:05") + " ===\n\n")
	for _, line := range lines {
		if len(line) > 16<<10 {
			line = line[:16<<10]
		}
		buf.WriteString(redactDebugLine(line) + "\n")
	}
	if err := fsutil.WriteFileAtomic(logPath, []byte(buf.String()), 0o644); err != nil {
		log.Printf("[debug-logs] write error: %v", err)
		writeError(w, http.StatusInternalServerError, "log file write failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "saved",
		"lines":  len(lines),
		"path":   logPath,
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
	if req.Version >= 2 && len(req.Modes) > 0 {
		data.Version = 2
		data.ActiveMode = req.ActiveMode
		data.Modes = make([]model.RecoveryModeData, 0, len(req.Modes))
		for _, mode := range req.Modes {
			data.Modes = append(data.Modes, model.RecoveryModeData{
				Content:           h.editor.SerializeContent(mode.Talks, req.SaveN),
				FilePath:          mode.FilePath,
				EditorMode:        mode.EditorMode,
				TitleOverride:     mode.TitleOverride,
				HasUnsavedChanges: mode.HasUnsavedChanges,
				SourceTalks:       mode.SourceTalks,
				DocMeta:           mode.DocMeta,
			})
		}
		active := &data.Modes[0]
		for i := range data.Modes {
			if data.Modes[i].EditorMode == req.ActiveMode {
				active = &data.Modes[i]
				break
			}
		}
		// Mirror the active slot into V1 fields. Recovery V2 therefore remains
		// discoverable by App.vue and readable by older single-mode clients.
		data.Content = active.Content
		data.FilePath = active.FilePath
		data.EditorMode = active.EditorMode
		if active.DocMeta != nil {
			data.StoryType = active.DocMeta.StoryType
			data.StorySort = active.DocMeta.Sort
			data.StoryIndex = active.DocMeta.Index
			data.StoryChapter = active.DocMeta.Chapter
			data.StorySource = active.DocMeta.Source
		}
	}

	// Encode fully in memory first, then write atomically (temp file + fsync +
	// rename) so a crash / disk-full / kill mid-write can never truncate the
	// previous good autosave or leave a half-written one — which is exactly the
	// moment recovery must survive. Previously os.Create's O_TRUNC destroyed the
	// old autosave up front and the Encode error was dropped while still reporting
	// "saved", silently corrupting the recovery point.
	buf, err := json.Marshal(data)
	if err != nil {
		log.Printf("[recovery] save encode error: %v", err)
		writeError(w, http.StatusInternalServerError, "autosave encode failed: "+err.Error())
		return
	}
	if err := fsutil.WriteFileAtomic(h.recoveryPath(), buf, 0644); err != nil {
		if !fsutil.IsWriteCommitted(err) {
			log.Printf("[recovery] save write error: %v", err)
			writeError(w, http.StatusInternalServerError, "autosave write failed: "+err.Error())
			return
		}
		// The rename already published the new recovery snapshot. Report success so
		// the browser commits the matching lossless sidecar instead of restoring a
		// previous sidecar that no longer corresponds to the backend file.
		log.Printf("[recovery] autosave committed with directory sync warning: %v", err)
	}
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
		"version":      data.Version,
		"activeMode":   data.ActiveMode,
		"modes":        data.Modes,
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
	if err := os.Remove(h.recoveryPath()); err != nil && !os.IsNotExist(err) {
		log.Printf("[recovery] clear error: %v", err)
		writeError(w, http.StatusInternalServerError, "autosave clear failed: "+err.Error())
		return
	}
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
