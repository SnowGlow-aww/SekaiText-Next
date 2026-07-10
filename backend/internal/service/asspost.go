package service

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ASS 导出后处理：把字幕组导出后必跑的 Aegisub 宏（tools.lua）内建进导出流程，
// 并可在 Effect 字段埋入 st:N 行标识，作为与 Aegisub 双向同步的键。
//
// tools.lua 语义（逐条对齐）：
//   cln: 对话行按文本中 \N 个数改样式名（0→1行 1→2行 2→3行；1920×1440 视频加
//        " - 1920*1440" 后缀，2560×1600 无后缀）。与 tools.lua 不同：保留文本里的
//        \N——分行是译者手动断的句，删掉后 2行/3行 样式只剩一行长条（用户反馈）。
//        地点横幅同理改名 BannerMask→遮罩、BannerText→地点名称（团队成品口径：
//        事件标签结构与引擎输出一致，只换样式名套团队样式包的定义）。
//   dlt: 删除样式为 Character / Screen 的行（角色名行与引擎调试注释）。
type AssPostOptions struct {
	Clean         bool   `json:"clean"`
	SyncTags      bool   `json:"syncTags"`
	StyleTemplate string `json:"styleTemplate,omitempty"` // 团队样式模板 .ass 路径，提供 1行/2行/3行 等定义
	// StyleTemplateContent 是模板的整段文本（插件内置模板走这里，随插件分发、
	// 开箱即用不落盘）。StyleTemplate 路径非空时优先，便于用户自定义覆盖。
	StyleTemplateContent string `json:"styleTemplateContent,omitempty"`
	// Staff 非空则在 [Events] 顶部注入一条 staff 制作人员行（0:00:00→0:00:05，
	// 团队成品口径）。样式定义来自团队样式模板（staff 已在模板里）。
	Staff *StaffInfo `json:"staff,omitempty"`
}

// StaffInfo 是 staff 行的可自定义字段（职位标签固定，ID 由用户输入）。
// 全部留空则不生成 staff 行；时轴与轴校&压制相同则合并为「时轴&轴校&压制」。
type StaffInfo struct {
	Group      string `json:"group"`      // 字幕组名，如 PJS字幕组
	Episode    string `json:"episode"`    // 话数，如 第一话
	Title      string `json:"title"`      // 标题，如 三周年
	Recorder   string `json:"recorder"`   // 录制
	Translator string `json:"translator"` // 翻译
	Proofread  string `json:"proofread"`  // 校对
	Timer      string `json:"timer"`      // 时轴
	Suppressor string `json:"suppressor"` // 轴校&压制
}

// buildStaffText 组装 staff 行文本；无任何内容时返回 ""。
func buildStaffText(s StaffInfo) string {
	var parts []string
	if g := strings.TrimSpace(s.Group); g != "" {
		parts = append(parts, "字幕制作 by "+g)
	}
	ep, ti := strings.TrimSpace(s.Episode), strings.TrimSpace(s.Title)
	switch {
	case ep != "" && ti != "":
		parts = append(parts, ep+"："+ti)
	case ep != "":
		parts = append(parts, ep)
	case ti != "":
		parts = append(parts, ti)
	}
	add := func(label, v string) {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, label+"："+v)
		}
	}
	add("录制", s.Recorder)
	add("翻译", s.Translator)
	add("校对", s.Proofread)
	timer, sup := strings.TrimSpace(s.Timer), strings.TrimSpace(s.Suppressor)
	if timer != "" && timer == sup {
		parts = append(parts, "时轴&轴校&压制："+timer)
	} else {
		add("时轴", timer)
		add("轴校&压制", sup)
	}
	if len(parts) == 0 {
		return ""
	}
	return `{\fad(300,200)}` + strings.Join(parts, `\N`)
}

