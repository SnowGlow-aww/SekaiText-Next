package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 迷你版引擎导出：一条不分句对话(2行译文含\N)、一条分句对话(两半)、一条 banner。
const sampleAss = `[Script Info]
Title: Test
PlayResX: 2560
PlayResY: 1600

[V4+ Styles]
Format: Name, Fontname, Fontsize
Style: Line1,FOT-Rodin,60
Style: Line2,FOT-Rodin,60
Style: Line3,FOT-Rodin,60
Style: Character,FOT-Rodin,54
Style: Screen,FOT-Rodin,60
Style: BannerText,FOT-Rodin,60

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Comment: 0,0:00:01.00,0:00:03.00,Screen,,0,0,0,,-----  001  -----  Start
Dialogue: 0,0:00:01.00,0:00:03.00,Line2,,0,0,0,,第一句上\N第一句下
Dialogue: 0,0:00:01.00,0:00:03.00,Character,,0,0,0,,咲希
Comment: 0,0:00:01.00,0:00:03.00,Screen,,0,0,0,,-----  001  -----  End
Comment: 0,0:00:05.70,0:00:15.48,Screen,,0,0,0,,-----  002  -----  Start
Comment: 0,0:00:05.70,0:00:15.48,Screen,,0,0,0,,-----  002  -----  Line 1 ↓
Dialogue: 0,0:00:05.70,0:00:11.86,Line3,,0,0,0,,长句前半，带逗号
Dialogue: 0,0:00:05.70,0:00:11.86,Character,,0,0,0,,穗波
Comment: 0,0:00:05.70,0:00:15.48,Screen,,0,0,0,,-----  002  -----  Line 2 ↓
Dialogue: 0,0:00:11.86,0:00:15.48,Line3,,0,0,0,,长句后半
Dialogue: 0,0:00:11.86,0:00:15.48,Character,,0,0,0,,穗波
Comment: 0,0:00:05.70,0:00:15.48,Screen,,0,0,0,,-----  002  -----  End
Dialogue: 0,0:00:20.00,0:00:22.00,BannerText,,0,0,0,,场景横幅
`

func TestPostProcessCleanAndTags(t *testing.T) {
	post, err := PostProcessAss(sampleAss, AssPostOptions{Clean: true, SyncTags: true})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	out := post.Content

	// dlt：Character/Screen 行全删
	if strings.Contains(out, "Character,") || strings.Contains(out, ",Screen,") {
		t.Errorf("Character/Screen 行未删干净:\n%s", out)
	}
	// cln：一个\N → 2行；分句半行无\N → 1行；\N 本身保留（分行是译者手动断的句，
	// 删掉后 2行/3行 样式只剩一行长条——用户反馈后改为保留）
	if !strings.Contains(out, ",2行,") {
		t.Errorf("含一个\\N 的行应改为 2行 样式")
	}
	if !strings.Contains(out, `第一句上\N第一句下`) {
		t.Errorf("\\N 应保留在文本中:\n%s", out)
	}
	if !strings.Contains(out, ",1行,") {
		t.Errorf("分句半行应为 1行 样式")
	}
	// banner 按团队成品口径改名：BannerText→地点名称（事件文本原样保留）
	if !strings.Contains(out, ",地点名称,") || !strings.Contains(out, "场景横幅") {
		t.Errorf("banner 行应改用 地点名称 样式且文本保留:\n%s", out)
	}
	if strings.Contains(out, ",BannerText,") {
		t.Errorf("banner 事件不应再引用引擎样式 BannerText")
	}
	// 同步标识：对话组打上 st:N，banner 不打
	if !strings.Contains(out, ",st:1,第一句上") {
		t.Errorf("第一组应带 st:1 标识:\n%s", out)
	}
	if !strings.Contains(out, ",st:2,长句前半，带逗号") || !strings.Contains(out, ",st:2,长句后半") {
		t.Errorf("分句两半应都带 st:2")
	}
	if strings.Contains(out, "场景横幅") && strings.Contains(out, "st:3") {
		t.Errorf("banner 不应有 st 标识")
	}
	// 样式表：Line*/Character/Screen 定义删除，新样式补上定义（无模板时克隆引擎定义）
	if strings.Contains(out, "Style: Line2,") || strings.Contains(out, "Style: Character,") {
		t.Errorf("不再使用的引擎样式定义应删除")
	}
	if !strings.Contains(out, "Style: 1行,FOT-Rodin") || !strings.Contains(out, "Style: 2行,FOT-Rodin") {
		t.Errorf("应补上 1行/2行 的样式定义:\n%s", out)
	}
	if len(post.Warnings) == 0 {
		t.Errorf("无模板时应有「暂用引擎默认定义」告警")
	}
	// Groups 供推送用
	if len(post.Groups["st:2"]) != 2 {
		t.Errorf("st:2 组应含 2 行(清理后)，得到 %d", len(post.Groups["st:2"]))
	}
}

