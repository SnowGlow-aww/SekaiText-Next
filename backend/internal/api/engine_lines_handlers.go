package api

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sekaitext/backend/internal/service"
)

// 行列表 / 分句编辑 / Aegisub 同步。全部绑定到具体 taskId（同 export 的语义）：
// 新一轮打轴开始后旧任务的这些端点全部 404，杜绝编辑到引擎里换掉的数据。

// EngineTimingLines 代理引擎的 subtitle.lines（引擎是唯一权威，broker 不缓存）。
// 运行中也可调用：引擎只返回已定稿的行，插件用它渲染实时增长的行列表。
func (h *Handler) EngineTimingLines(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return
	}
	if _, ok := h.engineTimingJob(r.URL.Query().Get("task")); !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	raw, err := h.engine.TimingLines()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取行列表失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// requireDoneTimingJob 取绑定任务并要求已完成（编辑/导出/同步都要求引擎数据已定稿）。
func (h *Handler) requireDoneTimingJob(w http.ResponseWriter, r *http.Request) (*service.EngineTimingJob, bool) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return nil, false
	}
	job, ok := h.engineTimingJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return nil, false
	}
	job.Mu.Lock()
	status := job.Status
	job.Mu.Unlock()
	if status != "done" {
		writeError(w, http.StatusConflict, "打轴尚未完成")
		return nil, false
	}
	return job, true
}

// EngineTimingLineSeparator 设置某行的分句（换行帧/文本分割点/是否分句）。
func (h *Handler) EngineTimingLineSeparator(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
		return
	}
	var body struct {
		Index                 *int  `json:"index"`
		UseSeparator          *bool `json:"useSeparator"`
		SeparateFrame         *int  `json:"separateFrame"`
		SeparatorContentIndex *int  `json:"separatorContentIndex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Index == nil {
		writeError(w, http.StatusBadRequest, "index 必填")
		return
	}
	params := map[string]interface{}{"index": *body.Index}
	if body.UseSeparator != nil {
		params["useSeparator"] = *body.UseSeparator
	}
	if body.SeparateFrame != nil {
		params["separateFrame"] = *body.SeparateFrame
	}
	if body.SeparatorContentIndex != nil {
		params["separatorContentIndex"] = *body.SeparatorContentIndex
	}
	raw, err := h.engine.TimingLineCall("subtitle.setSeparator", params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "设置分句失败: "+err.Error())
		return
	}
	markDirtyLine(job, *body.Index)
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// EngineTimingLineTranslation 修改某行译文（对齐 GUI 的 QuickEdit 语义）。
func (h *Handler) EngineTimingLineTranslation(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
		return
	}
	var body struct {
		Index        *int    `json:"index"`
		Text         *string `json:"text"`
		UseSeparator *bool   `json:"useSeparator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Index == nil || body.Text == nil {
		writeError(w, http.StatusBadRequest, "index 和 text 必填")
		return
	}
	params := map[string]interface{}{"index": *body.Index, "text": *body.Text}
	if body.UseSeparator != nil {
		params["useSeparator"] = *body.UseSeparator
	}
	raw, err := h.engine.TimingLineCall("subtitle.setTranslation", params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "修改译文失败: "+err.Error())
		return
	}
	markDirtyLine(job, *body.Index)
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// EngineTimingAutosave 把当前引擎字幕（同导出口径后处理）写到 <outputDir>/autosave.ass。
// 与正式导出的区别：不更新导出/同步基线（ExportAssPath/MTime/DirtyLines 全不动），
// 纯粹是逐行微调后的落盘保险——崩溃/误退后打开 autosave.ass 即可拿回全部微调。
func (h *Handler) EngineTimingAutosave(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireDoneTimingJob(w, r); !ok {
		return
	}
	var body struct {
		OutputDir            string `json:"outputDir"`
		Clean                bool   `json:"clean"`
		SyncTags             bool   `json:"syncTags"`
		StyleTemplate        string `json:"styleTemplate"`
		StyleTemplateContent string `json:"styleTemplateContent"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	content, err := h.engine.Export()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "组装字幕失败: "+err.Error())
		return
	}
	opts := service.AssPostOptions{
		Clean:                body.Clean,
		SyncTags:             body.SyncTags,
		StyleTemplate:        strings.TrimSpace(body.StyleTemplate),
		StyleTemplateContent: body.StyleTemplateContent,
	}
	if opts.Clean || opts.SyncTags {
		// 后处理失败时保留原始内容——保险文件宁可裸也不能缺。
		if post, perr := service.PostProcessAss(content, opts); perr == nil {
			content = post.Content
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
	assPath := filepath.Join(outDir, "autosave.ass")
	if err := os.WriteFile(assPath, []byte(content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "写入失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"assPath": assPath})
}

// EngineTimingLineEstimate 按打字速度估算给定文本分割点对应的换行帧（只算不落地）。
func (h *Handler) EngineTimingLineEstimate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireDoneTimingJob(w, r); !ok {
		return
	}
	var body struct {
		Index                 *int `json:"index"`
		SeparatorContentIndex *int `json:"separatorContentIndex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Index == nil {
		writeError(w, http.StatusBadRequest, "index 必填")
		return
	}
	params := map[string]interface{}{"index": *body.Index}
	if body.SeparatorContentIndex != nil {
		params["separatorContentIndex"] = *body.SeparatorContentIndex
	}
	raw, err := h.engine.TimingLineCall("subtitle.estimateSeparator", params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "估算失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// EngineTimingFrame 取指定帧画面（base64 jpeg），分隔帧微调的所见即所得预览。
func (h *Handler) EngineTimingFrame(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return
	}
	if _, ok := h.engineTimingJob(r.URL.Query().Get("task")); !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	frame, err := strconv.Atoi(r.URL.Query().Get("frame"))
	if err != nil || frame < 0 {
		writeError(w, http.StatusBadRequest, "frame 必须是非负整数")
		return
	}
	maxWidth, _ := strconv.Atoi(r.URL.Query().Get("maxWidth"))
	raw, rerr := h.engine.TimingFrame(frame, maxWidth)
	if rerr != nil {
		writeError(w, http.StatusInternalServerError, "取帧失败: "+rerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// --- Aegisub 同步 ---

// EngineTimingSyncStatus 报告导出文件的同步态：Aegisub 侧是否改过、轴机侧有哪些待推送行。
func (h *Handler) EngineTimingSyncStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := h.engineTimingJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	job.Mu.Lock()
	assPath := job.ExportAssPath
	mt := job.ExportMTime
	size := job.ExportSize
	dirty := sortedDirty(job.DirtyLines)
	syncTags := job.ExportOpts.SyncTags
	job.Mu.Unlock()

	if assPath == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"exported": false})
		return
	}
	fi, err := os.Stat(assPath)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"exported":      true,
		"assPath":       assPath,
		"syncTags":      syncTags,
		"fileMissing":   err != nil,
		"changedOnDisk": err == nil && (!fi.ModTime().Equal(mt) || fi.Size() != size),
		"dirtyLines":    dirty,
		"syncFile":      assPath + ".sekaisync.txt",
	})
}

