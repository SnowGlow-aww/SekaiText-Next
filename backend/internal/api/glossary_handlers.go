package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

// --- Glossary: search / browse ---

func (h *Handler) GlossarySearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	category := r.URL.Query().Get("category")
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	results := h.glossary.Search(q, category, limit)
	writeJSON(w, http.StatusOK, results)
}

func (h *Handler) GlossaryCategories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.glossary.Categories())
}

func (h *Handler) GlossaryEntries(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	items, total := h.glossary.Entries(category, offset, limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
}

// --- Glossary: CRUD ---

func (h *Handler) GlossaryAddEntry(w http.ResponseWriter, r *http.Request) {
	var e model.GlossaryEntry
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(e.Source) == "" {
		writeError(w, http.StatusBadRequest, "missing source")
		return
	}
	saved, err := h.glossary.AddEntry(e)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) GlossaryUpdateEntry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var e model.GlossaryEntry
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	saved, ok, err := h.glossary.UpdateEntry(id, e)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "entry not found")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) GlossaryDeleteEntry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.glossary.DeleteEntry(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "entry not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Glossary: import / reload / sync ---

func (h *Handler) GlossaryImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SrcPath string `json:"srcPath"`
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
	entries, appellations, grammar, report, err := service.ParseWorkbook(src)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.glossary.MergeImport(entries, appellations, grammar, model.OriginImport); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[glossary] imported %d entries, %d appellations, %d grammar from %s", report.TotalEntries, report.TotalAppell, report.TotalGrammar, src)
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) GlossaryReload(w http.ResponseWriter, r *http.Request) {
	if err := h.glossary.Reload(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

// GlossarySync pulls a JSON GlossaryData payload from a remote URL and merges it
// (Origin=remote). This is the seam for future server-side central distribution.
func (h *Handler) GlossarySync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RemoteURL string `json:"remoteUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	url := strings.TrimSpace(req.RemoteURL)
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing remoteUrl")
		return
	}
	// Only allow http(s) so the fetch can't be redirected to file://, gopher://
	// and similar SSRF vectors. (Private/LAN hosts are intentionally allowed:
	// the user may self-host the team glossary server on a plain-http LAN box.)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		writeError(w, http.StatusBadRequest, "remoteUrl must be an http(s) URL")
		return
	}
	resp, err := http.Get(url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "remote returned "+resp.Status)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, "read failed: "+err.Error())
		return
	}
	var gd model.GlossaryData
	if err := json.Unmarshal(body, &gd); err != nil {
		writeError(w, http.StatusBadRequest, "invalid remote payload: "+err.Error())
		return
	}
	if err := h.glossary.MergeImport(gd.Entries, gd.Appellations, gd.Grammar, model.OriginRemote); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "synced", "entries": len(gd.Entries), "appellations": len(gd.Appellations), "grammar": len(gd.Grammar),
	})
}

// --- Glossary: appellation lookup (人称表) ---

func (h *Handler) GlossaryAppellationSpeakers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.glossary.AppellationSpeakers())
}

func (h *Handler) GlossaryAppellationTargets(w http.ResponseWriter, r *http.Request) {
	speaker := r.URL.Query().Get("speaker")
	writeJSON(w, http.StatusOK, h.glossary.AppellationTargets(speaker))
}

func (h *Handler) GlossaryAppellationLookup(w http.ResponseWriter, r *http.Request) {
	speaker := r.URL.Query().Get("speaker")
	target := r.URL.Query().Get("target")
	a, ok := h.glossary.AppellationLookup(speaker, target)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"found": true, "speaker": a.Speaker, "target": a.Target, "jp": a.JP, "cn": a.CN,
	})
}

func (h *Handler) GlossaryAppellationUpsert(w http.ResponseWriter, r *http.Request) {
	var a model.Appellation
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(a.Speaker) == "" || strings.TrimSpace(a.Target) == "" {
		writeError(w, http.StatusBadRequest, "missing speaker or target")
		return
	}
	if err := h.glossary.UpsertAppellation(a); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// --- Glossary: grammar (语法用例) + export ---

func (h *Handler) GlossaryGrammar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := atoiDefault(r.URL.Query().Get("limit"), 0)
	writeJSON(w, http.StatusOK, h.glossary.SearchGrammar(q, limit))
}

// GlossaryExport returns the full payload as a downloadable JSON attachment.
func (h *Handler) GlossaryExport(w http.ResponseWriter, r *http.Request) {
	gd := h.glossary.Export()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="glossary.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(gd)
}

// atoiDefault parses s as an int, returning def on failure/empty.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
