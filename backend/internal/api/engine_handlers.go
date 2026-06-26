package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"sekaitext/backend/internal/service"
)

// Engine endpoints drive the bundled SekaiToolsEngine sidecar: auto-timing (打轴)
// and video-suppress (压制). They follow the same trigger+poll job pattern as the
// JSON download endpoints (taskId -> a /progress poll -> terminal status).

func newTaskID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// EngineStatus reports whether the engine is bundled and, if so, its version.
func (h *Handler) EngineStatus(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeJSON(w, http.StatusOK, map[string]interface{}{"available": false})
		return
	}
	info, err := h.engine.Ping()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"available": true, "ready": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"available": true, "ready": true, "engine": info})
}

// --- Auto-timing ---

func (h *Handler) EngineTimingStart(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴引擎未安装")
		return
	}
	var req service.TimingParams
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

	job, err := h.engine.StartTiming(newTaskID(), req)
	if err != nil {
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
		"dialogTotal": job.DialogTotal,
		"bannerTotal": job.BannerTotal,
		"markerTotal": job.MarkerTotal,
		"matched":     job.Matched,
		"error":       job.Error,
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
		writeError(w, http.StatusServiceUnavailable, "打轴引擎未安装")
		return
	}
	content, err := h.engine.Export()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "导出字幕失败: "+err.Error())
		return
	}
	outDir := filepath.Join(h.cfg.DataDir, "subtitles")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "创建输出目录失败: "+err.Error())
		return
	}
	assPath := filepath.Join(outDir, fmt.Sprintf("timing-%s.ass", newTaskID()))
	if err := os.WriteFile(assPath, []byte(content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "写入字幕失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"assPath": assPath, "chars": len(content)})
}

// --- Suppress ---

func (h *Handler) EngineSuppressStart(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "压制引擎未安装")
		return
	}
	var req service.SuppressParams
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

	job, err := h.engine.StartSuppress(newTaskID(), req)
	if err != nil {
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

// EngineCancel stops the active run in a domain ("timing" | "suppress").
func (h *Handler) EngineCancel(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "引擎未安装")
		return
	}
	domain := r.URL.Query().Get("domain")
	if err := h.engine.Cancel(domain); err != nil {
		writeError(w, http.StatusInternalServerError, "取消失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "canceled"})
}

// helpers — single active job per domain (the engine has no correlation ids).
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