// EngineTimingSyncPush 把轴机侧改过的行写成同步文件，供 Aegisub 宏一键拉取。
func (h *Handler) EngineTimingSyncPush(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
		return
	}
	job.Mu.Lock()
	assPath := job.ExportAssPath
	opts := job.ExportOpts
	dirty := sortedDirty(job.DirtyLines)
	job.Mu.Unlock()
	if assPath == "" {
		writeError(w, http.StatusConflict, "尚未导出，请先导出 ass")
		return
	}
	if !opts.SyncTags {
		writeError(w, http.StatusConflict, "导出时未启用同步标识，无法推送；请重新导出")
		return
	}

	var body struct {
		Indices []int `json:"indices"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	indices := body.Indices
	if len(indices) == 0 {
		indices = dirty
	}
	if len(indices) == 0 {
		writeError(w, http.StatusConflict, "没有需要推送的改动")
		return
	}

	content, err := h.engine.Export()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "重新组装字幕失败: "+err.Error())
		return
	}
	post, err := service.PostProcessAss(content, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "后处理失败: "+err.Error())
		return
	}

	var lines []string
	var missing []int
	sort.Ints(indices)
	for _, idx := range indices {
		tag := "st:" + strconv.Itoa(idx+1) // 引擎标记 1-based
		if evs, ok := post.Groups[tag]; ok {
			lines = append(lines, evs...)
		} else {
			missing = append(missing, idx)
		}
	}
	if len(lines) == 0 {
		writeError(w, http.StatusConflict, "选中的行在导出内容里没有对应事件")
		return
	}
	syncFile := assPath + ".sekaisync.txt"
	payload := "; SekaiText sync v1 — 由 Aegisub 宏「SekaiText/从轴机拉取」消费\n" +
		strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(syncFile, []byte(payload), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "写同步文件失败: "+err.Error())
		return
	}

	job.Mu.Lock()
	for _, idx := range indices {
		delete(job.DirtyLines, idx)
	}
	job.Mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"syncFile": syncFile,
		"groups":   len(indices) - len(missing),
		"events":   len(lines),
		"missing":  missing,
	})
}

// EngineTimingSyncPull 回读 Aegisub 保存的 .ass：对每条分句行，用磁盘上第二半的
// 起始时间反推换行帧写回引擎，让轴机列表反映 Aegisub 里的精调、且再导出不丢。
func (h *Handler) EngineTimingSyncPull(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
		return
	}
	job.Mu.Lock()
	assPath := job.ExportAssPath
	job.Mu.Unlock()
	if assPath == "" {
		writeError(w, http.StatusConflict, "尚未导出，请先导出 ass")
		return
	}
	data, err := os.ReadFile(assPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取导出文件失败: "+err.Error())
		return
	}
	groups, _, err := service.ExtractSyncGroups(string(data))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "解析导出文件失败: "+err.Error())
		return
	}

	rawLines, err := h.engine.TimingLines()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取引擎行列表失败: "+err.Error())
		return
	}
	var engineState struct {
		Fps   float64 `json:"fps"`
		Lines []struct {
			Type          string `json:"type"`
			Index         int    `json:"index"`
			StartIndex    int    `json:"startIndex"`
			EndIndex      int    `json:"endIndex"`
			Shake         bool   `json:"shake"`
			UseSeparator  bool   `json:"useSeparator"`
			SeparateFrame int    `json:"separateFrame"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(rawLines, &engineState); err != nil || engineState.Fps <= 0 {
		writeError(w, http.StatusInternalServerError, "引擎行列表格式异常")
		return
	}

	applied, checked := 0, 0
	var skipped []string
	for _, ln := range engineState.Lines {
		if ln.Type != "dialog" || !ln.UseSeparator || ln.Shake {
			continue
		}
		tag := "st:" + strconv.Itoa(ln.Index+1)
		evs, ok := groups[tag]
		if !ok {
			continue
		}
		// 只看正文 Dialogue（滤掉角色名/调试注释）；分句行恰好两半才能反推边界。
		var bodies []service.SyncedEvent
		for _, ev := range evs {
			if ev.Kind != "Dialogue" || ev.Style == "Character" || ev.Style == "Screen" {
				continue
			}
			bodies = append(bodies, ev)
		}
		if len(bodies) != 2 {
			if len(bodies) > 0 {
				skipped = append(skipped, tag)
			}
			continue
		}
		checked++
		sec := service.AssTimeToSeconds(bodies[1].Start)
		if sec < 0 {
			skipped = append(skipped, tag)
			continue
		}
		frame := int(math.Round(sec * engineState.Fps))
		if frame <= ln.StartIndex {
			frame = ln.StartIndex + 1
		}
		if frame >= ln.EndIndex {
			frame = ln.EndIndex - 1
		}
		if frame == ln.SeparateFrame {
			continue
		}
		if _, err := h.engine.TimingLineCall("subtitle.setSeparator",
			map[string]interface{}{"index": ln.Index, "separateFrame": frame}); err != nil {
			skipped = append(skipped, tag)
			continue
		}
		applied++
		// 来自 Aegisub 的值不标脏：推回去只会是空转
	}

	// 同步完成后以当前磁盘状态为基准，status 不再报"外部已改动"
	if fi, err := os.Stat(assPath); err == nil {
		job.Mu.Lock()
		job.ExportMTime = fi.ModTime()
		job.ExportSize = fi.Size()
		job.Mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied": applied,
		"checked": checked,
		"skipped": skipped,
	})
}

func markDirtyLine(job *service.EngineTimingJob, index int) {
	job.Mu.Lock()
	if job.DirtyLines == nil {
		job.DirtyLines = map[int]bool{}
	}
	job.DirtyLines[index] = true
	job.Mu.Unlock()
}

// sortedDirty 在持有 job.Mu 时调用。
func sortedDirty(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
