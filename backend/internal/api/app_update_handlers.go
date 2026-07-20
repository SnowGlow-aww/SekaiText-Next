package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sekaitext/backend/internal/fsutil"
	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
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

// downloadsDir is private app storage, not the user-writable Downloads folder.
func (h *Handler) downloadsDir() string {
	return filepath.Join(h.cfg.DataDir, "updates")
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
	h.appUpdateMu.Lock()
	defer h.appUpdateMu.Unlock()
	var activeTaskID string
	h.downloadTasks.Range(func(_, value interface{}) bool {
		task, ok := value.(*model.DownloadTaskProgress)
		if !ok {
			return true
		}
		task.Mu.Lock()
		if task.Purpose == "app-update" && task.Status == "downloading" {
			activeTaskID = task.TaskID
		}
		task.Mu.Unlock()
		return activeTaskID == ""
	})
	if activeTaskID != "" {
		writeJSON(w, http.StatusOK, map[string]string{"taskId": activeTaskID})
		return
	}
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
	task := &model.DownloadTaskProgress{
		TaskID:       taskID,
		Status:       "downloading",
		Total:        info.Size,
		Purpose:      "app-update",
		Digest:       info.Digest,
		ExpectedSize: info.Size,
	}
	h.downloadTasks.Store(taskID, task)

	dest := h.downloadsDir()
	go func(url, digest string, size int64) {
		path, err := h.appUpdate.DownloadUpdate(url, digest, size, dest, func(read, total int64) {
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
			task.IntegrityVerified = true
		}
		task.FinishedAt = time.Now().UnixNano()
		task.Mu.Unlock()
	}(info.DownloadURL, info.Digest, info.Size)

	writeJSON(w, http.StatusOK, map[string]string{"taskId": taskID})
}

// AppUpdateOpen opens a downloaded installer (mounts the .dmg / launches the
// installer). Body: {"path": string}. The path must exactly match a completed,
// integrity-verified app-update task and is reverified immediately before launch.
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
	var artifactTask *model.DownloadTaskProgress
	h.downloadTasks.Range(func(_, value interface{}) bool {
		task, ok := value.(*model.DownloadTaskProgress)
		if !ok {
			return true
		}
		task.Mu.Lock()
		matches := task.Purpose == "app-update" && task.Status == "done" &&
			task.IntegrityVerified && sameUpdatePath(task.FilePath, p)
		task.Mu.Unlock()
		if matches {
			artifactTask = task
			return false
		}
		return true
	})
	if artifactTask == nil {
		writeError(w, http.StatusForbidden, "只允许打开当前已校验完成的更新安装包")
		return
	}
	artifactTask.Mu.Lock()
	digest := artifactTask.Digest
	size := artifactTask.ExpectedSize
	artifactTask.Mu.Unlock()
	// Reject unresolved links instead of falling back to the client-controlled
	// spelling when path resolution fails.
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		writeError(w, http.StatusConflict, "无法解析安装包真实路径，已拒绝打开")
		return
	}
	p = real
	// Only ever launch an installer file type, never an arbitrary executable that
	// merely happens to sit under Downloads.
	switch strings.ToLower(filepath.Ext(p)) {
	case ".dmg", ".pkg", ".exe", ".msi":
	default:
		writeError(w, http.StatusForbidden, "只允许打开安装包文件（.dmg/.pkg/.exe/.msi）")
		return
	}
	if err := service.VerifyUpdateFile(p, digest, size); err != nil {
		artifactTask.Mu.Lock()
		artifactTask.IntegrityVerified = false
		artifactTask.Status = "error"
		artifactTask.Error = "安装包完整性复检失败: " + err.Error()
		artifactTask.Mu.Unlock()
		writeError(w, http.StatusConflict, "安装包完整性复检失败，已拒绝打开")
		return
	}
	launchPath, err := h.stageUpdateForLaunch(r.Context(), p, digest, size)
	if err != nil {
		writeError(w, http.StatusConflict, "安装包安全暂存失败，已拒绝打开: "+err.Error())
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", launchPath)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", launchPath)
	default:
		cmd = exec.Command("xdg-open", launchPath)
	}
	service.DetachInstallerProcess(cmd)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(filepath.Dir(launchPath))
		writeError(w, http.StatusInternalServerError, "打开失败: "+err.Error())
		return
	}
	go func() {
		_ = cmd.Wait()
		timer := time.NewTimer(24 * time.Hour)
		defer timer.Stop()
		<-timer.C
		_ = os.RemoveAll(filepath.Dir(launchPath))
	}()
	writeJSON(w, http.StatusOK, map[string]string{"opened": launchPath})
}

func (h *Handler) stageUpdateForLaunch(ctx context.Context, source, digest string, size int64) (string, error) {
	root := filepath.Join(h.cfg.DataDir, "update-launch")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "launch-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	dest := filepath.Join(dir, filepath.Base(source))
	if err := fsutil.CopyFileAtomic(ctx, source, dest, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if err := service.VerifyUpdateFile(dest, digest, size); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dest, nil
}

func (h *Handler) cleanupUpdateLaunchDirs() {
	root := filepath.Join(h.cfg.DataDir, "update-launch")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

func sameUpdatePath(a, b string) bool {
	a, errA := filepath.Abs(strings.TrimSpace(a))
	b, errB := filepath.Abs(strings.TrimSpace(b))
	if errA != nil || errB != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
