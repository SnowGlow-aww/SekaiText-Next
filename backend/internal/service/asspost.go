package service

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sekaitext/backend/internal/fsutil"
)

// ASS 导出后处理：把字幕组导出后必跑的 Aegisub 宏（tools.lua）内建进导出流程，
// 并可在 Effect 字段埋入 st:N 行标识，作为与 Aegisub 双向同步的键。
//
// tools.lua 语义（逐条对齐）：
//
//	cln: 对话行按引擎样式名改名（Line1→1行 Line2→2行 Line3→3行；1920×1440 视频加
//	     " - 1920*1440" 后缀，2560×1600 无后缀）。与 tools.lua 不同的两点（均为用户反馈）：
//	     ① 行数以原文为准而非数译文 \N——引擎的 LineN 就是剧本原文的换行数，而
//	        译文没手动断行（2行原文配单行中文）或三行长台词被分隔切成两半后，
//	        \N 数会低于原文行数，按 \N 数套样式会把译文压到日文行上；
//	     ② 保留文本里的 \N——分行是译者手动断的句，删掉后多行文本只剩一行长条。
//	     地点横幅同理改名 BannerMask→遮罩、BannerText→地点名称（团队成品口径：
//	     事件标签结构与引擎输出一致，只换样式名套团队样式包的定义）。
//	dlt: 删除样式为 Character / Screen 的行（角色名行与引擎调试注释）。
type AssPostOptions struct {
	Clean        bool `json:"clean"`
	SyncTags     bool `json:"syncTags"`
	SpeakerColor bool `json:"speakerColor"` // 读取角色名并为 26 位主要角色在 Text 首部注入 \3c 代表色描边，其他角色保留默认描边
	// DocumentID scopes sync tags to one immutable timing document. Empty keeps
	// the legacy st:N form for explicitly gated compatibility paths only.
	DocumentID    string `json:"documentId,omitempty"`
	StyleTemplate string `json:"styleTemplate,omitempty"` // 团队样式模板 .ass 路径，提供 1行/2行/3行 等定义
	// StyleTemplateContent 是模板的整段文本（插件内置模板走这里，随插件分发、
	// 开箱即用不落盘）。StyleTemplate 路径非空时优先，便于用户自定义覆盖。
	StyleTemplateContent string `json:"styleTemplateContent,omitempty"`
	// Staff 非空则在 [Events] 顶部注入一条 staff 制作人员行（0:00:00→0:00:05，
	// 团队成品口径）。样式定义来自团队样式模板（staff 已在模板里）。
	Staff *StaffInfo `json:"staff,omitempty"`
}

// StaffInfo 是 staff 行的可自定义字段。Enabled 缺失时按旧插件兼容：仅非空
// 字段输出，旧 suppressor 同时代表轴校与压制；新插件可逐项勾选并分开两人。
type StaffInfo struct {
	Group      string        `json:"group"`      // 字幕组名，如 PJS字幕组
	Episode    string        `json:"episode"`    // 话数，如 第一话
	Title      string        `json:"title"`      // 标题，如 三周年
	Recorder   string        `json:"recorder"`   // 录制
	Translator string        `json:"translator"` // 翻译
	Proofread  string        `json:"proofread"`  // 校对
	Timer      string        `json:"timer"`      // 时轴
	Checker    string        `json:"checker"`    // 轴校
	Suppressor string        `json:"suppressor"` // 压制
	Enabled    *StaffEnabled `json:"enabled,omitempty"`
}

type StaffEnabled struct {
	Group      bool `json:"group"`
	Episode    bool `json:"episode"`
	Title      bool `json:"title"`
	Recorder   bool `json:"recorder"`
	Translator bool `json:"translator"`
	Proofread  bool `json:"proofread"`
	Timer      bool `json:"timer"`
	Checker    bool `json:"checker"`
	Suppressor bool `json:"suppressor"`
}

func sanitizeStaffField(value string) string {
	value = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", `\N`, " ", `\n`, " ").Replace(value)
	return strings.TrimSpace(value)
}

func staffFieldEnabled(s StaffInfo, field string, value string) bool {
	if s.Enabled == nil {
		return sanitizeStaffField(value) != ""
	}
	switch field {
	case "group":
		return s.Enabled.Group
	case "episode":
		return s.Enabled.Episode
	case "title":
		return s.Enabled.Title
	case "recorder":
		return s.Enabled.Recorder
	case "translator":
		return s.Enabled.Translator
	case "proofread":
		return s.Enabled.Proofread
	case "timer":
		return s.Enabled.Timer
	case "checker":
		return s.Enabled.Checker
	case "suppressor":
		return s.Enabled.Suppressor
	}
	return false
}

func staffGroupHeading(value string) string {
	value = sanitizeStaffField(value)
	for _, prefix := range []string{"字幕制作 by", "字幕制作by"} {
		if strings.HasPrefix(value, prefix) {
			name := strings.TrimSpace(strings.TrimPrefix(value, prefix))
			if name == "" {
				return "字幕制作"
			}
			return "字幕制作 by " + name
		}
	}
	if value == "" {
		return "字幕制作"
	}
	return "字幕制作 by " + value
}