func TestPostProcessTagsOnlyKeepsEverything(t *testing.T) {
	post, err := PostProcessAss(sampleAss, AssPostOptions{SyncTags: true})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	out := post.Content
	if !strings.Contains(out, "Character") || !strings.Contains(out, "-----  001  -----  Start") {
		t.Errorf("仅打标识时不应删任何行")
	}
	if !strings.Contains(out, `第一句上\N第一句下`) {
		t.Errorf("仅打标识时不应动文本")
	}
	// 组内含注释与角色行
	if len(post.Groups["st:2"]) != 8 {
		t.Errorf("st:2 组应含 8 行(2正文+2角色+4注释)，得到 %d", len(post.Groups["st:2"]))
	}
}

func TestPostProcessStyleTemplate(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "styles.ass")
	if err := os.WriteFile(tmpl, []byte(
		"[V4+ Styles]\nStyle: 1行,思源黑体,72\nStyle: 2行,思源黑体,72\nStyle: 3行,思源黑体,72\nStyle: 标题,思源黑体,90\nStyle: 地点名称,思源黑体,70\nStyle: 遮罩,Arial,100\n"), 0644); err != nil {
		t.Fatal(err)
	}
	post, err := PostProcessAss(sampleAss, AssPostOptions{Clean: true, StyleTemplate: tmpl})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	out := post.Content
	if !strings.Contains(out, "Style: 1行,思源黑体,72") || !strings.Contains(out, "Style: 2行,思源黑体,72") {
		t.Errorf("应采用模板里的样式定义:\n%s", out)
	}
	if !strings.Contains(out, "Style: 标题,思源黑体,90") {
		t.Errorf("模板里的额外样式应一并带上")
	}
	for _, wme := range post.Warnings {
		if strings.Contains(wme, "暂用引擎默认定义") {
			t.Errorf("有模板时不应出现引擎默认定义告警: %v", post.Warnings)
		}
	}
}

func TestPostProcessStyleTemplateContent(t *testing.T) {
	// 插件内置模板走整段文本直传（BOM+CRLF 也要能吃）；显式路径优先于内容。
	content := "\ufeff[V4+ Styles]\r\nStyle: 1行,内容版,70\r\nStyle: 2行,内容版,70\r\nStyle: 3行,内容版,70\r\nStyle: 遮罩,Arial,100\r\nStyle: 地点名称,内容版,70\r\n"
	post, err := PostProcessAss(sampleAss, AssPostOptions{Clean: true, StyleTemplateContent: content})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	if !strings.Contains(post.Content, "Style: 1行,内容版,70") || !strings.Contains(post.Content, "Style: 遮罩,Arial,100") {
		t.Errorf("应采用内容直传模板的样式定义:\n%s", post.Content)
	}
	for _, wme := range post.Warnings {
		if strings.Contains(wme, "暂用引擎默认定义") {
			t.Errorf("有内置模板时不应出现引擎默认定义告警: %v", post.Warnings)
		}
	}

	dir := t.TempDir()
	tmpl := filepath.Join(dir, "custom.ass")
	if err := os.WriteFile(tmpl, []byte("[V4+ Styles]\nStyle: 1行,路径版,72\n"), 0644); err != nil {
		t.Fatal(err)
	}
	post, err = PostProcessAss(sampleAss, AssPostOptions{Clean: true, StyleTemplate: tmpl, StyleTemplateContent: content})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	if !strings.Contains(post.Content, "Style: 1行,路径版,72") {
		t.Errorf("显式模板路径应覆盖内置模板内容:\n%s", post.Content)
	}
}