// staffEventLine 按 [Events] 的 Format 组装 staff Dialogue 行。
func staffEventLine(format []string, text string) string {
	fields := make([]string, len(format))
	for i, f := range format {
		switch f {
		case "Start":
			fields[i] = "0:00:00.00"
		case "End":
			fields[i] = "0:00:05.00"
		case "Style":
			fields[i] = "staff"
		case "Name", "Effect":
			fields[i] = ""
		case "Text":
			fields[i] = text
		default: // Layer / Margin* 等数值字段
			fields[i] = "0"
		}
	}
	return "Dialogue: " + strings.Join(fields, ",")
}

type AssPostResult struct {
	Content  string
	Warnings []string
	// Groups: st 标识 → 该组处理后的事件行（完整 "Dialogue: ..." 文本），供同步推送取用。
	Groups map[string][]string
	Order  []string // Groups 键的出现顺序
}

// 引擎在每条对话前后输出的 Screen 注释标记，如 "-----  012  -----  Start"。
var dialogMarkerRe = regexp.MustCompile(`^-+\s+(\d+)\s+-+\s+(.+)$`)

// 地点横幅样式改名映射（团队成品口径，见文件头 cln 注释）。引擎事件的覆写标签
// （\fad\blur\an7\p1 + 左右 clip 展开 + \fshp 位移 + \an5\fs\move）与团队成品
// 逐字一致，改名即等效；「地点角标」marker 团队样式包里没有对应，保持原样。
var bannerStyleRename = map[string]string{
	"BannerMask": "遮罩",
	"BannerText": "地点名称",
}

// assEvent 是 [Events] 里一行的解析结果。Fields 与 Format 字段一一对应，
// Text（最后一个字段）保留其中的逗号。
type assEvent struct {
	Kind   string // "Dialogue" | "Comment"
	Fields []string
}

func (e *assEvent) String() string {
	return e.Kind + ": " + strings.Join(e.Fields, ",")
}

// parseEventLine 按 Format 字段数拆一行事件；不是事件行时返回 nil。
func parseEventLine(line string, nFields int) *assEvent {
	kind := ""
	rest := ""
	switch {
	case strings.HasPrefix(line, "Dialogue: "):
		kind, rest = "Dialogue", line[len("Dialogue: "):]
	case strings.HasPrefix(line, "Comment: "):
		kind, rest = "Comment", line[len("Comment: "):]
	default:
		return nil
	}
	fields := strings.SplitN(rest, ",", nFields)
	if len(fields) != nFields {
		return nil
	}
	return &assEvent{Kind: kind, Fields: fields}
}

// assSection 保序保存原文件的一个小节。
type assSection struct {
	Header string // 如 "[V4+ Styles]"；文件头部无小节的行 Header==""
	Lines  []string
}

func splitSections(content string) []assSection {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	sections := []assSection{{Header: ""}}
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			sections = append(sections, assSection{Header: t})
			continue
		}
		sections[len(sections)-1].Lines = append(sections[len(sections)-1].Lines, ln)
	}
	return sections
}

// styleName 从 "Style: 名字,字体,..." 提取样式名；不是样式行返回 ""。
func styleName(line string) string {
	if !strings.HasPrefix(line, "Style: ") {
		return ""
	}
	rest := line[len("Style: "):]
	if i := strings.IndexByte(rest, ','); i >= 0 {
		return strings.TrimSpace(rest[:i])
	}
	return strings.TrimSpace(rest)
}

// renameStyleLine 把样式定义行的名字替换成 newName。
func renameStyleLine(line, newName string) string {
	rest := line[len("Style: "):]
	if i := strings.IndexByte(rest, ','); i >= 0 {
		return "Style: " + newName + rest[i:]
	}
	return "Style: " + newName
}

// findFormat 在 [Events]/[V4+ Styles] 小节里找 Format 行并返回字段名列表。
func findFormat(lines []string) []string {
	for _, ln := range lines {
		if strings.HasPrefix(ln, "Format:") {
			raw := strings.Split(ln[len("Format:"):], ",")
			out := make([]string, len(raw))
			for i, f := range raw {
				out[i] = strings.TrimSpace(f)
			}
			return out
		}
	}
	return nil
}

func fieldIndex(format []string, name string) int {
	for i, f := range format {
		if f == name {
			return i
		}
	}
	return -1
}