// buildStaffText 组装 staff 行文本；未勾选不输出，勾选空值输出职位本身，填写
// 内容则输出「职位：内容」。相同人员只在相邻职责间安全合并。
func buildStaffText(s StaffInfo) string {
	var parts []string
	if staffFieldEnabled(s, "group", s.Group) {
		parts = append(parts, staffGroupHeading(s.Group))
	}

	epEnabled := staffFieldEnabled(s, "episode", s.Episode)
	titleEnabled := staffFieldEnabled(s, "title", s.Title)
	ep, title := sanitizeStaffField(s.Episode), sanitizeStaffField(s.Title)
	if epEnabled || titleEnabled {
		if epEnabled && ep == "" {
			ep = "话数"
		}
		if titleEnabled && title == "" {
			title = "标题"
		}
		switch {
		case epEnabled && titleEnabled:
			parts = append(parts, ep+"："+title)
		case epEnabled:
			parts = append(parts, ep)
		case titleEnabled:
			parts = append(parts, title)
		}
	}

	add := func(label string, enabled bool, value string) {
		if !enabled {
			return
		}
		if value = sanitizeStaffField(value); value != "" {
			parts = append(parts, label+"："+value)
		} else {
			parts = append(parts, label)
		}
	}
	add("录制", staffFieldEnabled(s, "recorder", s.Recorder), s.Recorder)
	add("翻译", staffFieldEnabled(s, "translator", s.Translator), s.Translator)
	add("校对", staffFieldEnabled(s, "proofread", s.Proofread), s.Proofread)

	timer := sanitizeStaffField(s.Timer)
	checker := sanitizeStaffField(s.Checker)
	suppressor := sanitizeStaffField(s.Suppressor)
	timerEnabled := staffFieldEnabled(s, "timer", s.Timer)
	checkerEnabled := staffFieldEnabled(s, "checker", s.Checker)
	suppressorEnabled := staffFieldEnabled(s, "suppressor", s.Suppressor)
	if s.Enabled == nil && checker == "" && suppressor != "" {
		// Legacy suppressor represented the combined 轴校&压制 field.
		checker = suppressor
		checkerEnabled = true
	}
	switch {
	case timerEnabled && checkerEnabled && suppressorEnabled && timer != "" && timer == checker && checker == suppressor:
		parts = append(parts, "时轴&轴校&压制："+timer)
	case checkerEnabled && suppressorEnabled && checker != "" && checker == suppressor:
		add("时轴", timerEnabled, timer)
		parts = append(parts, "轴校&压制："+checker)
	case timerEnabled && checkerEnabled && timer != "" && timer == checker:
		parts = append(parts, "时轴&轴校："+timer)
		add("压制", suppressorEnabled, suppressor)
	default:
		add("时轴", timerEnabled, timer)
		add("轴校", checkerEnabled, checker)
		add("压制", suppressorEnabled, suppressor)
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

var syncDocumentIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// SyncTag is the document-scoped identity encoded in an ASS Effect field.
// Legacy tags have an empty DocumentID and are accepted only when the caller
// has independently established that the ASS is the unique legacy document.
type SyncTag struct {
	DocumentID string
	Line       int
	Legacy     bool
}

func FormatSyncTag(documentID string, line int) string {
	if documentID == "" {
		return "st:" + strconv.Itoa(line)
	}
	return "st:" + documentID + ":" + strconv.Itoa(line)
}

func ParseSyncTag(value string) (SyncTag, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "st:") {
		return SyncTag{}, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "st:"), ":")
	if len(parts) == 1 {
		line, err := strconv.Atoi(parts[0])
		if err != nil || line <= 0 {
			return SyncTag{}, false
		}
		return SyncTag{Line: line, Legacy: true}, true
	}
	if len(parts) != 2 || !syncDocumentIDRe.MatchString(parts[0]) {
		return SyncTag{}, false
	}
	line, err := strconv.Atoi(parts[1])
	if err != nil || line <= 0 {
		return SyncTag{}, false
	}
	return SyncTag{DocumentID: parts[0], Line: line}, true
}

// ValidateSyncGroups rejects cross-document and mixed legacy/current payloads.
// allowLegacy must only be true after the caller proves directory-level
// uniqueness; the tag alone cannot identify its source document.
func ValidateSyncGroups(groups map[string][]SyncedEvent, expectedDocumentID string, allowLegacy bool) error {
	if len(groups) == 0 {
		return fmt.Errorf("ASS 中没有 SekaiText 同步标识")
	}
	documentID := ""
	hasLegacy := false
	for raw := range groups {
		tag, ok := ParseSyncTag(raw)
		if !ok {
			return fmt.Errorf("ASS 含有无法识别的同步标识 %q", raw)
		}
		if tag.Legacy {
			hasLegacy = true
			continue
		}
		if documentID == "" {
			documentID = tag.DocumentID
		} else if documentID != tag.DocumentID {
			return fmt.Errorf("ASS 混有多个 document ID（%s / %s）", documentID, tag.DocumentID)
		}
	}
	if hasLegacy && documentID != "" {
		return fmt.Errorf("ASS 混用了旧 st:N 与带 document ID 的同步标识")
	}
	if hasLegacy {
		if !allowLegacy {
			return fmt.Errorf("旧 st:N 无法确认文档身份")
		}
		return nil
	}
	if documentID != expectedDocumentID {
		return fmt.Errorf("document ID 不匹配：ASS=%s，任务=%s", documentID, expectedDocumentID)
	}
	return nil
}

// 地点横幅样式改名映射（团队成品口径，见文件头 cln 注释）。引擎事件的覆写标签
// （\fad\blur\an7\p1 + 左右 clip 展开 + \fshp 位移 + \an5\fs\move）与团队成品
// 逐字一致，改名即等效；「地点角标」marker 团队样式包里没有对应，保持原样。
var bannerStyleRename = map[string]string{
	"BannerMask": "遮罩",
	"BannerText": "地点名称",
}

