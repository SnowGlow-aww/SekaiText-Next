package api

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"sekaitext/backend/internal/service"
)

// 干净的 ASCII 资产名（语音文件夹候选的可信判据）：坏日文 ScenarioId 匹配不上。
var cleanAssetIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// 行列表 / 分句编辑 / Aegisub 同步。全部绑定到具体 taskId（同 export 的语义），
// 每个任务独占一个引擎进程：任务被关闭/替换后这些端点 404，杜绝编辑到别的任务。

// EngineTimingLines 代理引擎的 subtitle.lines（引擎是唯一权威，broker 不缓存）。
// 运行中也可调用：引擎只返回已定稿的行，插件用它渲染实时增长的行列表。
func (h *Handler) EngineTimingLines(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return
	}
	job, ok := h.engineTimingJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	raw, err := h.engine.TimingLines(job)
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
	raw, err := h.engine.TimingLineCall(job, "subtitle.setSeparator", params)
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
	raw, err := h.engine.TimingLineCall(job, "subtitle.setTranslation", params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "修改译文失败: "+err.Error())
		return
	}
	markDirtyLine(job, *body.Index)
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// EngineTimingBannerTranslation 修改地点横幅文本。banner 与 dialog 各自独立编号，
// 使用单独端点/IPC 方法，避免相同 index 误改到一条对话；横幅不参与 st:N 双向同步，
// 保存后由 autosave/重新导出写入 ass。
func (h *Handler) EngineTimingBannerTranslation(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
		return
	}
	var body struct {
		Index *int    `json:"index"`
		Text  *string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Index == nil || body.Text == nil {
		writeError(w, http.StatusBadRequest, "index 和 text 必填")
		return
	}
	raw, err := h.engine.TimingLineCall(job, "subtitle.setBannerTranslation", map[string]interface{}{
		"index": *body.Index,
		"text":  *body.Text,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "修改地点横幅失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// EngineTimingAutosave 把当前引擎字幕（同导出口径后处理）写到 <outputDir>/autosave.ass。
// 与正式导出的区别：不更新导出/同步基线（ExportAssPath/MTime/DirtyLines 全不动），
// 纯粹是逐行微调后的落盘保险——崩溃/误退后打开 autosave.ass 即可拿回全部微调。
func (h *Handler) EngineTimingAutosave(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
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

	content, err := h.engine.Export(job)
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
	// 并行多任务时各任务的保险文件不能互相覆盖：按剧本名区分。
	job.Mu.Lock()
	scriptPath := job.ScriptPath
	job.Mu.Unlock()
	base := strings.TrimSuffix(assFileNameFor(scriptPath), ".ass")
	assPath := filepath.Join(outDir, "autosave-"+base+".ass")
	if err := os.WriteFile(assPath, []byte(content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "写入失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"assPath": assPath})
}

// EngineTimingLineEstimate 按打字速度估算给定文本分割点对应的换行帧（只算不落地）。
func (h *Handler) EngineTimingLineEstimate(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
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
	raw, err := h.engine.TimingLineCall(job, "subtitle.estimateSeparator", params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "估算失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// EngineTimingLineVoicePauses 语音停顿候选：按行定位剧本里的 VoiceId → 直连
// exmeaning 拉音频（会话级本地缓存）→ ffmpeg silencedetect 找语句间停顿 →
// 换算成视频帧，给分句微调当候选换行点（打轴习惯：人声按语音节奏分句）。
func (h *Handler) EngineTimingLineVoicePauses(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireDoneTimingJob(w, r)
	if !ok {
		return
	}
	var body struct {
		Index *int `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Index == nil {
		writeError(w, http.StatusBadRequest, "index 必填")
		return
	}

	rawLines, err := h.engine.TimingLines(job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取引擎行列表失败: "+err.Error())
		return
	}
	var st struct {
		Fps   float64 `json:"fps"`
		Lines []struct {
			Type       string `json:"type"`
			Index      int    `json:"index"`
			StartIndex int    `json:"startIndex"`
			EndIndex   int    `json:"endIndex"`
			Body       string `json:"body"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(rawLines, &st); err != nil || st.Fps <= 0 {
		writeError(w, http.StatusInternalServerError, "引擎行列表格式异常")
		return
	}
	// 目标行 + 它是同文本对话行里的第几次出现（用于在剧本 TalkData 里对位——
	// 引擎行序与 TalkData 序未必一一对应，按原文匹配最稳）
	compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
	targetIdx := -1
	occurrence := 0
	var targetBody string
	var startFrame, endFrame int
	for i, ln := range st.Lines {
		if ln.Type != "dialog" {
			continue
		}
		if ln.Index == *body.Index {
			targetIdx = i
			targetBody = compact(ln.Body)
			startFrame, endFrame = ln.StartIndex, ln.EndIndex
			break
		}
	}
	if targetIdx < 0 {
		writeError(w, http.StatusNotFound, "行不存在")
		return
	}
	for _, ln := range st.Lines[:targetIdx] {
		if ln.Type == "dialog" && compact(ln.Body) == targetBody {
			occurrence++
		}
	}

	job.Mu.Lock()
	scriptPath := job.ScriptPath
	job.Mu.Unlock()
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取剧本失败: "+err.Error())
		return
	}
	var story service.UnityStoryData
	if err := json.Unmarshal(data, &story); err != nil {
		writeError(w, http.StatusInternalServerError, "解析剧本失败: "+err.Error())
		return
	}
	var voice *service.VoiceData
	seen := 0
	for i := range story.TalkData {
		if compact(story.TalkData[i].Body) != targetBody {
			continue
		}
		if seen == occurrence {
			if len(story.TalkData[i].Voices) > 0 {
				voice = &story.TalkData[i].Voices[0]
			}
			break
		}
		seen++
	}
	if voice == nil || voice.VoiceID == "" {
		// 虚拟歌手等无语音台词：明确告知，前端引导用「按字数均分」
		writeJSON(w, http.StatusOK, map[string]interface{}{"noVoice": true, "pauses": []interface{}{}})
		return
	}

	// 语音文件夹名候选（按可信度排序，Analyze 逐个尝试）：
	//   1. 剧本 JSON 自带的 ScenarioId——仅当是干净的 ASCII 资产名才可信（老卡面存在
	//      "★4冬弥・泉_前半" 这类坏日文名）。festival/活动/初始卡面的本地文件名是
	//      app 合成的展示名（festival_020_nene_01），真实语音文件夹是资源名
	//      （015054_nene01），只按文件名拼必 404——真机 birth2026-nene 实锤。
	//   2. 本地文件名兜底：活动/主线的文件名与资源名一致，且坏 ScenarioId 时别无他选。
	fileBase := strings.TrimSuffix(filepath.Base(scriptPath), filepath.Ext(scriptPath))
	var scenarioIDs []string
	if cleanAssetIDRe.MatchString(story.ScenarioID) {
		scenarioIDs = append(scenarioIDs, story.ScenarioID)
	}
	scenarioIDs = append(scenarioIDs, fileBase)
	info, err := h.voiceAlign.Analyze(scenarioIDs, voice.VoiceID, voice.Character2dId)
	if err != nil {
		writeError(w, http.StatusBadGateway, "语音获取/分析失败: "+err.Error())
		return
	}

	// 停顿中点 → 帧（语音起点≈台词框出现帧），并夹进本行帧区间
	type pauseOut struct {
		Frame    int     `json:"frame"`
		TimeSec  float64 `json:"timeSec"`
		Duration float64 `json:"durationSec"`
	}
	var pauses []pauseOut
	seenFrame := map[int]bool{}
	for _, p := range info.Pauses {
		mid := (p.Start + p.End) / 2
		frame := startFrame + int(math.Round(mid*st.Fps))
		if frame <= startFrame {
			frame = startFrame + 1
		}
		if frame >= endFrame {
			frame = endFrame - 1
		}
		if seenFrame[frame] {
			continue
		}
		seenFrame[frame] = true
		pauses = append(pauses, pauseOut{Frame: frame, TimeSec: mid, Duration: p.End - p.Start})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"voiceId":     voice.VoiceID,
		"durationSec": info.Duration,
		"pauses":      pauses,
	})
}

// EngineTimingFrame 取指定帧画面（base64 jpeg），分隔帧微调的所见即所得预览。
func (h *Handler) EngineTimingFrame(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || !h.engine.Available() {
		writeError(w, http.StatusServiceUnavailable, "打轴内核未安装")
		return
	}
	job, ok := h.engineTimingJob(r.URL.Query().Get("task"))
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	frame, err := strconv.Atoi(r.URL.Query().Get("frame"))
	if err != nil || frame < 0 {
		writeError(w, http.StatusBadRequest, "frame 必须是非负整数")
		return
	}
	maxWidth, _ := strconv.Atoi(r.URL.Query().Get("maxWidth"))
	raw, rerr := h.engine.TimingFrame(job, frame, maxWidth)
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

	content, err := h.engine.Export(job)
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
	// 固定名副本：宏在缺 lfs 模块的 Aegisub 环境（个别 3.2.2 安装）无法扫目录
	// 找最新 *.sekaisync.txt，回落读取这个固定名文件。每次推送覆盖。
	_ = os.WriteFile(filepath.Join(filepath.Dir(assPath), "_sekaitext.sekaisync.txt"), []byte(payload), 0644)

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

// assOverrideTagRe 匹配 ASS 覆写标签块（打字机逐字 alpha 标签等）。
var assOverrideTagRe = regexp.MustCompile(`\{[^{}]*\}`)

// normalizeAssText 把 ASS 文本与引擎译文拉到同一口径再比较：
// \N/\n → 真换行；省略号按引擎打字机的替换规则归一（…→...、"... ..."→......）。
func normalizeAssText(s string) string {
	s = strings.ReplaceAll(s, `\N`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "…", "...")
	s = strings.ReplaceAll(s, "... ...", "......")
	return s
}

// trimAllLen 对齐引擎 TrimAll().Length 的计数口径（C# UTF-16 长度；去首尾空白与
// 换行/\R/\N 标记），分割点索引按此传给引擎。
func trimAllLen(s string) int {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, `\R`, "")
	s = strings.ReplaceAll(s, `\N`, "")
	return len(utf16.Encode([]rune(s)))
}

// EngineTimingSyncPull 回读 Aegisub 保存的 .ass：
//   - 译文：剥掉覆写标签后与引擎当前译文不一致的行，写回引擎（在 Aegisub 里改字
//     从此不再被下次导出冲掉）；
//   - 分句行：用磁盘上第二半的起始时间反推换行帧写回引擎。
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
	// 读盘前先记 mtime/size，作为"所读内容对应"的同步基线（下方处理完回写）。
	// 若耗时处理期间 Aegisub 又保存了一次，磁盘会领先 fi0，下次 status 判定不相等
	// → 前端轮询自动再 pull 补齐；宁可多拉一次，也不能漏掉那次改动。
	fi0, statErr := os.Stat(assPath)
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

	rawLines, err := h.engine.TimingLines(job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取引擎行列表失败: "+err.Error())
		return
	}
	var engineState struct {
		Fps   float64 `json:"fps"`
		Lines []struct {
			Type           string `json:"type"`
			Index          int    `json:"index"`
			StartIndex     int    `json:"startIndex"`
			EndIndex       int    `json:"endIndex"`
			Shake          bool   `json:"shake"`
			UseSeparator   bool   `json:"useSeparator"`
			SeparateFrame  int    `json:"separateFrame"`
			Body           string `json:"body"`
			BodyTranslated string `json:"bodyTranslated"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(rawLines, &engineState); err != nil || engineState.Fps <= 0 {
		writeError(w, http.StatusInternalServerError, "引擎行列表格式异常")
		return
	}

	applied, textApplied, checked := 0, 0, 0
	var skipped []string
	for _, ln := range engineState.Lines {
		if ln.Type != "dialog" {
			continue
		}
		tag := "st:" + strconv.Itoa(ln.Index+1)
		evs, ok := groups[tag]
		if !ok {
			continue
		}
		// 只看正文 Dialogue（滤掉角色名/调试注释）。
		var bodies []service.SyncedEvent
		for _, ev := range evs {
			if ev.Kind != "Dialogue" || ev.Style == "Character" || ev.Style == "Screen" {
				continue
			}
			bodies = append(bodies, ev)
		}
		if len(bodies) == 0 {
			continue
		}

		// --- 译文回读。抖动行导出为逐帧多事件，无法唯一还原文本，跳过。 ---
		if !ln.Shake && len(bodies) <= 2 {
			engineText := normalizeAssText(ln.BodyTranslated)
			var newText string
			if len(bodies) == 1 {
				newText = normalizeAssText(assOverrideTagRe.ReplaceAllString(bodies[0].Text, ""))
			} else {
				h0 := normalizeAssText(assOverrideTagRe.ReplaceAllString(bodies[0].Text, ""))
				h1 := normalizeAssText(assOverrideTagRe.ReplaceAllString(bodies[1].Text, ""))
				if strings.Contains(engineText, "\n") {
					newText = h0 + "\n" + h1 // 引擎侧本就是显式换行（QuickEdit 语义），保持
				} else {
					newText = h0 + h1 // 引擎侧按索引分半，别凭空引入换行
				}
			}
			// 未翻译的行导出的是原文——原样回读会把原文当译文写回，跳过。
			untranslatedEcho := engineText == "" && newText == normalizeAssText(ln.Body)
			if newText != "" && newText != engineText && !untranslatedEcho {
				params := map[string]interface{}{
					"index": ln.Index, "text": newText, "useSeparator": ln.UseSeparator,
				}
				if _, err := h.engine.TimingLineCall(job, "subtitle.setTranslation", params); err != nil {
					skipped = append(skipped, tag)
				} else {
					textApplied++
					if len(bodies) == 2 && !strings.Contains(newText, "\n") {
						// 无显式换行时 setTranslation 不动分割点；用户改写两半后
						// 边界=前半长度（引擎 TrimAll 计数口径）。
						h0 := normalizeAssText(assOverrideTagRe.ReplaceAllString(bodies[0].Text, ""))
						_, _ = h.engine.TimingLineCall(job, "subtitle.setSeparator",
							map[string]interface{}{"index": ln.Index, "separatorContentIndex": trimAllLen(h0)})
					}
					// 来自 Aegisub 的值不标脏：推回去只会是空转
				}
			}
		}

		// --- 换行帧回读：分句行恰好两半才能反推边界。 ---
		if !ln.UseSeparator || ln.Shake {
			continue
		}
		if len(bodies) != 2 {
			skipped = append(skipped, tag)
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
		if _, err := h.engine.TimingLineCall(job, "subtitle.setSeparator",
			map[string]interface{}{"index": ln.Index, "separateFrame": frame}); err != nil {
			skipped = append(skipped, tag)
			continue
		}
		applied++
	}

	// 基线用"读盘那一刻"的 mtime/size（fi0），而非处理后再 Stat：处理期间若有
	// 新保存，磁盘已领先 fi0，下次 status 判定不相等→自动再 pull，不丢那次改动。
	if statErr == nil {
		job.Mu.Lock()
		job.ExportMTime = fi0.ModTime()
		job.ExportSize = fi0.Size()
		job.Mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied":     applied,
		"textApplied": textApplied,
		"checked":     checked,
		"skipped":     skipped,
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