func TestPostProcess1920Suffix(t *testing.T) {
	src := strings.ReplaceAll(sampleAss, "PlayResX: 2560", "PlayResX: 1920")
	src = strings.ReplaceAll(src, "PlayResY: 1600", "PlayResY: 1440")
	post, err := PostProcessAss(src, AssPostOptions{Clean: true})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	if !strings.Contains(post.Content, ",2行 - 1920*1440,") {
		t.Errorf("1920×1440 视频应使用带后缀样式名:\n%s", post.Content)
	}
}

func TestExtractSyncGroupsAndTimes(t *testing.T) {
	post, err := PostProcessAss(sampleAss, AssPostOptions{Clean: true, SyncTags: true})
	if err != nil {
		t.Fatal(err)
	}
	groups, order, err := ExtractSyncGroups(post.Content)
	if err != nil {
		t.Fatalf("ExtractSyncGroups: %v", err)
	}
	if len(order) != 2 || order[0] != "st:1" || order[1] != "st:2" {
		t.Fatalf("组顺序错误: %v", order)
	}
	evs := groups["st:2"]
	if len(evs) != 2 {
		t.Fatalf("st:2 应有两半, got %d", len(evs))
	}
	if got := AssTimeToSeconds(evs[1].Start); got < 11.85 || got > 11.87 {
		t.Errorf("第二半起始时间解析错误: %v", got)
	}
	if evs[0].Text != "长句前半，带逗号" {
		t.Errorf("文本含逗号时解析被截断: %q", evs[0].Text)
	}
	if AssTimeToSeconds("bad") != -1 {
		t.Errorf("非法时间应返回 -1")
	}
}

func TestPostProcessStaffLine(t *testing.T) {
	// 时轴与轴校&压制不同人：两行分开
	post, err := PostProcessAss(sampleAss, AssPostOptions{Clean: true, Staff: &StaffInfo{
		Group: "PJS字幕组", Episode: "第一话", Title: "三周年",
		Recorder: "八成是茶币币", Translator: "组员A", Proofread: "组员B",
		Timer: "组员C", Suppressor: "组员D",
	}})
	if err != nil {
		t.Fatalf("PostProcessAss: %v", err)
	}
	want := `Dialogue: 0,0:00:00.00,0:00:05.00,staff,,0,0,0,,{\fad(300,200)}字幕制作 by PJS字幕组\N第一话：三周年\N录制：八成是茶币币\N翻译：组员A\N校对：组员B\N时轴：组员C\N轴校&压制：组员D`
	if !strings.Contains(post.Content, want) {
		t.Fatalf("缺少 staff 行:\n%s", post.Content)
	}
	// staff 行必须是 Format 之后的第一条事件
	lines := strings.Split(post.Content, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, "Format: Layer") {
			if !strings.HasPrefix(lines[i+1], "Dialogue: 0,0:00:00.00,0:00:05.00,staff") {
				t.Fatalf("staff 行不在事件区顶部: %q", lines[i+1])
			}
			break
		}
	}

	// 时轴 == 轴校&压制：合并为一行
	post, err = PostProcessAss(sampleAss, AssPostOptions{Staff: &StaffInfo{
		Group: "PJS字幕组", Timer: "组员C", Suppressor: "组员C",
	}})
	if err != nil {
		t.Fatalf("PostProcessAss(merge): %v", err)
	}
	if !strings.Contains(post.Content, `时轴&轴校&压制：组员C`) {
		t.Fatalf("同一人未合并: %s", post.Content)
	}
	if strings.Contains(post.Content, `\N时轴：组员C`) {
		t.Fatalf("合并后不应再有单独时轴行")
	}

	// 全空：不生成 staff 行
	post, err = PostProcessAss(sampleAss, AssPostOptions{Staff: &StaffInfo{}})
	if err != nil {
		t.Fatalf("PostProcessAss(empty): %v", err)
	}
	if strings.Contains(post.Content, ",staff,") {
		t.Fatalf("全空不应生成 staff 行")
	}
}