// PJSK 官方 26 位主要角色代表色 (ASS BGR 格式 &HBBGGRR&)
var pjskCharacterColors = map[string]string{
	// [Leo/need]
	"星乃一歌": "&HEEAA33&", "一歌": "&HEEAA33&", "一亲": "&HEEAA33&", "小一": "&HEEAA33&", "ichika": "&HEEAA33&", "星乃": "&HEEAA33&",
	"天马咲希": "&H44DDFF&", "天馬咲希": "&H44DDFF&", "咲希": "&H44DDFF&", "小咲希": "&H44DDFF&", "咲希希": "&H44DDFF&", "saki": "&H44DDFF&",
	"望月穗波": "&H6666EE&", "望月穂波": "&H6666EE&", "穗波": "&H6666EE&", "穂波": "&H6666EE&", "小穗波": "&H6666EE&", "小穗": "&H6666EE&", "honami": "&H6666EE&",
	"日野森志步": "&H22DDBB&", "日野森志歩": "&H22DDBB&", "志步": "&H22DDBB&", "志歩": "&H22DDBB&", "小志步": "&H22DDBB&", "shiho": "&H22DDBB&",

	// [MORE MORE JUMP！]
	"花里实乃理": "&HAACCFF&", "花里みのり": "&HAACCFF&", "花里实乃里": "&HAACCFF&", "实乃理": "&HAACCFF&", "实乃里": "&HAACCFF&", "みのり": "&HAACCFF&", "小实乃理": "&HAACCFF&", "minori": "&HAACCFF&",
	"桐谷遥": "&HFFCC99&", "遥": "&HFFCC99&", "小遥": "&HFFCC99&", "haruka": "&HFFCC99&",
	"桃井爱莉": "&HCCAACC&", "桃井愛莉": "&HCCAACC&", "爱莉": "&HCCAACC&", "愛莉": "&HCCAACC&", "小爱莉": "&HCCAACC&", "airi": "&HCCAACC&",
	"日野森雫": "&HDDEE99&", "雫": "&HDDEE99&", "小雫": "&HDDEE99&", "shizuku": "&HDDEE99&",

	// [Vivid BAD SQUAD]
	"小豆泽心羽": "&H9966FF&", "小豆沢こはね": "&H9966FF&", "心羽": "&H9966FF&", "こはね": "&H9966FF&", "小心羽": "&H9966FF&", "kohane": "&H9966FF&",
	"白石杏": "&HDDBB00&", "杏": "&HDDBB00&", "小杏": "&HDDBB00&", "an": "&HDDBB00&",
	"东云彰人": "&H2277FF&", "東雲彰人": "&H2277FF&", "彰人": "&H2277FF&", "akito": "&H2277FF&",
	"青柳冬弥": "&HDD7700&", "冬弥": "&HDD7700&", "toya": "&HDD7700&", "touya": "&HDD7700&",

	// [Wonderlands×Showtime]
	"天马司": "&H00BBFF&", "天馬司": "&H00BBFF&", "司": "&H00BBFF&", "tsukasa": "&H00BBFF&",
	"凤笑梦": "&HBB66FF&", "鳳えむ": "&HBB66FF&", "笑梦": "&HBB66FF&", "えむ": "&HBB66FF&", "小笑梦": "&HBB66FF&", "emu": "&HBB66FF&",
	"草薙宁宁": "&H99DD33&", "草薙寧々": "&H99DD33&", "宁宁": "&H99DD33&", "寧々": "&H99DD33&", "小宁宁": "&H99DD33&", "nene": "&H99DD33&",
	"神代类": "&HEE88BB&", "神代類": "&HEE88BB&", "类": "&HEE88BB&", "類": "&HEE88BB&", "rui": "&HEE88BB&",

	// [25点，Nightcord见。]
	"宵崎奏": "&H8866BB&", "奏": "&H8866BB&", "かなで": "&H8866BB&", "小奏": "&H8866BB&", "k": "&H8866BB&", "kanade": "&H8866BB&",
	"朝比奈真冬": "&HCC8888&", "朝比奈まふゆ": "&HCC8888&", "真冬": "&HCC8888&", "まふゆ": "&HCC8888&", "雪": "&HCC8888&", "mafuyu": "&HCC8888&",
	"东云绘名": "&H88AACC&", "東雲絵名": "&H88AACC&", "绘名": "&H88AACC&", "絵名": "&H88AACC&", "enana": "&H88AACC&", "ena": "&H88AACC&",
	"晓山瑞希": "&HCCAADD&", "暁山瑞希": "&HCCAADD&", "瑞希": "&HCCAADD&", "みずき": "&HCCAADD&", "小瑞希": "&HCCAADD&", "amia": "&HCCAADD&", "mizuki": "&HCCAADD&",

	// [虚拟歌手 Virtual Singer]
	"初音未来": "&HBBCC33&", "初音ミク": "&HBBCC33&", "初音": "&HBBCC33&", "ミク": "&HBBCC33&", "miku": "&HBBCC33&",
	"镜音铃": "&H11CCFF&", "鏡音リン": "&H11CCFF&", "铃": "&H11CCFF&", "リン": "&H11CCFF&", "rin": "&H11CCFF&",
	"镜音连": "&H11EEFF&", "鏡音レン": "&H11EEFF&", "连": "&H11EEFF&", "レン": "&H11EEFF&", "len": "&H11EEFF&",
	"巡音流歌": "&HCCBBFF&", "巡音ルカ": "&HCCBBFF&", "流歌": "&HCCBBFF&", "ルカ": "&HCCBBFF&", "luka": "&HCCBBFF&",
	"meiko": "&H4444DD&", "MEIKO": "&H4444DD&", "めいこ": "&H4444DD&",
	"kaito": "&HCC6633&", "KAITO": "&HCC6633&", "かいと": "&HCC6633&",
}

