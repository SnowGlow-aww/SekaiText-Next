package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sekaitext/backend/internal/service"
)

// Engine endpoints drive the bundled SekaiCoreEngine sidecar: auto-timing (打轴)
// and video-suppress (压制). They follow the same trigger+poll job pattern as the
// JSON download endpoints (taskId -> a /progress poll -> terminal status).

func newTaskID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// sanitizeThreshold defends the .NET engine's GetDouble() — which throws on a
// non-number JSON value — from a malformed threshold object: it keeps only finite
// numeric entries and drops everything else, so a stray ""/null/string from any
// caller can't 500 the start. The plugin already coerces these client-side; this
// is belt-and-suspenders for other callers. A non-object threshold (nil, etc.)
// yields nil, letting the engine apply its built-in defaults.
func sanitizeThreshold(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, val := range m {
		switch n := val.(type) {
		case float64:
			if !math.IsNaN(n) && !math.IsInf(n, 0) {
				out[k] = n
			}
		case json.Number:
			if f, err := n.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				out[k] = f
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// engineStatusPingGate rate-limits the spawn-on-Ping in EngineStatus. Ping issues
// system.version, which lazily spawns the sidecar. A healthy engine stays started
// (ensureStarted is then a no-op, so repeated polls don't churn), but a binary that
// crashes on launch resets started=false via onExit, so pinging on every status poll
// would relaunch the crashing process endlessly. After a failed Ping we serve the
// cached error for pingFailCooldown before allowing another spawn attempt, bounding
// respawns of a broken binary to once per window while still letting a fixed engine
// recover on the next retry.
var engineStatusPingGate struct {
	mu      sync.Mutex
	failAt  time.Time
	failMsg string
}

const pingFailCooldown = 10 * time.Second

// EngineStatus reports whether the engine is bundled and, if so, its version.
func (h *Handler) EngineStatus(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeJSON(w, http.StatusOK, map[string]interface{}{"available": false})
		return
	}
	// Within the cooldown after a failed Ping, report the cached error without
	// re-pinging so a crash-on-start binary isn't respawned by every poll.
	engineStatusPingGate.mu.Lock()
	if !engineStatusPingGate.failAt.IsZero() && time.Since(engineStatusPingGate.failAt) < pingFailCooldown {
		msg := engineStatusPingGate.failMsg
		engineStatusPingGate.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"available": true, "ready": false, "error": msg})
		return
	}
	engineStatusPingGate.mu.Unlock()

	info, err := h.engine.Ping()
	if err != nil {
		engineStatusPingGate.mu.Lock()
		engineStatusPingGate.failAt = time.Now()
		engineStatusPingGate.failMsg = err.Error()
		engineStatusPingGate.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"available": true, "ready": false, "error": err.Error()})
		return
	}
	// Success — clear any prior failure backoff so a recovered engine reports ready.
	engineStatusPingGate.mu.Lock()
	engineStatusPingGate.failAt = time.Time{}
	engineStatusPingGate.failMsg = ""
	engineStatusPingGate.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"available": true, "ready": true, "engine": info})
}

// --- Auto-timing ---

