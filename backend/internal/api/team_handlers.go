package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

// TeamStatus reports the current team-mode session.
func (h *Handler) TeamStatus(w http.ResponseWriter, r *http.Request) {
	url, user := h.team.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"loggedIn":  user != nil,
		"connected": url != "",
		"readonly":  url != "" && user == nil,
		"serverUrl": url,
		"user":      user,
	})
}

// TeamConnect sets the server URL for no-login readonly mode.
func (h *Handler) TeamConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerURL string `json:"serverUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.team.Connect(req.ServerURL); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, service.ErrTeamPersistence) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"connected": true, "readonly": true})
}

// TeamDisconnect fully clears the session (back to pure local).
func (h *Handler) TeamDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := h.team.Disconnect(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// TeamLogin authenticates against a remote glossary-server.
func (h *Handler) TeamLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerURL string `json:"serverUrl"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.team.Login(req.ServerURL, req.Username, req.Password)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, service.ErrTeamPersistence) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"loggedIn": true, "user": user})
}

// TeamLogout clears the session.
func (h *Handler) TeamLogout(w http.ResponseWriter, r *http.Request) {
	if err := h.team.Logout(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// TeamSync polls the remote version; if it changed (or force), pulls /export
// and merges as Origin=remote, reusing the existing glossary merge path.
func (h *Handler) TeamSync(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	var gd model.GlossaryData
	mergeStatus := 0
	result, err := h.team.Sync(force, func(raw []byte) (int, error) {
		if err := json.Unmarshal(raw, &gd); err != nil {
			mergeStatus = http.StatusBadGateway
			return 0, fmt.Errorf("invalid remote payload: %w", err)
		}
		removed, err := h.glossary.MergeImport(gd.Entries, gd.Appellations, gd.Grammar, model.OriginRemote)
		if err != nil {
			mergeStatus = http.StatusInternalServerError
		}
		return removed, err
	})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, service.ErrNotLoggedIn) {
			status = http.StatusUnauthorized
		} else if errors.Is(err, service.ErrStaleTeamSession) {
			status = http.StatusConflict
		} else if mergeStatus != 0 {
			status = mergeStatus
		}
		writeError(w, status, err.Error())
		return
	}
	if !result.Changed {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "up-to-date", "version": result.Version, "changed": false,
		})
		return
	}
	// 下行备份：把刚拉到的服务器全量 JSON 滚动存档（保留最近 10 份），误操作可回滚
	h.glossary.WriteSyncBackup(result.Raw)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "synced", "version": result.Version, "changed": true,
		"entries": len(gd.Entries), "appellations": len(gd.Appellations), "grammar": len(gd.Grammar),
		"removed": result.Removed,
	})
}

// teamProxy forwards a request to the remote server and relays body+status.
func (h *Handler) teamProxy(w http.ResponseWriter, method, path string, payload any) {
	body, status, err := h.team.Proxy(method, path, payload)
	if err != nil {
		if err == service.ErrNotLoggedIn {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func decodeBody(r *http.Request) map[string]any {
	var m map[string]any
	_ = json.NewDecoder(r.Body).Decode(&m)
	return m
}

func cleanID(s string) string { return strings.TrimSpace(s) }