// ResolveSpeakerOutlineColor 返回 26 位主要角色的代表色描边色值；未命中其他角色返回 ("", false)。
func ResolveSpeakerOutlineColor(speaker string) (string, bool) {
	s := strings.TrimSpace(speaker)
	if s == "" {
		return "", false
	}
	if col, ok := pjskCharacterColors[s]; ok {
		return col, true
	}
	if col, ok := pjskCharacterColors[strings.ToLower(s)]; ok {
		return col, true
	}
	for k, v := range pjskCharacterColors {
		if strings.Contains(s, k) {
			return v, true
		}
	}
	return "", false
}

var (
	assBraceTagRe    = regexp.MustCompile(`\{[^}]*\}`)
	outlineTagRe     = regexp.MustCompile(`^\{\\3c&H[0-9A-Fa-f]+&?(?:\\3a&H[0-9A-Fa-f]+&?)?\}`)
	leadingAssTagsRe = regexp.MustCompile(`^\{(?:\\[1234]c&H[0-9A-Fa-f]+&?|\\3a&H[0-9A-Fa-f]+&?|\\bord[0-9.]+|\\shad[0-9.]+)+\}`)
)

// StripLeadingColorTags 移除文本首部已有的颜色/描边/阴影标签，避免双层宏重复叠加。
func StripLeadingColorTags(text string) string {
	text = leadingAssTagsRe.ReplaceAllString(text, "")
	text = outlineTagRe.ReplaceAllString(text, "")
	if strings.HasPrefix(text, "{}") {
		text = text[2:]
	}
	return text
}

// ApplyOuterDoubleOutline 生成角色代表色外轮廓 (Layer 0, 4.8px)
func ApplyOuterDoubleOutline(text, colorBgr string, outerBord float64) string {
	clean := StripLeadingColorTags(text)
	tag := fmt.Sprintf(`\1c%s\3c%s\3a&H00&\bord%.1f\shad0`, colorBgr, colorBgr, outerBord)
	if strings.HasPrefix(clean, "{") {
		return "{" + tag + clean[1:]
	}
	return "{" + tag + "}" + clean
}

// ApplyInnerDoubleOutline 生成深灰紫内描边 (Layer 1, 1.8px) 确保白字无论在何种代表色下都具有极致清晰度
func ApplyInnerDoubleOutline(text string, innerBord float64) string {
	clean := StripLeadingColorTags(text)
	tag := fmt.Sprintf(`\1c&HFFFFFF&\3c&H46664749&\3a&H00&\bord%.1f\shad0`, innerBord)
	if strings.HasPrefix(clean, "{") {
		return "{" + tag + clean[1:]
	}
	return "{" + tag + "}" + clean
}

// ApplyOutlineColorToText 将代表色描边注入到文本 Text 字段首部；其他角色(colorBgr=="")则清除多余覆写保留默认样式。
// 注入代表色的同时显式声明 \3a&H00&，避免首字受模板历史 Alpha 通道影响而与打字机后续字符产生透明度视觉色差。
func ApplyOutlineColorToText(text, colorBgr string) string {
	text = outlineTagRe.ReplaceAllString(text, "")
	if colorBgr == "" {
		return text
	}
	if strings.HasPrefix(text, "{") {
		return `{\3c` + colorBgr + `\3a&H00&` + text[1:]
	}
	return `{\3c` + colorBgr + `\3a&H00&}` + text
}

func parseAssTime(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid ASS time format: %q", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	secParts := strings.Split(parts[2], ".")
	if len(secParts) != 2 {
		return 0, fmt.Errorf("invalid seconds format in ASS time: %q", s)
	}
	sec, err := strconv.Atoi(secParts[0])
	if err != nil {
		return 0, err
	}
	csStr := secParts[1]
	if len(csStr) == 1 {
		csStr += "0"
	} else if len(csStr) > 2 {
		csStr = csStr[:2]
	}
	cs, err := strconv.Atoi(csStr)
	if err != nil {
		return 0, err
	}
	ms := (h*3600+m*60+sec)*1000 + cs*10
	return time.Duration(ms) * time.Millisecond, nil
}

func formatAssTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	cs := (ms % 1000) / 10
	secTotal := ms / 1000
	h := secTotal / 3600
	m := (secTotal % 3600) / 60
	s := secTotal % 60
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

func textRuneWeight(s string) float64 {
	clean := assBraceTagRe.ReplaceAllString(s, "")
	clean = strings.ReplaceAll(clean, "\\N", "")
	clean = strings.ReplaceAll(clean, "\\n", "")
	clean = strings.ReplaceAll(clean, "\\h", "")
	clean = strings.TrimSpace(clean)
	var w float64
	for _, r := range clean {
		if r <= 127 {
			w += 0.5
		} else {
			w += 1.0
		}
	}
	return w
}

