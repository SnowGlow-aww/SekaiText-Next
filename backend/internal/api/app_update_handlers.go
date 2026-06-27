package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sekaitext/backend/internal/model"
)

// --- Plugin auto-update ---

// MarketAutoUpdate reinstalls every installed plugin that has a newer version in
// the market, returning a per-plugin summary. Body: {"hostVersion": string}.
func (h *Handler) MarketAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HostVersion string `json:"hostVersion"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional
	sum, err := h.market.AutoUpdate(h.marketURL(), req.HostVersion)
	if err != nil {
		writeError(w, http.StatusBadGateway, "插件更新检查失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// --- App self-update ---

// appUpdateURL resolves the configured app-release manifest URL (empty → default).
func (h *Handler) appUpdateURL() string {
	s, err := h.loadSettings()
	if err != nil {
		return ""
	}
	return s.AppUpdateURL // empty → service falls back to default
}

// downloadsDir is where installers are saved — the user's Downloads folder when
// available, else the app data dir.
func (h *Handler) downloadsDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		d := filepath.Join(home, "Downloads")
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return d
		}
	}
	return h.cfg.DataDir
}

// AppUpdateCheck reports whether a newer app version is available for this
// platform. Query: ?current=<version>.
func (h *Handler) AppUpdateCheck(w http.ResponseWriter, r *http.Request) {
	current := r.URL.Query().Get("current")
	info, err := h.appUpdate.CheckUpdate(h.appUpdateURL(), current)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// AppUpdateDownload starts an async download of the available installer to the
// Downloads dir; poll progress via /app/update/download-progress (shared
// DownloadProgress). Body: {"current": string}. The download URL comes from a
// fresh server-side check, so the client can't point this at an arbitrary URL.
func (h *Handler) AppUpdateDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	info, err := h.appUpdate.CheckUpdate(h.appUpdateURL(), req.Current)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !info.UpdateAvailable || info.DownloadURL == "" {
		writeError(w, http.StatusConflict, "当前没有可用更新")
		return
	}

	taskID := strconv.FormatInt(time.Now().UnixNano(), 36)
	task := &model.DownloadTaskProgress{TaskID: taskID, Status: "downloading"}
	h.downloadTasks.Store(taskID, task)

	dest := h.downloadsDir()
	go func(url string) {
		path, err := h.appUpdate.DownloadUpdate(url, dest, func(read, total int64) {
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
			task.FilePath = path
		}
		task.Mu.Unlock()
	}(info.DownloadURL)

	writeJSON(w, http.StatusOK, map[string]string{"taskId": taskID})
}

// AppUpdateOpen opens a downloaded installer (mounts the .dmg / launches the
// installer). Body: {"path": string}. The path must be inside the Downloads/data
// dir we wrote to, so this can't be used to open arbitrary files.
func (h *Handler) AppUpdateOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil || req.Path == "" {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	allowed := false
	for _, base := range []string{h.downloadsDir(), h.cfg.DataDir} {
		if b, err := filepath.Abs(base); err == nil && strings.HasPrefix(p, b+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "path not allowed")
		return
	}
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", p)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", p)
	default:
		cmd = exec.Command("xdg-open", p)
	}
	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "打开失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"opened": p})
}
