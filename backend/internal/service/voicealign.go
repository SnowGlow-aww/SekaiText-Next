package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 轴机「语音停顿对齐」：把某句台词的语音拉到本地，用 ffmpeg silencedetect 找
// 说话人语句之间的停顿，供三行分句把换行时机对齐到实际停顿（打轴习惯：人声
// 按语句对应语音节奏，无语音的虚拟歌手台词才按字数/打字速度）。
//
// 存储纪律（用户点名）：
//   - 语音一律直连 exmeaning 源（storage2 优先），绝不走自家 CDN 镜像域名——
//     镜像回源会把音频持久化进 OSS 桶白吃存储；
//   - 本地缓存是会话级的：后端每次启动即清空缓存目录，等效"关闭软件即清空"，
//     崩溃也不会残留。
type VoiceAligner struct {
	cacheDir   string
	ffmpegPath string
	client     *http.Client
}

// NewVoiceAligner 构造并清空上一会话的缓存目录。
func NewVoiceAligner(dataDir, ffmpegPath string) *VoiceAligner {
	dir := filepath.Join(dataDir, "voice-align-cache")
	_ = os.RemoveAll(dir)
	return &VoiceAligner{
		cacheDir:   dir,
		ffmpegPath: ffmpegPath,
		client:     &http.Client{Timeout: 60 * time.Second},
	}
}

// 语音直连源：storage2（用户点名的 exmeaning2）优先，storage 兜底（编辑器播放在用）。
var voiceBases = []string{
	"https://storage2.exmeaning.com/sekai-jp-assets/",
	"https://storage.exmeaning.com/sekai-jp-assets/",
}

// 卡面剧情 scenarioId：6 位数字 + "_"（与 api.VoiceURL 同口径）。
var voiceCardScenarioRe = regexp.MustCompile(`^\d{6}_`)

// voiceRelPath 与 api.VoiceURL 的路径规则一致（partvoice 共享包 / 卡面 / 普通）。
func voiceRelPath(scenarioID, voiceID string, chara2d int) string {
	switch {
	case strings.HasPrefix(voiceID, "partvoice"):
		if c, ok := Character2dByID(chara2d); ok {
			return "sound/scenario/voice/part_voice_" + c.AssetName + "_" + c.Unit + "/" + voiceID + ".mp3"
		}
		return ""
	case voiceCardScenarioRe.MatchString(scenarioID):
		return "sound/card_scenario/voice/" + scenarioID + "/" + voiceID + ".mp3"
	default:
		return "sound/scenario/voice/" + scenarioID + "/" + voiceID + ".mp3"
	}
}

// fetch 下载语音到会话缓存（已存在则直接复用），返回本地路径。
// scenarioIDs 是语音文件夹名的候选列表（按可信度排序，逐个尝试）：剧本 JSON 自带的
// ScenarioId 与本地文件名可能不同（festival/活动/初始卡面的本地名是 app 合成的展示名，
// 如 festival_020_nene_01，真实资源文件夹是 015054_nene01——单用文件名必 404）。
func (va *VoiceAligner) fetch(scenarioIDs []string, voiceID string, chara2d int) (string, error) {
	var rels []string
	seenRel := map[string]bool{}
	for _, sid := range scenarioIDs {
		if sid == "" {
			continue
		}
		rel := voiceRelPath(sid, voiceID, chara2d)
		if rel == "" || seenRel[rel] {
			continue
		}
		seenRel[rel] = true
		rels = append(rels, rel)
	}
	if len(rels) == 0 {
		return "", errors.New("无法解析该语音的下载路径")
	}
	if err := os.MkdirAll(va.cacheDir, 0755); err != nil {
		return "", err
	}
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, voiceID)
	dst := filepath.Join(va.cacheDir, safe+".mp3")
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}

	var lastErr error
	for _, rel := range rels {
		for _, base := range voiceBases {
			resp, err := va.client.Get(base + rel)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				lastErr = fmt.Errorf("HTTP %d (%s)", resp.StatusCode, base+rel)
				continue
			}
			tmp := dst + ".part"
			f, err := os.Create(tmp)
			if err != nil {
				resp.Body.Close()
				return "", err
			}
			_, cerr := io.Copy(f, resp.Body)
			resp.Body.Close()
			f.Close()
			if cerr != nil {
				_ = os.Remove(tmp)
				lastErr = cerr
				continue
			}
			if err := os.Rename(tmp, dst); err != nil {
				return "", err
			}
			return dst, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("语音源均不可达")
	}
	return "", fmt.Errorf("下载语音失败: %w", lastErr)
}

// VoicePauseSpan 是语音中段的一个静音区间（秒）。
type VoicePauseSpan struct {
	Start float64
	End   float64
}

// VoiceAlignInfo 是一段语音的分析结果。
type VoiceAlignInfo struct {
	Duration float64 // 语音总时长（秒）
	Pauses   []VoicePauseSpan
}

var (
	ffDurationRe   = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)
	ffSilStartRe   = regexp.MustCompile(`silence_start:\s*(-?\d+(?:\.\d+)?)`)
	ffSilEndRe     = regexp.MustCompile(`silence_end:\s*(-?\d+(?:\.\d+)?)`)
	pauseEdgeGuard = 0.12 // 掐头去尾：开头/结尾的静音不是"语句间停顿"
)

// Analyze 下载并分析一段语音，返回总时长与语句间停顿区间。
// scenarioIDs 语义见 fetch：语音文件夹名候选列表，按序尝试。
func (va *VoiceAligner) Analyze(scenarioIDs []string, voiceID string, chara2d int) (*VoiceAlignInfo, error) {
	if va.ffmpegPath == "" {
		return nil, errors.New("未配置 ffmpeg，无法分析语音")
	}
	if _, err := os.Stat(va.ffmpegPath); err != nil {
		return nil, errors.New("ffmpeg 不存在: " + va.ffmpegPath)
	}
	audio, err := va.fetch(scenarioIDs, voiceID, chara2d)
	if err != nil {
		return nil, err
	}

	// silencedetect：-32dB 低于语音包络、0.25s 起判，足够挑出语句间换气/停句。
	cmd := exec.Command(va.ffmpegPath, "-hide_banner", "-nostats",
		"-i", audio, "-af", "silencedetect=noise=-32dB:d=0.25", "-f", "null", "-")
	HideConsoleWindow(cmd)
	out, runErr := cmd.CombinedOutput()
	text := string(out)

	info := &VoiceAlignInfo{}
	if m := ffDurationRe.FindStringSubmatch(text); m != nil {
		hh, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		ss, _ := strconv.ParseFloat(m[3], 64)
		info.Duration = float64(hh)*3600 + float64(mm)*60 + ss
	}
	if info.Duration <= 0 {
		if runErr != nil {
			return nil, fmt.Errorf("ffmpeg 分析失败: %v", runErr)
		}
		return nil, errors.New("无法解析语音时长")
	}

	starts := ffSilStartRe.FindAllStringSubmatch(text, -1)
	ends := ffSilEndRe.FindAllStringSubmatch(text, -1)
	for i, sm := range starts {
		s, _ := strconv.ParseFloat(sm[1], 64)
		e := info.Duration // 末尾静音没有 silence_end
		if i < len(ends) {
			e, _ = strconv.ParseFloat(ends[i][1], 64)
		}
		if s < pauseEdgeGuard || e > info.Duration-pauseEdgeGuard || e <= s {
			continue // 掐头去尾
		}
		info.Pauses = append(info.Pauses, VoicePauseSpan{Start: s, End: e})
	}
	return info, nil
}