func splitThreeLines(lines []string) (part1, part2 string, splitIdx int) {
	l0 := lines[0]
	l1 := lines[1]
	l2 := lines[2]

	isTerminator := func(s string) bool {
		s = assBraceTagRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			return false
		}
		r := []rune(s)
		last := r[len(r)-1]
		return last == '。' || last == '！' || last == '？' || last == '!' || last == '?' || last == '…' || last == '~' || last == '”' || last == '’'
	}

	isPause := func(s string) bool {
		s = assBraceTagRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			return false
		}
		r := []rune(s)
		last := r[len(r)-1]
		return last == '，' || last == ',' || last == '、' || last == '；' || last == ';'
	}

	t0 := isTerminator(l0)
	t1 := isTerminator(l1)

	if t0 && !t1 {
		return l0, l1 + "\\N" + l2, 1
	}
	if t1 && !t0 {
		return l0 + "\\N" + l1, l2, 2
	}

	p0 := isPause(l0)
	p1 := isPause(l1)
	if p0 && !p1 {
		return l0, l1 + "\\N" + l2, 1
	}
	if p1 && !p0 {
		return l0 + "\\N" + l1, l2, 2
	}

	w0 := textRuneWeight(l0)
	w1 := textRuneWeight(l1)
	w2 := textRuneWeight(l2)

	diff1 := math.Abs(w0 - (w1 + w2))
	diff2 := math.Abs((w0 + w1) - w2)

	if diff1 <= diff2 {
		return l0, l1 + "\\N" + l2, 1
	}
	return l0 + "\\N" + l1, l2, 2
}

func splitMultipleLines(lines []string) (part1, part2 string, splitIdx int) {
	n := len(lines)
	if n == 3 {
		return splitThreeLines(lines)
	}
	splitIdx = n / 2
	if splitIdx < 1 {
		splitIdx = 1
	} else if splitIdx >= n {
		splitIdx = n - 1
	}
	part1 = strings.Join(lines[:splitIdx], "\\N")
	part2 = strings.Join(lines[splitIdx:], "\\N")
	return part1, part2, splitIdx
}

func splitEventIfTooManyLines(ev *assEvent, startI, endI, textI, styleI int, playX, playY int, clean bool) []*assEvent {
	if ev.Kind != "Dialogue" {
		return []*assEvent{ev}
	}
	rawText := ev.Fields[textI]
	normText := strings.ReplaceAll(rawText, "\\n", "\\N")
	normText = strings.ReplaceAll(normText, "\r\n", "\\N")
	normText = strings.ReplaceAll(normText, "\n", "\\N")

	lines := strings.Split(normText, "\\N")
	if len(lines) <= 2 {
		return []*assEvent{ev}
	}

	tStart, err1 := parseAssTime(ev.Fields[startI])
	tEnd, err2 := parseAssTime(ev.Fields[endI])
	if err1 != nil || err2 != nil || tEnd <= tStart {
		return []*assEvent{ev}
	}

	part1Text, part2Text, splitIdx := splitMultipleLines(lines)
	w1 := textRuneWeight(part1Text)
	w2 := textRuneWeight(part2Text)

	totalDuration := tEnd - tStart
	var ratio float64
	if w1+w2 > 0 {
		ratio = w1 / (w1 + w2)
	} else {
		ratio = 0.5
	}

	tMid := tStart + time.Duration(float64(totalDuration)*ratio)
	if totalDuration >= 1000*time.Millisecond {
		minPad := 500 * time.Millisecond
		if float64(totalDuration)*0.2 < float64(minPad) {
			minPad = time.Duration(float64(totalDuration) * 0.2)
		}
		if tMid < tStart+minPad {
			tMid = tStart + minPad
		}
		if tMid > tEnd-minPad {
			tMid = tEnd - minPad
		}
	} else {
		minPad := time.Duration(float64(totalDuration) * 0.15)
		if tMid < tStart+minPad {
			tMid = tStart + minPad
		}
		if tMid > tEnd-minPad {
			tMid = tEnd - minPad
		}
	}

	ev1 := &assEvent{
		Kind:   ev.Kind,
		Fields: append([]string(nil), ev.Fields...),
	}
	ev1.Fields[startI] = formatAssTime(tStart)
	ev1.Fields[endI] = formatAssTime(tMid)
	ev1.Fields[textI] = part1Text

	ev2 := &assEvent{
		Kind:   ev.Kind,
		Fields: append([]string(nil), ev.Fields...),
	}
	ev2.Fields[startI] = formatAssTime(tMid)
	ev2.Fields[endI] = formatAssTime(tEnd)
	ev2.Fields[textI] = part2Text

	lines1Count := splitIdx
	lines2Count := len(lines) - splitIdx
	if clean {
		style1Base := "1行"
		if lines1Count >= 2 {
			style1Base = "2行"
		}
		style2Base := "1行"
		if lines2Count >= 2 {
			style2Base = "2行"
		}
		if newStyle1, ok := cleanStyleFor(style1Base, playX, playY); ok {
			ev1.Fields[styleI] = newStyle1
		} else {
			ev1.Fields[styleI] = style1Base
		}
		if newStyle2, ok := cleanStyleFor(style2Base, playX, playY); ok {
			ev2.Fields[styleI] = newStyle2
		} else {
			ev2.Fields[styleI] = style2Base
		}
	} else {
		style1Base := "Line1"
		if lines1Count >= 2 {
			style1Base = "Line2"
		}
		style2Base := "Line1"
		if lines2Count >= 2 {
			style2Base = "Line2"
		}
		ev1.Fields[styleI] = style1Base
		ev2.Fields[styleI] = style2Base
	}

	res := make([]*assEvent, 0, 4)
	res = append(res, splitEventIfTooManyLines(ev1, startI, endI, textI, styleI, playX, playY, clean)...)
	res = append(res, splitEventIfTooManyLines(ev2, startI, endI, textI, styleI, playX, playY, clean)...)
	return res
}