// cleanStyleFor 按 tools.lua 的映射算清理后的样式名："1行/2行/3行" + 分辨率后缀。
// 未覆盖的分辨率沿用无后缀命名（tools.lua 只处理 2560×1600 / 1920×1440）。
func cleanStyleFor(nBreaks, playX, playY int) (string, bool) {
	var base string
	switch nBreaks {
	case 0:
		base = "1行"
	case 1:
		base = "2行"
	case 2:
		base = "3行"
	default:
		return "", false
	}
	if playX == 1920 && playY == 1440 {
		return base + " - 1920*1440", true
	}
	return base, true
}

// PostProcessAss 对引擎导出的 ASS 内容做后处理。见文件头注释。
func PostProcessAss(content string, opts AssPostOptions) (*AssPostResult, error) {
	res := &AssPostResult{Groups: map[string][]string{}}
	if !opts.Clean && !opts.SyncTags && opts.Staff == nil {
		res.Content = content
		return res, nil
	}

	sections := splitSections(content)

	playX, playY := 0, 0
	var eventsIdx = -1
	var stylesIdx = -1
	for i, sec := range sections {
		switch sec.Header {
		case "[Script Info]":
			for _, ln := range sec.Lines {
				if v, ok := strings.CutPrefix(ln, "PlayResX:"); ok {
					playX, _ = strconv.Atoi(strings.TrimSpace(v))
				}
				if v, ok := strings.CutPrefix(ln, "PlayResY:"); ok {
					playY, _ = strconv.Atoi(strings.TrimSpace(v))
				}
			}
		case "[Events]":
			eventsIdx = i
		case "[V4+ Styles]":
			stylesIdx = i
		}
	}
	if eventsIdx < 0 {
		return nil, fmt.Errorf("ASS 内容缺少 [Events] 小节")
	}

	evFormat := findFormat(sections[eventsIdx].Lines)
	styleI := fieldIndex(evFormat, "Style")
	effectI := fieldIndex(evFormat, "Effect")
	textI := fieldIndex(evFormat, "Text")
	if evFormat == nil || styleI < 0 || effectI < 0 || textI != len(evFormat)-1 {
		return nil, fmt.Errorf("无法识别 [Events] 的 Format 行，放弃后处理以免损坏字幕")
	}

	if opts.Clean && (playX == 0 || playY == 0) {
		res.Warnings = append(res.Warnings, "缺少 PlayResX/PlayResY，按无后缀样式名处理")
	}

	usedStyles := map[string]bool{}
	newStyles := map[string]bool{} // 清理改名产生的新样式名
	currentTag := ""               // 当前所属对话组的 st:N；不在组内为空
	var outLines []string

	for _, ln := range sections[eventsIdx].Lines {
		ev := parseEventLine(ln, len(evFormat))
		if ev == nil {
			outLines = append(outLines, ln)
			continue
		}
		style := strings.TrimSpace(ev.Fields[styleI])
		text := ev.Fields[textI]

		// 对话组边界（引擎的 Screen 注释标记）
		if ev.Kind == "Comment" && style == "Screen" {
			if m := dialogMarkerRe.FindStringSubmatch(text); m != nil {
				n, _ := strconv.Atoi(m[1])
				switch strings.TrimSpace(m[2]) {
				case "Start":
					currentTag = "st:" + strconv.Itoa(n)
				case "End":
					if opts.SyncTags && currentTag != "" {
						ev.Fields[effectI] = currentTag
					}
					currentTag = ""
				}
			}
		}

		if opts.SyncTags && currentTag != "" {
			ev.Fields[effectI] = currentTag
		}

		if opts.Clean {
			// dlt: 删角色名行与调试注释行
			if style == "Character" || style == "Screen" {
				continue
			}
			// cln: Line1/2/3 按 \N 个数改名（\N 本身保留，见文件头注释）
			if style == "Line1" || style == "Line2" || style == "Line3" {
				n := strings.Count(text, `\N`)
				if newName, ok := cleanStyleFor(n, playX, playY); ok {
					ev.Fields[styleI] = newName
					newStyles[newName] = true
				} else {
					res.Warnings = append(res.Warnings,
						fmt.Sprintf("某行含 %d 个 \\N，超出 1行/2行/3行 映射，保留原样式 %s", n, style))
				}
			}
			// 地点横幅按团队成品口径改名（事件标签原样保留，只换样式名）
			if newName, ok := bannerStyleRename[style]; ok {
				ev.Fields[styleI] = newName
				newStyles[newName] = true
			}
		}

		usedStyles[strings.TrimSpace(ev.Fields[styleI])] = true
		line := ev.String()
		if tag := strings.TrimSpace(ev.Fields[effectI]); strings.HasPrefix(tag, "st:") {
			if _, ok := res.Groups[tag]; !ok {
				res.Order = append(res.Order, tag)
			}
			res.Groups[tag] = append(res.Groups[tag], line)
		}
		outLines = append(outLines, line)
	}

	// staff 制作人员行：注入到 Format 行之后、所有事件之前（成品里 staff 在最前）。
	if opts.Staff != nil {
		if text := buildStaffText(*opts.Staff); text != "" {
			staffLine := staffEventLine(evFormat, text)
			inserted := false
			for i, ln := range outLines {
				if strings.HasPrefix(ln, "Format:") {
					outLines = append(outLines[:i+1], append([]string{staffLine}, outLines[i+1:]...)...)
					inserted = true
					break
				}
			}
			if !inserted {
				outLines = append([]string{staffLine}, outLines...)
			}
			usedStyles["staff"] = true
		}
	}
	sections[eventsIdx].Lines = outLines

	// 样式表：清理模式下删掉不再使用的引擎样式、补上 1行/2行/3行 的定义
	// （优先取团队样式模板，没有模板就克隆引擎 LineN 的定义并告警）。
	if opts.Clean && stylesIdx >= 0 {
		var tmplStyles map[string]string
		var tmplOrder []string
		if opts.StyleTemplate != "" {
			var err error
			tmplStyles, tmplOrder, err = loadStyleTemplate(opts.StyleTemplate)
			if err != nil {
				res.Warnings = append(res.Warnings, "样式模板读取失败: "+err.Error())
			}
		} else if opts.StyleTemplateContent != "" {
			var err error
			tmplStyles, tmplOrder, err = parseStyleTemplate(opts.StyleTemplateContent)
			if err != nil {
				res.Warnings = append(res.Warnings, "内置样式模板解析失败: "+err.Error())
			}
		}

		engineDefs := map[string]string{}
		var kept []string
		for _, ln := range sections[stylesIdx].Lines {
			name := styleName(ln)
			if name == "" {
				kept = append(kept, ln)
				continue
			}
			engineDefs[name] = ln
			switch name {
			case "Line1", "Line2", "Line3", "Character", "Screen", "BannerMask", "BannerText":
				if !usedStyles[name] {
					continue // 已无事件引用，删定义
				}
			}
			if tmpl, ok := tmplStyles[name]; ok {
				ln = tmpl // 同名以团队模板为准
			}
			kept = append(kept, ln)
		}

		present := map[string]bool{}
		for _, ln := range kept {
			if n := styleName(ln); n != "" {
				present[n] = true
			}
		}
		// 先补事件实际用到的新样式（模板定义优先，缺了才克隆引擎定义改名）
		type styleFill struct{ name, src string }
		var fills []styleFill
		for _, base := range []string{"1行", "2行", "3行"} {
			src := map[string]string{"1行": "Line1", "2行": "Line2", "3行": "Line3"}[base]
			fills = append(fills, styleFill{base, src}, styleFill{base + " - 1920*1440", src})
		}
		// 固定顺序追加（map 遍历顺序不定，别让导出产物的样式顺序抖动）
		fills = append(fills, styleFill{"遮罩", "BannerMask"}, styleFill{"地点名称", "BannerText"})
		for _, f := range fills {
			if !newStyles[f.name] || present[f.name] {
				continue
			}
			if tmpl, ok := tmplStyles[f.name]; ok {
				kept = append(kept, tmpl)
			} else {
				if def, ok := engineDefs[f.src]; ok {
					kept = append(kept, renameStyleLine(def, f.name))
				}
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("未配置团队样式模板，样式「%s」暂用引擎默认定义，渲染效果可能与成品不符", f.name))
			}
			present[f.name] = true
		}
		// 模板里其余样式一并带上（标题等），方便 Aegisub 内直接可用
		for _, name := range tmplOrder {
			if !present[name] {
				kept = append(kept, tmplStyles[name])
				present[name] = true
			}
		}
		sections[stylesIdx].Lines = kept
	}

	var sb strings.Builder
	for i, sec := range sections {
		if sec.Header != "" {
			sb.WriteString(sec.Header)
			sb.WriteString("\n")
		}
		for _, ln := range sec.Lines {
			sb.WriteString(ln)
			sb.WriteString("\n")
		}
		_ = i
	}
	res.Content = strings.TrimRight(sb.String(), "\n") + "\n"
	return res, nil
}

