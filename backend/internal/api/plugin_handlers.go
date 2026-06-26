package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"sekaitext/backend/internal/service"
)

// --- Plugins ---
// Plugins live under {dataDir}/plugins/<id>/ (writable) and are served by this
// sidecar so the frontend can install/enable/disable/uninstall at runtime —
// the bundled frontend dist is read-only in the packaged app. (There is no
// first-party seeding or un-uninstallable flag — any installed id can be
// enabled/disabled/uninstalled.)

// PluginsList returns all installed plugins with manifest metadata + state.
func (h *Handler) PluginsList(w http.ResponseWriter, r *http.Request) {
	list, err := h.plugins.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list plugins failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// PluginSetEnabled persists a plugin's enabled flag. Body: {"enabled": bool}.
func (h *Handler) PluginSetEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing plugin id")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.plugins.SetEnabled(id, req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "set enabled failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// PluginUninstall removes a plugin's directory.
func (h *Handler) PluginUninstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing plugin id")
		return
	}
	if err := h.plugins.Uninstall(id); err != nil {
		writeError(w, http.StatusInternalServerError, "uninstall failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// PluginFile serves a static file from a plugin's directory (entry.js, assets).
func (h *Handler) PluginFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rest := chi.URLParam(r, "*")
	if id == "" || rest == "" {
		writeError(w, http.StatusBadRequest, "missing plugin id or path")
		return
	}
	base := h.plugins.PluginDir(id)
	if base == "" { // invalid plugin id (rejected by the store)
		writeError(w, http.StatusBadRequest, "invalid plugin id")
		return
	}
	target := filepath.Join(base, filepath.Clean("/"+rest))
	if !strings.HasPrefix(target, filepath.Clean(base)+string(filepath.Separator)) {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	// JS entry must be served with a JS mime so dynamic import() accepts it.
	if strings.HasSuffix(target, ".js") || strings.HasSuffix(target, ".mjs") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, target)
}

// PluginInstall unpacks a .sekplugin archive from a local path into the plugins
// dir. Body: {"srcPath": string, "hostVersion": string}. Used both by the
// "install from file" action (Tauri file dialog → path) and by the marketplace
// (download to temp → install by path). Returns the installed manifest.
func (h *Handler) PluginInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SrcPath     string `json:"srcPath"`
		HostVersion string `json:"hostVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	src := strings.TrimSpace(req.SrcPath)
	if src == "" {
		writeError(w, http.StatusBadRequest, "missing srcPath")
		return
	}
	if abs, err := filepath.Abs(src); err == nil {
		src = abs
	}
	if info, err := os.Stat(src); err != nil || info.IsDir() {
		writeError(w, http.StatusBadRequest, "package file not found")
		return
	}
	m, err := h.plugins.Install(src, req.HostVersion, "")
	if err != nil {
		switch {
		case errors.Is(err, service.ErrIncompatible):
			writeError(w, http.StatusConflict, "插件需要更新版本的主程序（minHostVersion="+m.MinHostVersion+"）")
		case errors.Is(err, service.ErrBadPackage):
			writeError(w, http.StatusBadRequest, "无效的插件包")
		default:
			writeError(w, http.StatusBadRequest, "安装失败: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// --- Plugin marketplace ---

// marketURL resolves the configured market index URL, falling back to default.
func (h *Handler) marketURL() string {
	s, err := h.loadSettings()
	if err != nil {
		return service.DefaultMarketURL
	}
	return s.PluginMarketURL // empty → service falls back to default
}

// MarketIndex fetches the remote plugin index, annotated with installed/update
// state for each entry.
func (h *Handler) MarketIndex(w http.ResponseWriter, r *http.Request) {
	listings, err := h.market.Listings(h.marketURL())
	if err != nil {
		writeError(w, http.StatusBadGateway, "插件市场获取失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listings)
}

// MarketInstall downloads + installs a plugin by id from the market. Body:
// {"id": string, "hostVersion": string}.
func (h *Handler) MarketInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		HostVersion string `json:"hostVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "missing plugin id")
		return
	}
	m, err := h.market.Install(h.marketURL(), req.ID, req.HostVersion)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrIncompatible):
			writeError(w, http.StatusConflict, "插件需要更新版本的主程序（minHostVersion="+m.MinHostVersion+"）")
		case errors.Is(err, service.ErrIDMismatch):
			writeError(w, http.StatusBadGateway, "插件包与市场条目不一致，已拒绝安装")
		case errors.Is(err, service.ErrBadPackage):
			writeError(w, http.StatusBadRequest, "无效的插件包")
		default:
			writeError(w, http.StatusBadGateway, "安装失败: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, m)
}