// split2LineEventToNativeRows 检查对话事件是否包含 2 行文本（含 \N）。
// 若包含 2 行，则拆分为同起止时间的 1行 与 2行 两条独立分行事件，
// 使得 1行落在 MarginV 1295，2行落在 MarginV 1365，锁定 70px 原生垂直行距。
func split2LineEventToNativeRows(ev *assEvent, textI, styleI int, playX, playY int, clean bool) []*assEvent {
	if ev.Kind != "Dialogue" || textI < 0 || styleI < 0 {
		return []*assEvent{ev}
	}
	rawText := ev.Fields[textI]
	normText := strings.ReplaceAll(rawText, "\\n", "\\N")
	normText = strings.ReplaceAll(normText, "\r\n", "\\N")
	normText = strings.ReplaceAll(normText, "\n", "\\N")

	lines := strings.Split(normText, "\\N")
	if len(lines) != 2 {
		return []*assEvent{ev}
	}

	ev1 := &assEvent{
		Kind:   ev.Kind,
		Fields: append([]string(nil), ev.Fields...),
	}
	ev1.Fields[textI] = lines[0]

	ev2 := &assEvent{
		Kind:   ev.Kind,
		Fields: append([]string(nil), ev.Fields...),
	}
	ev2.Fields[textI] = lines[1]

	if clean {
		if s1, ok := cleanStyleFor("1行", playX, playY); ok {
			ev1.Fields[styleI] = s1
		} else {
			ev1.Fields[styleI] = "1行"
		}
		if s2, ok := cleanStyleFor("2行", playX, playY); ok {
			ev2.Fields[styleI] = s2
		} else {
			ev2.Fields[styleI] = "2行"
		}
	} else {
		ev1.Fields[styleI] = "Line1"
		ev2.Fields[styleI] = "Line2"
	}

	return []*assEvent{ev1, ev2}
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

var standardAssStyleFormat = []string{
	"Name", "Fontname", "Fontsize", "PrimaryColour", "SecondaryColour", "OutlineColour", "BackColour",
	"Bold", "Italic", "Underline", "StrikeOut", "ScaleX", "ScaleY", "Spacing", "Angle", "BorderStyle",
	"Outline", "Shadow", "Alignment", "MarginL", "MarginR", "MarginV", "Encoding",
}

func defaultStaffStyleLine(format []string) string {
	fields := make([]string, len(format))
	for i, field := range format {
		switch field {
		case "Name":
			fields[i] = "staff"
		case "Fontname":
			fields[i] = "Arial"
		case "Fontsize":
			fields[i] = "60"
		case "PrimaryColour":
			fields[i] = "&H00FFFFFF"
		case "SecondaryColour":
			fields[i] = "&H000000FF"
		case "OutlineColour", "BackColour":
			fields[i] = "&H00000000"
		case "ScaleX", "ScaleY":
			fields[i] = "100"
		case "BorderStyle":
			fields[i] = "1"
		case "Outline":
			fields[i] = "2"
		case "Alignment":
			fields[i] = "2"
		case "MarginL", "MarginR", "MarginV":
			fields[i] = "10"
		case "Encoding":
			fields[i] = "1"
		default:
			fields[i] = "0"
		}
	}
	return "Style: " + strings.Join(fields, ",")
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

// cleanStyleFor 算清理后的样式名："1行/2行/3行" + 分辨率后缀。行数直接取引擎
// 样式名 Line1/2/3（= 剧本原文换行数），不数译文 \N（见文件头注释①）。
// 未覆盖的分辨率沿用无后缀命名（tools.lua 只处理 2560×1600 / 1920×1440）。
func cleanStyleFor(engineStyle string, playX, playY int) (string, bool) {
	var base string
	switch engineStyle {
	case "Line1":
		base = "1行"
	case "Line2":
		base = "2行"
	case "Line3":
		base = "3行"
	default:
		return "", false
	}
	if playX == 1920 && playY == 1440 {
		return base + " - 1920*1440", true
	}
	return base, true
}

// isInternalStyleTemplate 检查是否加载了内部专属模板（通过模板元数据签名或 internal 路径静默识别）。
func isInternalStyleTemplate(opts AssPostOptions) bool {
	if opts.SpeakerColor {
		return true
	}
	if opts.StyleTemplate != "" {
		if strings.Contains(strings.ToLower(opts.StyleTemplate), "internal") {
			return true
		}
		if data, err := os.ReadFile(opts.StyleTemplate); err == nil {
			s := string(data)
			if strings.Contains(s, "Group-Exclusive-Production") || strings.Contains(s, "Internal-SekaiText") {
				return true
			}
		}
	}
	if opts.StyleTemplateContent != "" {
		if strings.Contains(opts.StyleTemplateContent, "Group-Exclusive-Production") || strings.Contains(opts.StyleTemplateContent, "Internal-SekaiText") {
			return true
		}
	}
	return false
}

// PostProcessAss 对引擎导出的 ASS 内容做后处理。见文件头注释。
func PostProcessAss(content string, opts AssPostOptions) (*AssPostResult, error) {
	enableSpeakerColor := isInternalStyleTemplate(opts)
	res := &AssPostResult{Groups: map[string][]string{}}
	if !opts.Clean && !opts.SyncTags && opts.Staff == nil && !enableSpeakerColor {
		res.Content = content
		return res, nil
	}
	if opts.SyncTags && opts.DocumentID != "" && !syncDocumentIDRe.MatchString(opts.DocumentID) {
		return nil, fmt.Errorf("无效的同步 document ID %q", opts.DocumentID)
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
	nameI := fieldIndex(evFormat, "Name")
	effectI := fieldIndex(evFormat, "Effect")
	textI := fieldIndex(evFormat, "Text")
	layerI := fieldIndex(evFormat, "Layer")
	startI := fieldIndex(evFormat, "Start")
	endI := fieldIndex(evFormat, "End")
	if evFormat == nil || styleI < 0 || effectI < 0 || textI != len(evFormat)-1 {
		return nil, fmt.Errorf("无法识别 [Events] 的 Format 行，放弃后处理以免损坏字幕")
	}

	if opts.Clean && (playX == 0 || playY == 0) {
		res.Warnings = append(res.Warnings, "缺少 PlayResX/PlayResY，按无后缀样式名处理")
	}

	// 建立对话行与 Character 行的说话人(Actor)关联（SekaiTools 引擎默认顺序为 [Character, LineN]，并兼容反向）
	for i, ln := range sections[eventsIdx].Lines {
		ev := parseEventLine(ln, len(evFormat))
		if ev == nil {
			continue
		}
		style := strings.TrimSpace(ev.Fields[styleI])
		if ev.Kind == "Dialogue" && style == "Character" {
			speaker := strings.TrimSpace(assBraceTagRe.ReplaceAllString(ev.Fields[textI], ""))
			found := false
			// 优先向后查找紧随其后的 Dialogue 对话行（SekaiTools 引擎标准顺序）
			for j := i + 1; j < len(sections[eventsIdx].Lines); j++ {
				nxt := parseEventLine(sections[eventsIdx].Lines[j], len(evFormat))
				if nxt == nil {
					continue
				}
				nxtStyle := strings.TrimSpace(nxt.Fields[styleI])
				if nxt.Kind == "Comment" && nxtStyle == "Screen" {
					break
				}
				if nxt.Kind == "Dialogue" && nxtStyle != "Character" && nxtStyle != "staff" {
					if nameI >= 0 && strings.TrimSpace(nxt.Fields[nameI]) == "" {
						nxt.Fields[nameI] = speaker
						sections[eventsIdx].Lines[j] = nxt.String()
						found = true
					}
					break
				}
			}
			if !found {
				// 兼容向前查找
				for j := i - 1; j >= 0; j-- {
					prev := parseEventLine(sections[eventsIdx].Lines[j], len(evFormat))
					if prev == nil {
						continue
					}
					prevStyle := strings.TrimSpace(prev.Fields[styleI])
					if prev.Kind == "Comment" && prevStyle == "Screen" {
						break
					}
					if prev.Kind == "Dialogue" && prevStyle != "Character" && prevStyle != "staff" {
						if nameI >= 0 && strings.TrimSpace(prev.Fields[nameI]) == "" {
							prev.Fields[nameI] = speaker
							sections[eventsIdx].Lines[j] = prev.String()
						}
						break
					}
				}
			}
		}
	}

	usedStyles := map[string]bool{}
	newStyles := map[string]bool{} // 清理改名产生的新样式名
	currentTag := ""               // 当前所属对话组的同步标识；不在组内为空
	var outLines []string
	staffAdded := false

	for _, ln := range sections[eventsIdx].Lines {
		ev := parseEventLine(ln, len(evFormat))
		if ev == nil {
			outLines = append(outLines, ln)
			continue
		}
		style := strings.TrimSpace(ev.Fields[styleI])
		text := ev.Fields[textI]

		// 注入新 staff 时把模板/旧导出中已有的 staff Dialogue 当作可替换槽位，
		// 避免同一个制作组抬头在成片开头出现两遍；说明性 Comment 保留。
		if opts.Staff != nil && ev.Kind == "Dialogue" && style == "staff" {
			continue
		}

		// 对话组边界（引擎的 Screen 注释标记）
		if ev.Kind == "Comment" && style == "Screen" {
			if m := dialogMarkerRe.FindStringSubmatch(text); m != nil {
				n, _ := strconv.Atoi(m[1])
				switch strings.TrimSpace(m[2]) {
				case "Start":
					currentTag = FormatSyncTag(opts.DocumentID, n)
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
			// cln: Line1/2/3 按样式名改名，行数以原文为准（\N 本身保留，见文件头注释）
			if newName, ok := cleanStyleFor(style, playX, playY); ok {
				ev.Fields[styleI] = newName
				newStyles[newName] = true
			}
			// 地点横幅按团队成品口径改名（事件标签原样保留，只换样式名）
			if newName, ok := bannerStyleRename[style]; ok {
				ev.Fields[styleI] = newName
				newStyles[newName] = true
			}
		}

		// 对超过2行的台词自动按时间轴切分为前后两条拼凑轴（确保画面最多显示2行中文，消除溢出遮挡）
		var evSlice []*assEvent
		if ev.Kind == "Dialogue" && startI >= 0 && endI >= 0 && style != "Character" && style != "staff" && style != "Screen" {
			evSlice = splitEventIfTooManyLines(ev, startI, endI, textI, styleI, playX, playY, opts.Clean)
		} else {
			evSlice = []*assEvent{ev}
		}

		var finalEvSlice []*assEvent
		for _, e := range evSlice {
			if e.Kind == "Dialogue" && style != "Character" && style != "staff" && style != "Screen" && (enableSpeakerColor || isInternalStyleTemplate(opts)) {
				finalEvSlice = append(finalEvSlice, split2LineEventToNativeRows(e, textI, styleI, playX, playY, opts.Clean)...)
			} else {
				finalEvSlice = append(finalEvSlice, e)
			}
		}

		for _, subEv := range finalEvSlice {
			currentStyle := strings.TrimSpace(subEv.Fields[styleI])
			newStyles[currentStyle] = true

			if enableSpeakerColor && subEv.Kind == "Dialogue" && (strings.Contains(currentStyle, "行") || strings.Contains(currentStyle, "Line") || currentStyle == "Default") {
				speaker := ""
				if nameI >= 0 {
					speaker = strings.TrimSpace(subEv.Fields[nameI])
				}
				if col, ok := ResolveSpeakerOutlineColor(speaker); ok {
					innerBord, outerBord := 1.8, 3.5
					if playX == 1920 && playY == 1080 {
						innerBord, outerBord = 1.3, 2.4
					} else if playX == 1920 && playY == 1440 {
						innerBord, outerBord = 1.4, 2.7
					}

					// 下层 (Layer 0): 3.5px 角色代表色外轮廓
					evOuter := &assEvent{
						Kind:   subEv.Kind,
						Fields: append([]string(nil), subEv.Fields...),
					}
					if layerI >= 0 {
						evOuter.Fields[layerI] = "0"
					}
					evOuter.Fields[textI] = ApplyOuterDoubleOutline(subEv.Fields[textI], col, outerBord)
					usedStyles[currentStyle] = true
					outerLine := evOuter.String()
					tag := strings.TrimSpace(evOuter.Fields[effectI])
					if _, validTag := ParseSyncTag(tag); validTag {
						if _, ok := res.Groups[tag]; !ok {
							res.Order = append(res.Order, tag)
						}
						res.Groups[tag] = append(res.Groups[tag], outerLine)
					}
					outLines = append(outLines, outerLine)

					// 上层 (Layer 1): 白字 + 1.8px 深灰紫内描边
					if layerI >= 0 {
						subEv.Fields[layerI] = "1"
					}
					subEv.Fields[textI] = ApplyInnerDoubleOutline(subEv.Fields[textI], innerBord)
				}
			}

			usedStyles[strings.TrimSpace(subEv.Fields[styleI])] = true
			line := subEv.String()
			tag := strings.TrimSpace(subEv.Fields[effectI])
			if _, validTag := ParseSyncTag(tag); validTag {
				if _, ok := res.Groups[tag]; !ok {
					res.Order = append(res.Order, tag)
				}
				res.Groups[tag] = append(res.Groups[tag], line)
			}
			outLines = append(outLines, line)
		}
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
			staffAdded = true
		}
	}
	sections[eventsIdx].Lines = outLines
	if staffAdded && stylesIdx < 0 {
		styleSection := assSection{
			Header: "[V4+ Styles]",
			Lines: []string{
				"Format: " + strings.Join(standardAssStyleFormat, ", "),
				defaultStaffStyleLine(standardAssStyleFormat),
			},
		}
		sections = append(sections, assSection{})
		copy(sections[eventsIdx+1:], sections[eventsIdx:])
		sections[eventsIdx] = styleSection
		stylesIdx = eventsIdx
		eventsIdx++
	}

	// 样式表：清理模式下删掉不再使用的引擎样式、补上 1行/2行/3行 的定义
	// （优先取团队样式模板，没有模板就克隆引擎 LineN 的定义并告警）。
	if (opts.Clean || staffAdded) && stylesIdx >= 0 {
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
		firstEngineDef := ""
		var kept []string
		for _, ln := range sections[stylesIdx].Lines {
			name := styleName(ln)
			if name == "" {
				kept = append(kept, ln)
				continue
			}
			engineDefs[name] = ln
			if firstEngineDef == "" {
				firstEngineDef = ln
			}
			if opts.Clean {
				switch name {
				case "Line1", "Line2", "Line3", "Character", "Screen", "BannerMask", "BannerText":
					if !usedStyles[name] {
						continue // 已无事件引用，删定义
					}
				}
				if tmpl, ok := tmplStyles[name]; ok {
					ln = tmpl // 同名以团队模板为准
				}
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
			if !opts.Clean {
				break
			}
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
		if opts.Clean {
			for _, name := range tmplOrder {
				if !present[name] {
					kept = append(kept, tmplStyles[name])
					present[name] = true
				}
			}
		}
		if staffAdded && !present["staff"] {
			switch {
			case tmplStyles["staff"] != "":
				kept = append(kept, tmplStyles["staff"])
			case firstEngineDef != "":
				kept = append(kept, renameStyleLine(firstEngineDef, "staff"))
			default:
				styleFormat := findFormat(sections[stylesIdx].Lines)
				if len(styleFormat) == 0 {
					styleFormat = standardAssStyleFormat
					kept = append([]string{"Format: " + strings.Join(styleFormat, ", ")}, kept...)
				}
				kept = append(kept, defaultStaffStyleLine(styleFormat))
			}
			present["staff"] = true
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
			if _, ok := ParseSyncTag(tag); !ok {
				return nil, nil, fmt.Errorf("ASS 含有无法识别的同步标识 %q", tag)
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

// WriteFileAtomic keeps the ASS-facing service API local while sharing the
// cross-platform unique-temp replacement implementation used by other data.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return fsutil.WriteFileAtomic(path, data, perm)
}