func (h *Handler) EngineTimingStart(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return
	}
	var req struct {
		service.TimingParams
		Parallel bool `json:"parallel"` // true=并行模式（多任务并存）；false=老语义单飞
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.VideoPath == "" || req.ScriptPath == "" {
		writeError(w, http.StatusBadRequest, "videoPath 和 scriptPath 必填")
		return
	}
	if _, err := os.Stat(req.VideoPath); err != nil {
		writeError(w, http.StatusBadRequest, "视频文件不存在: "+req.VideoPath)
		return
	}
	if _, err := os.Stat(req.ScriptPath); err != nil {
		writeError(w, http.StatusBadRequest, "剧本文件不存在: "+req.ScriptPath)
		return
	}
	// Drop any non-numeric threshold entries before they reach the engine's
	// GetDouble() (which would otherwise throw and 500 the start).
	req.Threshold = sanitizeThreshold(req.Threshold)

	job, err := h.engine.StartTiming(newTaskID(), req.TimingParams, req.Parallel)
	if err != nil {
		if errors.Is(err, service.ErrTimingBusy) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "启动打轴失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"taskId": job.TaskID})
}

func (h *Handler) EngineTimingProgress(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	job, ok := h.engineTimingJob(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	job.Mu.Lock()
	snap := map[string]interface{}{
		"taskId":      job.TaskID,
		"status":      job.Status,
		"percent":     job.Percent,
		"fps":         job.Fps,
		"eta":         job.Eta,
		"dialogTotal":   job.DialogTotal,
		"bannerTotal":   job.BannerTotal,
		"markerTotal":   job.MarkerTotal,
		"matched":       job.Matched,
		"matchedDialog": job.MatchedDialog,
		"matchedBanner": job.MatchedBanner,
		"matchedMarker": job.MatchedMarker,
		"error":         job.Error,
	}
	job.Mu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

// EngineTimingPreview serves the latest preview frame (base64 jpeg) on its own
// endpoint so the frequent /progress poll stays small.
func (h *Handler) EngineTimingPreview(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	job, ok := h.engineTimingJob(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	job.Mu.Lock()
	b64 := job.PreviewB64
	job.Mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"base64": b64})
}

// EngineTimingExport pulls the assembled ASS from the engine and writes it under
// the data dir, returning the file path (ready to feed into 压制).
func (h *Handler) EngineTimingExport(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return
	}
	// Bind export to a specific finished task so a second timing run (or a still-
	// running one) can't make us export the engine's wrong/half-built subtitle.
	job, ok := h.engineTimingJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	job.Mu.Lock()
	status := job.Status
	scriptPath := job.ScriptPath
	job.Mu.Unlock()
	if status != "done" {
		writeError(w, http.StatusConflict, "打轴尚未完成，无法导出")
		return
	}
	// Optional JSON body: output directory + post-process options. A missing/empty
	// body keeps the legacy behavior (raw engine output, default subtitles dir).
	var body struct {
		OutputDir            string `json:"outputDir"`
		Clean                bool   `json:"clean"`                // 内建 tools.lua：改样式/删 Character+Screen 行（\N 保留）
		SyncTags             bool   `json:"syncTags"`             // Effect 埋 st:N 标识（Aegisub 同步的键）
		StyleTemplate        string `json:"styleTemplate"`        // 团队样式模板 .ass 路径（自定义覆盖）
		StyleTemplateContent string `json:"styleTemplateContent"` // 模板整段文本（插件内置模板，开箱即用）
		AegisubDir           string `json:"aegisubDir"`           // 用户指定的 Aegisub automation/autoload 目录（便携版）
		Staff                *service.StaffInfo `json:"staff"`    // staff 制作人员行（可选，见 asspost.StaffInfo）
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body is fine

	content, err := h.engine.Export(job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "导出字幕失败: "+err.Error())
		return
	}

	opts := service.AssPostOptions{
		Clean:                body.Clean,
		SyncTags:             body.SyncTags,
		StyleTemplate:        strings.TrimSpace(body.StyleTemplate),
		StyleTemplateContent: body.StyleTemplateContent,
		Staff:                body.Staff,
	}
	var warnings []string
	if opts.Clean || opts.SyncTags || opts.Staff != nil {
		post, perr := service.PostProcessAss(content, opts)
		if perr != nil {
			// 后处理拒绝损坏字幕时宁可导出原始内容，也不让用户拿不到 ass。
			warnings = append(warnings, "后处理失败，已导出未处理内容: "+perr.Error())
			opts = service.AssPostOptions{}
		} else {
			content = post.Content
			warnings = post.Warnings
		}
	}

	outDir := strings.TrimSpace(body.OutputDir)
	if outDir == "" {
		outDir = filepath.Join(h.cfg.DataDir, "subtitles")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "创建输出目录失败: "+err.Error())
		return
	}
	// Name the .ass after the scenario script (event_206_05.json -> event_206_05.ass)
	// rather than an opaque timing-<id>.ass; fall back to a timestamp if unknown.
	assPath := filepath.Join(outDir, assFileNameFor(scriptPath))
	if err := os.WriteFile(assPath, []byte(content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "写入字幕失败: "+err.Error())
		return
	}

	// 记录导出/同步基线：mtime+size 用来检测 Aegisub 侧改动，DirtyLines 从零计。
	var mtime time.Time
	var size int64
	if fi, serr := os.Stat(assPath); serr == nil {
		mtime, size = fi.ModTime(), fi.Size()
	}
	job.Mu.Lock()
	job.ExportAssPath = assPath
	job.ExportOpts = opts
	job.ExportMTime = mtime
	job.ExportSize = size
	job.DirtyLines = map[int]bool{}
	job.Mu.Unlock()

	// 同步启用时顺手把 Aegisub 宏写到同目录，并尽力直接装进本机 Aegisub 的
	// automation/autoload（探测到才装，开箱即用；没装 Aegisub 就只留目录副本）。
	syncScript := ""
	aegisubMacro := ""
	if opts.SyncTags {
		if p, serr := service.WriteAegisubSyncScript(outDir); serr == nil {
			syncScript = p
		} else {
			warnings = append(warnings, "写入 Aegisub 同步宏失败: "+serr.Error())
		}
		if p, serr := service.InstallAegisubSyncMacro(body.AegisubDir); serr != nil {
			warnings = append(warnings, "安装 Aegisub 同步宏失败: "+serr.Error())
		} else {
			aegisubMacro = p
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"assPath":      assPath,
		"chars":        len(content),
		"warnings":     warnings,
		"syncScript":   syncScript,
		"aegisubMacro": aegisubMacro,
	})
}

