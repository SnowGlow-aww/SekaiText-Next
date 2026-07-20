package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"sekaitext/backend/internal/fsutil"
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

// Serializes destination validation and publication across concurrent timing
// exports. The lock is deliberately outside EngineManager lifecycle handling.
var timingOutputMu sync.Mutex

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
	writeJSON(w, http.StatusOK, map[string]interface{}{"taskId": job.TaskID, "documentId": job.TaskID, "revision": uint64(0)})
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
		"taskId":        job.TaskID,
		"documentId":    job.TaskID,
		"revision":      job.SyncRevision,
		"status":        job.Status,
		"percent":       job.Percent,
		"fps":           job.Fps,
		"eta":           job.Eta,
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
	job.DocumentMu.Lock()
	defer job.DocumentMu.Unlock()
	job.Mu.Lock()
	status := job.Status
	scriptPath := job.ScriptPath
	currentAssPath := job.ExportAssPath
	currentDocumentID := job.ExportOpts.DocumentID
	nextRevision := job.SyncRevision + 1
	job.Mu.Unlock()
	if status != "done" {
		writeError(w, http.StatusConflict, "打轴尚未完成，无法导出")
		return
	}
	// Optional JSON body: output directory + post-process options. A missing/empty
	// body keeps the legacy behavior (raw engine output, default subtitles dir).
	var body struct {
		OutputDir            string             `json:"outputDir"`
		Clean                bool               `json:"clean"`                // 内建 tools.lua：改样式/删 Character+Screen 行（\N 保留）
		SyncTags             bool               `json:"syncTags"`             // Effect 埋 st:N 标识（Aegisub 同步的键）
		StyleTemplate        string             `json:"styleTemplate"`        // 团队样式模板 .ass 路径（自定义覆盖）
		StyleTemplateContent string             `json:"styleTemplateContent"` // 模板整段文本（插件内置模板，开箱即用）
		AegisubDir           string             `json:"aegisubDir"`           // 用户指定的 Aegisub automation/autoload 目录（便携版）
		Staff                *service.StaffInfo `json:"staff"`                // staff 制作人员行（可选，见 asspost.StaffInfo）
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
	if opts.SyncTags {
		opts.DocumentID = job.TaskID
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
	syncHash := ""
	if opts.SyncTags {
		syncHash = contentSHA256([]byte(content))
		content, err = service.EmbedAegisubSyncMetadata(content, service.AegisubSyncMetadata{
			DocumentID:  job.TaskID,
			Revision:    nextRevision,
			ContentHash: syncHash,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "写入同步元数据失败: "+err.Error())
			return
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
	preferredAssPath := filepath.Join(outDir, assFileNameFor(scriptPath))
	timingOutputMu.Lock()
	// Sidecars are one-shot CAS requests. A re-export establishes a new baseline,
	// so stale requests in both the old and new directories must be invalidated.
	if err := invalidateAegisubSidecars(
		service.AegisubSyncPath(currentAssPath, currentDocumentID),
		service.AegisubSyncPath(preferredAssPath, opts.DocumentID),
	); err != nil {
		timingOutputMu.Unlock()
		writeError(w, http.StatusInternalServerError, "无法使旧同步文件失效: "+err.Error())
		return
	}
	contentBytes := []byte(content)
	forceVersion := timingOutputConflict(h.engine, job.TaskID, preferredAssPath) != ""
	assPath, err := publishTimingASS(preferredAssPath, job.TaskID, nextRevision, contentBytes, forceVersion)
	if err != nil {
		timingOutputMu.Unlock()
		writeError(w, http.StatusInternalServerError, "写入字幕失败: "+err.Error())
		return
	}

	// SHA-256 is the authoritative baseline. mtime/size remain populated for jobs
	// exported by older in-process code which have no hash yet.
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
	job.ExportHash = contentSHA256(contentBytes)
	job.SyncRevision = nextRevision
	job.ExportRevision = nextRevision
	job.ExportSyncHash = syncHash
	revision := nextRevision
	job.DirtyLines = map[int]bool{}
	job.LineRevisions = map[int]uint64{}
	job.Mu.Unlock()
	timingOutputMu.Unlock()

	// 同步启用时顺手把 Aegisub 宏写到同目录，并尽力直接装进本机 Aegisub 的
	// automation/autoload（探测到才装，开箱即用；没装 Aegisub 就只留目录副本）。
	syncScript := ""
	aegisubMacro := ""
	if opts.SyncTags {
		if p, serr := service.WriteAegisubSyncScript(filepath.Dir(assPath)); serr == nil {
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
		"documentId":   job.TaskID,
		"revision":     revision,
		"contentHash":  contentSHA256(contentBytes),
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

func contentSHA256(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func canonicalOutputPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	return filepath.Clean(abs)
}

func sameOutputPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if aInfo, aErr := os.Stat(a); aErr == nil {
		if bInfo, bErr := os.Stat(b); bErr == nil && os.SameFile(aInfo, bInfo) {
			return true
		}
	}
	a, b = canonicalOutputPath(a), canonicalOutputPath(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func timingOutputConflict(engine *service.EngineManager, taskID, outputPath string) string {
	if engine == nil {
		return ""
	}
	timing, _ := engine.Tasks()
	return timingOutputConflictIn(timing, taskID, outputPath)
}

func timingOutputConflictIn(timing []service.EngineTaskSnapshot, taskID, outputPath string) string {
	for _, other := range timing {
		if other.TaskID != taskID && sameOutputPath(other.ExportAssPath, outputPath) {
			return fmt.Sprintf("输出路径已被打轴任务 %s 使用: %s", other.TaskID, outputPath)
		}
	}
	return ""
}

// An existing SekaiText-tagged file is a document, not an anonymous output
// blob. A matching immutable ID proves ownership even if this job exported to
// another directory in between; legacy tags need the exact current binding.
func validateExportDestination(path, currentPath, currentHash, documentID string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查输出路径失败: %w", err)
	}
	// Only an exact bound path with an unchanged content baseline may be
	// overwritten. This protects Aegisub edits that have not been pulled yet and
	// arbitrary pre-existing untagged subtitle files.
	if !sameOutputPath(path, currentPath) || currentHash == "" {
		return fmt.Errorf("输出路径已存在，且不是当前任务可安全覆盖的字幕: %s", path)
	}
	if contentSHA256(data) != currentHash {
		return fmt.Errorf("字幕已在外部修改，请先从 Aegisub 回读或另存: %s", path)
	}
	groups, _, parseErr := service.ExtractSyncGroups(string(data))
	if parseErr != nil || len(groups) == 0 {
		// Non-sync exports are still safe because their exact content hash matches
		// the baseline captured by this task.
		return nil
	}
	if err := service.ValidateSyncGroups(groups, documentID, false); err == nil {
		return nil
	} else if legacySyncFileIsUnique(path) {
		if legacyErr := service.ValidateSyncGroups(groups, documentID, true); legacyErr == nil {
			return nil
		}
		return fmt.Errorf("输出路径文档身份冲突: %w", err)
	} else {
		return fmt.Errorf("输出路径文档身份冲突: %w", err)
	}
}

func invalidateAegisubSidecars(paths ...string) error {
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = canonicalOutputPath(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// publishTimingASS never replaces an existing path. If the preferred scenario
// name is occupied (including a normal re-export), it publishes a versioned
// sibling instead. This avoids the check-then-rename overwrite race for files
// Aegisub can modify independently.
func publishTimingASS(preferredPath, documentID string, revision uint64, data []byte, forceVersion bool) (string, error) {
	versionAttempt := 0
	for attempt := 0; attempt < 1000; attempt++ {
		path := preferredPath
		if forceVersion {
			path = versionedASSPath(preferredPath, documentID, revision, versionAttempt)
			versionAttempt++
		}
		err := writeFileNoReplaceAtomic(path, data, 0644)
		if errors.Is(err, os.ErrExist) {
			forceVersion = true
			continue
		}
		if err != nil {
			return "", err
		}
		// Establish no baseline unless the just-published path still contains the
		// bytes we wrote. A later external change is detected by normal sync status.
		readBack, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("verify published ASS: %w", err)
		}
		if contentSHA256(readBack) != contentSHA256(data) {
			return "", fmt.Errorf("ASS changed during publication: %s", path)
		}
		return path, nil
	}
	return "", fmt.Errorf("无法找到未占用的版本化 ASS 输出路径")
}

func versionedASSPath(path, documentID string, revision uint64, attempt int) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	suffix := fmt.Sprintf(".r%d-%s", revision, sanitizeBaseName(documentID))
	if attempt > 0 {
		suffix += "-" + strconv.Itoa(attempt+1)
	}
	return base + suffix + ext
}

// writeFileNoReplaceAtomic uses a sibling temp plus hard-link publication as an
// atomic create-if-absent operation. Filesystems without hard links fall back to
// O_EXCL direct creation: that fallback can expose a partial new version while
// writing, but it still never overwrites existing user data.
func writeFileNoReplaceAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileNoReplaceAtomicWithSync(path, data, perm, fsutil.SyncDir)
}

func writeFileNoReplaceAtomicWithSync(path string, data []byte, perm os.FileMode, syncDir func(string) error) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, tmp.Close())
		}
		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if n, err := tmp.Write(data); err != nil {
		return err
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true

	if err := os.Link(tmpPath, path); err == nil {
		if err := syncDir(dir); err != nil {
			return fmt.Errorf("sync ASS directory: %w", err)
		}
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return err
	}

	// Hard links are unavailable on some network/FAT-style filesystems.
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	removeOnError := true
	outClosed := false
	defer func() {
		if !outClosed {
			if closeErr := out.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
		if removeOnError {
			_ = os.Remove(path)
		}
	}()
	if n, writeErr := out.Write(data); writeErr != nil {
		err = writeErr
		return err
	} else if n != len(data) {
		err = io.ErrShortWrite
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	err = out.Close()
	outClosed = true
	if err != nil {
		return err
	}
	removeOnError = false
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync ASS directory: %w", err)
	}
	return nil
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
		if errors.Is(err, service.ErrSuppressBusy) || errors.Is(err, service.ErrSuppressOutputConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "启动压制失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"taskId": job.TaskID})
}

// EngineSuppressProbe returns the suppress runtime info plus hardware-verified
// encoders and the recommended default (engine ≥2.1.0; older engines omit the
// encoder fields and the plugin falls back to a platform-safe static list).
func (h *Handler) EngineSuppressProbe(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "压制内核未安装")
		return
	}
	res, err := h.engine.SuppressProbe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "探测压制环境失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res)
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
		"logPath":    job.LogPath, // 报错时后端已自动导出的日志文件（空 = 未导出）
	}
	job.Mu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

// EngineSuppressLog 返回任务的滚动日志（内存缓冲，进度行已折叠），给插件的
// 日志面板轮询用；path 为已导出的日志文件路径（报错自动导出后非空）。
func (h *Handler) EngineSuppressLog(w http.ResponseWriter, r *http.Request) {
	job, ok := h.engineSuppressJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	job.Mu.Lock()
	lines := append([]string(nil), job.LogLines...)
	path := job.LogPath
	job.Mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"taskId": job.TaskID, "lines": lines, "path": path})
}

// EngineSuppressLogExport 手动把任务日志落盘（报错时后端已自动导出；此端点给
// 用户主动留档/补导出用），返回文件路径。
func (h *Handler) EngineSuppressLogExport(w http.ResponseWriter, r *http.Request) {
	job, ok := h.engineSuppressJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	path, err := h.engine.ExportSuppressLog(job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "导出日志失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
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

// EngineSuppressClose 关闭并移除一个压制任务（含已取消/完成/失败的终态卡片），释放其内核进程。
func (h *Handler) EngineSuppressClose(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "内核未安装")
		return
	}
	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task 必填")
		return
	}
	if err := h.engine.CloseSuppress(taskID); err != nil {
		if errors.Is(err, service.ErrSuppressCloseRunning) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
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