// loadStyleTemplate 读团队样式模板文件，取所有 Style: 行（不限小节，容忍纯样式片段）。
func loadStyleTemplate(path string) (map[string]string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return parseStyleTemplate(string(data))
}

func parseStyleTemplate(content string) (map[string]string, []string, error) {
	styles := map[string]string{}
	var order []string
	for _, ln := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if name := styleName(strings.TrimSpace(ln)); name != "" {
			if _, dup := styles[name]; !dup {
				order = append(order, name)
			}
			styles[name] = strings.TrimSpace(ln)
		}
	}
	if len(styles) == 0 {
		return nil, nil, fmt.Errorf("模板里没有找到任何 Style: 行")
	}
	return styles, order, nil
}

// --- Aegisub → 轴机 方向：从磁盘上的 .ass 提取同步组 ---

// SyncedEvent 是磁盘 ass 里一条带 st: 标识的事件。
type SyncedEvent struct {
	Kind  string
	Start string // ASS 时间原文，如 0:00:05.70
	End   string
	Style string
	Text  string
	Raw   string
}

// ExtractSyncGroups 解析 ass 内容里带 st:N 标识的事件，按标识分组、保序。
func ExtractSyncGroups(content string) (map[string][]SyncedEvent, []string, error) {
	sections := splitSections(content)
	for _, sec := range sections {
		if sec.Header != "[Events]" {
			continue
		}
		format := findFormat(sec.Lines)
		styleI := fieldIndex(format, "Style")
		effectI := fieldIndex(format, "Effect")
		textI := fieldIndex(format, "Text")
		startI := fieldIndex(format, "Start")
		endI := fieldIndex(format, "End")
		if format == nil || styleI < 0 || effectI < 0 || textI != len(format)-1 || startI < 0 || endI < 0 {
			return nil, nil, fmt.Errorf("无法识别 [Events] 的 Format 行")
		}
		groups := map[string][]SyncedEvent{}
		var order []string
		for _, ln := range sec.Lines {
			ev := parseEventLine(ln, len(format))
			if ev == nil {
				continue
			}
			tag := strings.TrimSpace(ev.Fields[effectI])
			if !strings.HasPrefix(tag, "st:") {
				continue
			}
			if _, ok := groups[tag]; !ok {
				order = append(order, tag)
			}
			groups[tag] = append(groups[tag], SyncedEvent{
				Kind:  ev.Kind,
				Start: strings.TrimSpace(ev.Fields[startI]),
				End:   strings.TrimSpace(ev.Fields[endI]),
				Style: strings.TrimSpace(ev.Fields[styleI]),
				Text:  ev.Fields[textI],
				Raw:   ln,
			})
		}
		return groups, order, nil
	}
	return nil, nil, fmt.Errorf("ASS 内容缺少 [Events] 小节")
}

// AssTimeToSeconds 把 "H:MM:SS.CC" 转成秒；解析失败返回 -1。
func AssTimeToSeconds(s string) float64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return -1
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return -1
	}
	return float64(h)*3600 + float64(m)*60 + sec
}