// assFileNameFor derives the export filename from the scenario script path: its
// base name with the extension swapped to .ass. Path separators and other unsafe
// characters are stripped so a crafted script path can't escape outDir.
func assFileNameFor(scriptPath string) string {
	base := filepath.Base(scriptPath)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = sanitizeBaseName(base)
	if base == "" {
		return "timing-" + newTaskID() + ".ass"
	}
	return base + ".ass"
}

// sanitizeBaseName drops path separators, control chars, and characters that are
// illegal in filenames on common platforms.
func sanitizeBaseName(s string) string {
	s = strings.TrimSpace(s)
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return -1
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
}

// --- Suppress ---

func (h *Handler) EngineSuppressStart(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "压制内核未安装")
		return
	}
	var req struct {
		service.SuppressParams
		Parallel bool `json:"parallel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SourceVideo == "" || req.OutputPath == "" {
		writeError(w, http.StatusBadRequest, "sourceVideo 和 outputPath 必填")
		return
	}
	if _, err := os.Stat(req.SourceVideo); err != nil {
		writeError(w, http.StatusBadRequest, "源视频不存在: "+req.SourceVideo)
		return
	}

	job, err := h.engine.StartSuppress(newTaskID(), req.SuppressParams, req.Parallel)
	if err != nil {
		if errors.Is(err, service.ErrSuppressBusy) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "启动压制失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"taskId": job.TaskID})
}

func (h *Handler) EngineSuppressProgress(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	job, ok := h.engineSuppressJob(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	job.Mu.Lock()
	percent := 0.0
	if job.Total > 0 {
		percent = float64(job.Frame) / float64(job.Total) * 100
		// ffmpeg's frame count routinely overshoots the probed Total on VFR/estimated
		// sources, so clamp like the timing path (engine.go routeNotification) to keep
		// a progress bar from rendering past full.
		if percent > 100 {
			percent = 100
		}
	}
	snap := map[string]interface{}{
		"taskId":     job.TaskID,
		"status":     job.Status,
		"frame":      job.Frame,
		"total":      job.Total,
		"fps":        job.Fps,
		"percent":    percent,
		"outputPath": job.OutputPath,
		"lastLog":    job.LastLog,
		"error":      job.Error,
	}
	job.Mu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

// EngineCancel stops a run in a domain ("timing" | "suppress")。带 task 参数时
// 精确取消该任务（并行模式必带）；不带时取消该域当前 running 任务（老插件兼容）。
func (h *Handler) EngineCancel(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "内核未安装")
		return
	}
	domain := r.URL.Query().Get("domain")
	if domain != "timing" && domain != "suppress" {
		writeError(w, http.StatusBadRequest, "未知取消域（应为 timing 或 suppress）: "+domain)
		return
	}
	if err := h.engine.Cancel(domain, r.URL.Query().Get("task")); err != nil {
		writeError(w, http.StatusInternalServerError, "取消失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "canceled"})
}

// EngineTasks 快照全部已注册任务，插件页面重挂载后据此找回任务列表。
func (h *Handler) EngineTasks(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"timing": []interface{}{}, "suppress": []interface{}{}})
		return
	}
	timing, suppress := h.engine.Tasks()
	writeJSON(w, http.StatusOK, map[string]interface{}{"timing": timing, "suppress": suppress})
}

// EngineTimingClose 关闭并移除一个打轴任务，释放其独占的引擎进程。
func (h *Handler) EngineTimingClose(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "内核未安装")
		return
	}
	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task 必填")
		return
	}
	if err := h.engine.CloseTiming(taskID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// EngineAegisubInstall 手动把同步宏装进指定（或自动探测的）Aegisub autoload 目录。
// 便携版 Aegisub 的配置跟着 exe 走、自动探测不到，用户浏览选一次目录即可。
func (h *Handler) EngineAegisubInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	p, err := service.InstallAegisubSyncMacro(body.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "安装失败: "+err.Error())
		return
	}
	if p == "" {
		writeError(w, http.StatusNotFound, "未检测到本机 Aegisub 配置目录；请手动指定 automation/autoload 目录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"installed": p})
}

// helpers — jobs are registered per taskId (one engine process per job).
func (h *Handler) engineTimingJob(taskID string) (*service.EngineTimingJob, bool) {
	if h.engine == nil || taskID == "" {
		return nil, false
	}
	return h.engine.TimingJob(taskID)
}

func (h *Handler) engineSuppressJob(taskID string) (*service.EngineSuppressJob, bool) {
	if h.engine == nil || taskID == "" {
		return nil, false
	}
	return h.engine.SuppressJob(taskID)
}
