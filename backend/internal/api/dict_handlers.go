package api

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// 字典（只读日语词典）—— 挂在 /glossary/dicts/* 下，与主术语库物理隔离：这些端点
// 只碰 DictStore，绝不触及 GlossaryStore / Export / 团队同步。均为本地 app 后端端点，
// 无鉴权，与 /glossary/* 一致（导入/删除是 mutating，生产下走同一 capability token）。

// DictList 返回全部字典分类摘要。
func (h *Handler) DictList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.dict.List())
}

// DictImport 从 multipart 上传（字段名 "file"）导入一部字典。沿用 xlsx 导入的
// 64MiB 上限；源字典约 19MB。
func (h *Handler) DictImport(w http.ResponseWriter, r *http.Request) {
	// 内存阈值 64MiB：超出部分溢写临时文件，同时下面再对读入字节做一次硬上限兜底。
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "解析上传失败："+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少文件字段 file")
		return
	}
	defer file.Close()

	// LimitReader 读到上限 +1 字节，一旦越界即拒绝（ParseMultipartForm 的参数只是
	// 内存/落盘阈值，并非硬上限）。
	raw, err := io.ReadAll(io.LimitReader(file, 64<<20+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取文件失败："+err.Error())
		return
	}
	if len(raw) > 64<<20 {
		writeError(w, http.StatusBadRequest, "文件过大（>64MiB），已拒绝")
		return
	}

	info, err := h.dict.Import(header.Filename, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[dict] imported %q with %d entries", info.Name, info.Count)
	writeJSON(w, http.StatusOK, info)
}

// DictDelete 删除某字典。name 是 URL path value，中文经 encodeURIComponent 编码，
// 这里先 PathUnescape 还原（无 % 时原样返回，故解码/未解码两种情况都安全）。
func (h *Handler) DictDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing dict name")
		return
	}
	if err := h.dict.Delete(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// DictSurfaces 返回全部合格取词表面形 + 最长 rune 数（前端最长匹配扫描窗口上限）。
func (h *Handler) DictSurfaces(w http.ResponseWriter, r *http.Request) {
	surfaces, maxLen := h.dict.Surfaces()
	writeJSON(w, http.StatusOK, map[string]interface{}{"surfaces": surfaces, "maxLen": maxLen})
}

// DictLookup 取词：?surface=xxx → {"items":[LookupHit,...]}。空/未命中返回 items:[]。
func (h *Handler) DictLookup(w http.ResponseWriter, r *http.Request) {
	surface := r.URL.Query().Get("surface")
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": h.dict.Lookup(surface)})
}

// DictEntries 浏览/搜索某字典分类：?name=&q=&offset=&limit=。limit 默认 50、上限 200。
func (h *Handler) DictEntries(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	q := r.URL.Query().Get("q")
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	items, total := h.dict.EntriesPage(name, q, offset, limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
}
