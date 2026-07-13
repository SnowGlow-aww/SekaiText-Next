package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EngineManager drives the bundled SekaiCoreEngine — a headless .NET sidecar
// that speaks newline-delimited JSON (NDJSON) over stdio. It wraps the auto-timing
// (自动打轴, subtitle.*) and video-suppress (压制, suppress.*) handlers so the Go
// backend can run them as long jobs and surface progress to the frontend.
//
// Wire format (one JSON object per line):
//
//	request      (Go -> engine): {"id":<int>,"method":"<string>","params":<object|null>}
//	response     (engine -> Go): {"id":<int>,"result":<any|null>,"error":<string|null>}
//	notification (engine -> Go): {"method":"<string>","params":<object|null>}   (no "id")
//
// A line is a response iff it carries "id"; otherwise it is a notification.
//
// 并发模型：引擎进程内一次只有一个打轴 job（无 correlation id），所以隔离边界就是
// 进程——每个 job 独占一个引擎进程，通知天然归属所在进程绑定的 job。支持多视频
// 并行（parallel=true 时同域多 job 各占一个进程）；serial（默认）保持老语义：同域
// 单飞、新打轴替换全部旧任务。另留一个「备胎」空闲进程给 Ping 与下一个 job 领养，
// 避免每次起任务都冷启动 .NET。
type EngineManager struct {
	enginePath string
	ffmpegPath string
	logsDir    string // 压制日志自动导出目录（数据目录下 logs/）；空 = 不落盘

	mu            sync.Mutex // guards spare + job maps/orders + probeCache
	spare         *engineProc
	timingJobs    map[string]*EngineTimingJob
	timingOrder   []string
	suppressJobs  map[string]*EngineSuppressJob
	suppressOrder []string

	// suppress.probe 结果（可用编码器+推荐值）。首跑要对每个硬件编码器试编码
	// （数秒），显卡不会中途更换，成功结果缓存整个后端生命周期。
	probeCache json.RawMessage
}

// 并行上限：识别/压制本身就吃满多核，同域 4 个并行进程已经远超普通机器的合理负载；
// 保留的打轴任务数也设上限（每个 done 任务都占着一个引擎进程的内存）。
const (
	maxRunningPerDomain = 4
	maxKeptTimingJobs   = 8
	maxKeptSuppressJobs = 16

	// 每个压制任务保留的日志行数上限。进度行（frame=…）原地替换不占行数，
	// 溢出时保留头部参数信息 + 最新尾部（诊断需要头尾，中段最不重要）。
	maxSuppressLogLines  = 600
	suppressLogHeadLines = 8
)

type rawResponse struct {
	Result json.RawMessage
	Error  string
}

// Busy errors are returned when a serial-mode start is rejected because a run is
// already in progress; the HTTP layer maps these to 409 Conflict.
var (
	ErrTimingBusy   = errors.New("已有打轴任务在进行中（并行模式可同时跑多个）")
	ErrSuppressBusy = errors.New("已有压制任务在进行中（并行模式可同时跑多个）")
	// 两个 ffmpeg 同时写一个输出文件不会报错但产物必坏（Windows 共享写入），必须拦。
	ErrSuppressOutputConflict = errors.New("已有压制任务正在输出到同一个文件，请更换输出路径或等其完成")
)

// envelope is the union of response + notification fields; "id" presence selects.
type ipcEnvelope struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *string         `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// --- engineProc：一个已拉起的引擎进程，自带独立的 NDJSON 收发通道 ---

type engineProc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex // serializes writes so each JSON object is one atomic line
	nextID  int64      // atomic request-id counter
	pending sync.Map   // map[int64]chan rawResponse
	exited  chan struct{}

	mu       sync.Mutex // guards dead/notify/onExitCb
	dead     bool
	notify   func(method string, params json.RawMessage)
	onExitCb func()
}

func (em *EngineManager) spawnProc() (*engineProc, error) {
	if !em.Available() {
		return nil, fmt.Errorf("engine binary not found: %s", em.enginePath)
	}
	cmd := exec.Command(em.enginePath)
	HideConsoleWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("engine start: %w", err)
	}
	p := &engineProc{cmd: cmd, stdin: stdin, exited: make(chan struct{})}
	go p.readLoop(stdout)
	go drainStderr(stderr)
	go func() {
		_ = cmd.Wait()
		p.onExit()
	}()
	return p, nil
}

// readLoop parses one NDJSON object per line. Uses bufio.Reader.ReadString (which
// grows to any line length) rather than bufio.Scanner, whose 64KB token cap would
// be blown by a base64 preview jpeg.
func (p *engineProc) readLoop(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			p.dispatchLine(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("[engine] read error: %v", err)
			}
			return
		}
	}
}

func (p *engineProc) dispatchLine(line string) {
	var env ipcEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		// The engine does not log to stdout, so a non-JSON line is unexpected; skip it.
		return
	}
	if env.ID != nil {
		// Response: deliver to the waiting request.
		if ch, ok := p.pending.LoadAndDelete(*env.ID); ok {
			errStr := ""
			if env.Error != nil {
				errStr = *env.Error
			}
			ch.(chan rawResponse) <- rawResponse{Result: env.Result, Error: errStr}
		}
		return
	}
	if env.Method != "" {
		p.mu.Lock()
		fn := p.notify
		p.mu.Unlock()
		if fn != nil {
			fn(env.Method, env.Params)
		}
	}
}

// onExit fails pending requests and tells the bound job (if any) its engine died.
func (p *engineProc) onExit() {
	p.mu.Lock()
	p.dead = true
	cb := p.onExitCb
	p.onExitCb = nil
	p.notify = nil
	p.mu.Unlock()
	close(p.exited)
	p.pending.Range(func(k, v interface{}) bool {
		p.pending.Delete(k)
		select {
		case v.(chan rawResponse) <- rawResponse{Error: "engine exited"}:
		default:
		}
		return true
	})
	if cb != nil {
		cb()
	}
}

func (p *engineProc) isDead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dead
}

// bind attaches the job's notification router and death callback. Returns false
// if the proc already died (caller must not use it).
func (p *engineProc) bind(notify func(string, json.RawMessage), onExit func()) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return false
	}
	p.notify = notify
	p.onExitCb = onExit
	return true
}

// kill asks the engine to exit (stdin EOF) and force-kills it shortly after if
// it doesn't. Detaches callbacks first so the teardown doesn't fail a job that
// has already been handed a different proc (or none).
func (p *engineProc) kill() {
	p.mu.Lock()
	p.notify = nil
	p.onExitCb = nil
	p.mu.Unlock()
	_ = p.stdin.Close()
	go func() {
		select {
		case <-p.exited:
		case <-time.After(3 * time.Second):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
		}
	}()
}

// request sends a method call and waits for the matching response.
func (p *engineProc) request(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if p.isDead() {
		return nil, errors.New("内核进程已退出")
	}
	id := atomic.AddInt64(&p.nextID, 1)
	ch := make(chan rawResponse, 1)
	p.pending.Store(id, ch)

	payload := map[string]interface{}{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		p.pending.Delete(id)
		return nil, err
	}

	p.writeMu.Lock()
	_, werr := p.stdin.Write(append(buf, '\n'))
	p.writeMu.Unlock()
	if werr != nil {
		p.pending.Delete(id)
		return nil, fmt.Errorf("engine write: %w", werr)
	}

	select {
	case <-ctx.Done():
		p.pending.Delete(id)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return resp.Result, nil
	}
}

// --- jobs ---

// EngineTimingJob mirrors ProgressTracker for an auto-timing run. Mu guards the
// mutable fields, written by the notification-router goroutine and read by the
// progress HTTP handler.
type EngineTimingJob struct {
	Mu           sync.Mutex
	TaskID       string
	ScriptPath   string // scenario JSON path; used to name the exported .ass
	VideoPath    string
	Status       string // running, done, error, canceled
	Percent      float64
	Fps          int
	Eta          string
	DialogTotal  int
	BannerTotal  int
	MarkerTotal  int
	Matched       int    // dialogs+banners+markers finalized so far (合计, 向后兼容)
	MatchedDialog int    // 已匹配对话数
	MatchedBanner int    // 已匹配 banner 数
	MatchedMarker int    // 已匹配 marker 数
	PreviewB64    string // latest preview jpeg (served on a separate endpoint)
	FinishReason string
	Error        string

	// --- 导出与 Aegisub 同步状态（由 HTTP 层维护，同样由 Mu 保护） ---
	ExportAssPath string         // 最近一次导出的 .ass 绝对路径（空=未导出）
	ExportOpts    AssPostOptions // 导出时的后处理选项，推送同步时复用
	ExportMTime   time.Time      // 我们最后一次写盘后的 mtime（据此判定 Aegisub 侧是否改过）
	ExportSize    int64
	DirtyLines    map[int]bool // 自上次导出/推送后经 broker 编辑过的 dialog index

	proc *engineProc // 该任务独占的引擎进程；nil = 尚未领养或已回收/死亡
}

// EngineSuppressJob mirrors ProgressTracker for a 压制 (encode) run.
type EngineSuppressJob struct {
	Mu           sync.Mutex
	TaskID       string
	SourceVideo  string
	Status       string // running, done, error, canceled
	Frame        int
	Total        int
	Fps          float64
	OutputPath   string
	LastLog      string
	FinishReason string
	Error        string

	// 滚动日志（ffmpeg stderr + 引擎 [Sekai]/[VSPipe] 行）。报错时自动导出到
	// LogPath；也可通过 /engine/suppress/log 随时查看/手动导出。
	LogLines        []string
	LogPath         string
	logLastProgress bool

	proc *engineProc
}

// NewEngineManager constructs a manager. No engine process is spawned until the
// first job/ping; callers should gate features on Available().
func NewEngineManager(enginePath, ffmpegPath, logsDir string) *EngineManager {
	return &EngineManager{
		enginePath:   enginePath,
		ffmpegPath:   ffmpegPath,
		logsDir:      logsDir,
		timingJobs:   map[string]*EngineTimingJob{},
		suppressJobs: map[string]*EngineSuppressJob{},
	}
}

// Available reports whether the engine binary is present on disk.
func (em *EngineManager) Available() bool {
	if em.enginePath == "" {
		return false
	}
	info, err := os.Stat(em.enginePath)
	return err == nil && !info.IsDir()
}

// FfmpegPath exposes the configured ffmpeg used for 压制.
func (em *EngineManager) FfmpegPath() string { return em.ffmpegPath }

// takeProc hands out the spare proc (warm start) or spawns a fresh one.
func (em *EngineManager) takeProc() (*engineProc, error) {
	em.mu.Lock()
	p := em.spare
	em.spare = nil
	em.mu.Unlock()
	if p != nil && !p.isDead() {
		return p, nil
	}
	return em.spawnProc()
}

// recycleProc parks a clean idle proc as the spare for the next job/ping, or
// kills it when a spare is already parked.
func (em *EngineManager) recycleProc(p *engineProc) {
	if p == nil {
		return
	}
	p.mu.Lock()
	dead := p.dead
	p.notify = nil
	p.onExitCb = nil
	p.mu.Unlock()
	if dead {
		return
	}
	em.mu.Lock()
	if em.spare == nil || em.spare.isDead() {
		em.spare = p
		em.mu.Unlock()
		return
	}
	em.mu.Unlock()
	p.kill()
}

// spareProc returns a live spare proc for job-less calls (Ping), spawning and
// parking one if needed.
func (em *EngineManager) spareProc() (*engineProc, error) {
	em.mu.Lock()
	if em.spare != nil && !em.spare.isDead() {
		p := em.spare
		em.mu.Unlock()
		return p, nil
	}
	em.mu.Unlock()
	p, err := em.spawnProc()
	if err != nil {
		return nil, err
	}
	em.mu.Lock()
	if em.spare == nil || em.spare.isDead() {
		em.spare = p
		em.mu.Unlock()
		return p, nil
	}
	// raced with a recycle; keep the parked one, drop ours
	old := em.spare
	em.mu.Unlock()
	p.kill()
	return old, nil
}

func (j *EngineTimingJob) statusSnapshot() string {
	j.Mu.Lock()
	defer j.Mu.Unlock()
	return j.Status
}

func (j *EngineSuppressJob) statusSnapshot() string {
	j.Mu.Lock()
	defer j.Mu.Unlock()
	return j.Status
}

// timingProc resolves the engine proc bound to a job for line/export calls.
func timingProc(job *EngineTimingJob) (*engineProc, error) {
	job.Mu.Lock()
	p := job.proc
	job.Mu.Unlock()
	if p == nil || p.isDead() {
		return nil, errors.New("该任务的内核进程已退出，请重新打轴")
	}
	return p, nil
}

// --- Auto-timing (自动打轴) ---

// TimingParams is the start payload; ScriptPath is a scenario JSON, TranslatePath
// an optional engine-format .txt of translated lines.
type TimingParams struct {
	VideoPath     string      `json:"videoPath"`
	ScriptPath    string      `json:"scriptPath"`
	TranslatePath string      `json:"translatePath,omitempty"`
	Threshold     interface{} `json:"threshold,omitempty"`
}

// StartTiming launches an auto-timing run in its own engine process and registers
// the job. serial（parallel=false，老语义）：已有 running 即拒绝，且启动时替换掉全部
// 旧任务；parallel：多任务并存，受 maxRunningPerDomain/maxKeptTimingJobs 约束。
func (em *EngineManager) StartTiming(taskID string, p TimingParams, parallel bool) (*EngineTimingJob, error) {
	job := &EngineTimingJob{TaskID: taskID, ScriptPath: p.ScriptPath, VideoPath: p.VideoPath, Status: "running"}

	em.mu.Lock()
	running := 0
	for _, j := range em.timingJobs {
		if j.statusSnapshot() == "running" {
			running++
		}
	}
	if running > 0 && !parallel {
		em.mu.Unlock()
		return nil, ErrTimingBusy
	}
	if parallel && running >= maxRunningPerDomain {
		em.mu.Unlock()
		return nil, fmt.Errorf("并行打轴数已达上限（%d），请等待或取消其他任务", maxRunningPerDomain)
	}
	if parallel && len(em.timingJobs) >= maxKeptTimingJobs {
		em.mu.Unlock()
		return nil, fmt.Errorf("保留的打轴任务过多（上限 %d），请先关闭已完成的任务", maxKeptTimingJobs)
	}
	var replaced []*engineProc
	if !parallel {
		// 老语义：新一轮打轴替换全部旧任务；其进程回收一个当备胎、其余杀掉。
		for id, j := range em.timingJobs {
			j.Mu.Lock()
			pr := j.proc
			j.proc = nil
			j.Mu.Unlock()
			if pr != nil {
				replaced = append(replaced, pr)
			}
			delete(em.timingJobs, id)
		}
		em.timingOrder = nil
	}
	em.timingJobs[taskID] = job
	em.timingOrder = append(em.timingOrder, taskID)
	em.mu.Unlock()
	for _, pr := range replaced {
		em.recycleProc(pr)
	}

	// Fire subtitle.start asynchronously and return the taskId now. The engine
	// awaits EnsureResource (first-run download of VideoProcess templates/fonts)
	// before acking, which on a fresh machine with a slow network can take minutes;
	// blocking the HTTP start that long would freeze the UI and, worse, leave it
	// unable to cancel. A start failure instead surfaces through the job's terminal
	// state, which the progress poll reads.
	go em.launchTiming(job, p)
	return job, nil
}

func (em *EngineManager) launchTiming(job *EngineTimingJob, p TimingParams) {
	// A cancel issued before the proc was acquired marks the job canceled; don't
	// spawn an engine for a run the user already canceled.
	if job.statusSnapshot() != "running" {
		return
	}
	proc, err := em.takeProc()
	if err != nil {
		em.failStart(&job.Mu, &job.Status, &job.Error, "启动打轴失败: "+err.Error())
		return
	}
	if !proc.bind(
		func(method string, params json.RawMessage) { routeTimingNotification(job, method, params) },
		func() { failJobExit(&job.Mu, &job.Status, &job.Error, &job.FinishReason) },
	) {
		em.failStart(&job.Mu, &job.Status, &job.Error, "内核进程启动后立即退出")
		return
	}
	job.Mu.Lock()
	if job.Status != "running" { // canceled while we were spawning
		job.Mu.Unlock()
		em.recycleProc(proc)
		return
	}
	job.proc = proc
	job.Mu.Unlock()

	// The engine only acks subtitle.start after EnsureResource, which on a fresh
	// machine can take many minutes; the timeout is a generous backstop, not the
	// job's deadline (on deadline the engine keeps driving the job to a terminal
	// state through its notifications or the proc's exit callback).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := proc.request(ctx, "subtitle.start", p); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		em.failStart(&job.Mu, &job.Status, &job.Error, "启动打轴失败: "+err.Error())
		job.Mu.Lock()
		pr := job.proc
		job.proc = nil
		job.Mu.Unlock()
		em.recycleProc(pr)
		return
	}
	// A cancel that raced the start send has already marked the job canceled but
	// had no proc to stop — make sure the engine actually stops now.
	if job.statusSnapshot() == "canceled" {
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = proc.request(sctx, "subtitle.stop", nil)
		scancel()
	}
}

// failStart marks a still-running job as failed at start time. The job stays
// registered so the progress poll surfaces the error (serial-mode starts only
// count running jobs, so a failed job never blocks a retry).
func (em *EngineManager) failStart(mu *sync.Mutex, status, errMsg *string, msg string) {
	mu.Lock()
	if *status == "running" {
		*status = "error"
		*errMsg = msg
	}
	mu.Unlock()
}

// failJobExit is the proc-death callback: a still-running job will never get its
// finished notification once the engine dies, so fail it explicitly.
func failJobExit(mu *sync.Mutex, status, errMsg, reason *string) {
	mu.Lock()
	if *status == "running" {
		*status = "error"
		*errMsg = "内核进程已退出"
		*reason = "EngineExited"
	}
	mu.Unlock()
}

// Export pulls the assembled ASS subtitle from a timing job's engine.
func (em *EngineManager) Export(job *EngineTimingJob) (string, error) {
	proc, err := timingProc(job)
	if err != nil {
		return "", err
	}
	// Subtitle assembly from already-collected frame sets can take a while on a long
	// video / large dialog set; a tight 30s would spuriously fail an otherwise-fine export.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := proc.request(ctx, "subtitle.export", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	return out.Content, nil
}

// --- 行列表 / 分句编辑（原样代理给引擎） ---

// TimingLines 返回引擎侧的完整识别行列表（subtitle.lines 的原始 payload，
// 已经是前端要的形状，broker 不加工）。
func (em *EngineManager) TimingLines(job *EngineTimingJob) (json.RawMessage, error) {
	proc, err := timingProc(job)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return proc.request(ctx, "subtitle.lines", nil)
}

// TimingLineCall 代理单行编辑类方法（subtitle.setSeparator / subtitle.setTranslation /
// subtitle.estimateSeparator），params 原样透传。
func (em *EngineManager) TimingLineCall(job *EngineTimingJob, method string, params interface{}) (json.RawMessage, error) {
	proc, err := timingProc(job)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return proc.request(ctx, method, params)
}

// TimingFrame 取指定帧的画面（base64 jpeg），给分隔帧微调的实时预览用。
func (em *EngineManager) TimingFrame(job *EngineTimingJob, frame, maxWidth int) (json.RawMessage, error) {
	proc, err := timingProc(job)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	params := map[string]int{"frame": frame}
	if maxWidth > 0 {
		params["maxWidth"] = maxWidth
	}
	return proc.request(ctx, "subtitle.frame", params)
}

// CloseTiming 关闭并移除一个打轴任务：done/error/canceled 的空闲进程回收当备胎，
// 运行中的直接杀掉（等同取消）。
func (em *EngineManager) CloseTiming(taskID string) error {
	em.mu.Lock()
	job, ok := em.timingJobs[taskID]
	if !ok {
		em.mu.Unlock()
		return fmt.Errorf("task not found")
	}
	delete(em.timingJobs, taskID)
	em.timingOrder = removeID(em.timingOrder, taskID)
	em.mu.Unlock()

	job.Mu.Lock()
	pr := job.proc
	job.proc = nil
	running := job.Status == "running"
	if running {
		job.Status = "canceled"
		job.FinishReason = "Canceled"
	}
	job.Mu.Unlock()
	if pr != nil {
		if running {
			pr.kill()
		} else {
			em.recycleProc(pr)
		}
	}
	return nil
}

func removeID(order []string, id string) []string {
	out := order[:0]
	for _, v := range order {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// --- Suppress (压制) ---

// SuppressParams is the start payload. Encoder is a VideoEncoder enum name
// (e.g. "HevcVideoToolbox"); FfmpegPath is injected by the handler from config.
type SuppressParams struct {
	SourceVideo      string `json:"sourceVideo"`
	OutputPath       string `json:"outputPath"`
	SourceSubtitle   string `json:"sourceSubtitle,omitempty"`
	Crf              int    `json:"crf,omitempty"`
	Encoder          string `json:"encoder,omitempty"`
	UseHwAccelDecode *bool  `json:"useHwAccelDecode,omitempty"`
	FfmpegPath       string `json:"ffmpegPath,omitempty"`
}

// StartSuppress launches an encode run in its own engine process.
func (em *EngineManager) StartSuppress(taskID string, p SuppressParams, parallel bool) (*EngineSuppressJob, error) {
	if p.FfmpegPath == "" {
		p.FfmpegPath = em.ffmpegPath
	}
	job := &EngineSuppressJob{TaskID: taskID, Status: "running", OutputPath: p.OutputPath, SourceVideo: p.SourceVideo}
	job.LogLines = []string{
		"[SekaiText 压制日志] 任务 " + taskID + " · " + time.Now().Format("2006-01-02 15:04:05"),
		"源视频: " + p.SourceVideo,
		"字幕:   " + p.SourceSubtitle,
		"输出:   " + p.OutputPath,
		fmt.Sprintf("编码器: %s · CRF %d · 硬解 %v · 并行 %v", p.Encoder, p.Crf,
			p.UseHwAccelDecode == nil || *p.UseHwAccelDecode, parallel),
		"----------------------------------------",
	}

	em.mu.Lock()
	running := 0
	for _, j := range em.suppressJobs {
		if j.statusSnapshot() != "running" {
			continue
		}
		running++
		// 并行任务各自独占引擎进程，但输出文件仍是共享资源：两个 ffmpeg 写同一个
		// 文件在 Windows 上不报错、产物直接损坏，启动前必须拦下。
		if filepath.Clean(j.OutputPath) == filepath.Clean(p.OutputPath) {
			em.mu.Unlock()
			return nil, ErrSuppressOutputConflict
		}
	}
	if running > 0 && !parallel {
		em.mu.Unlock()
		return nil, ErrSuppressBusy
	}
	if parallel && running >= maxRunningPerDomain {
		em.mu.Unlock()
		return nil, fmt.Errorf("并行压制数已达上限（%d），请等待或取消其他任务", maxRunningPerDomain)
	}
	// 压制任务终态后进程即回收，job 结构只是进度快照——修剪最旧的终态任务防无界增长。
	for len(em.suppressJobs) >= maxKeptSuppressJobs && len(em.suppressOrder) > 0 {
		oldest := ""
		for _, id := range em.suppressOrder {
			if j := em.suppressJobs[id]; j != nil && j.statusSnapshot() != "running" {
				oldest = id
				break
			}
		}
		if oldest == "" {
			break
		}
		pruned := em.suppressJobs[oldest]
		delete(em.suppressJobs, oldest)
		em.suppressOrder = removeID(em.suppressOrder, oldest)
		if pruned != nil {
			// 启动即失败的任务可能还挂着活进程（正常终态的早已回收）——别泄漏。
			go em.releaseSuppressProc(pruned)
		}
	}
	em.suppressJobs[taskID] = job
	em.suppressOrder = append(em.suppressOrder, taskID)
	em.mu.Unlock()

	// Async start (see StartTiming): return the taskId immediately so the UI stays
	// responsive and cancelable; a start failure surfaces via the job's terminal state.
	go em.launchSuppress(job, p)
	return job, nil
}

func (em *EngineManager) launchSuppress(job *EngineSuppressJob, p SuppressParams) {
	if job.statusSnapshot() != "running" {
		return
	}
	proc, err := em.takeProc()
	if err != nil {
		em.failStart(&job.Mu, &job.Status, &job.Error, "启动压制失败: "+err.Error())
		return
	}
	if !proc.bind(
		func(method string, params json.RawMessage) { em.routeSuppressNotification(job, method, params) },
		func() {
			failJobExit(&job.Mu, &job.Status, &job.Error, &job.FinishReason)
			// 引擎进程异常死亡拿不到 suppress.finished——把死亡记录进日志并导出，
			// 否则最需要日志的崩溃场景反而什么都留不下。
			if job.statusSnapshot() == "error" {
				job.Mu.Lock()
				job.appendLogLocked("[SekaiText] 内核进程异常退出（未收到压制结束通知）", false)
				job.Mu.Unlock()
				go func() {
					if _, err := em.ExportSuppressLog(job); err != nil {
						log.Printf("[engine] 压制日志自动导出失败: %v", err)
					}
				}()
			}
		},
	) {
		em.failStart(&job.Mu, &job.Status, &job.Error, "内核进程启动后立即退出")
		return
	}
	job.Mu.Lock()
	if job.Status != "running" {
		job.Mu.Unlock()
		em.recycleProc(proc)
		return
	}
	job.proc = proc
	job.Mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := proc.request(ctx, "suppress.start", p); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		em.failStart(&job.Mu, &job.Status, &job.Error, "启动压制失败: "+err.Error())
		em.releaseSuppressProc(job)
		return
	}
	if job.statusSnapshot() == "canceled" {
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = proc.request(sctx, "suppress.stop", nil)
		scancel()
	}
}

// releaseSuppressProc 在压制到达终态后回收其进程（压制完成后进程再无用处，
// 不像打轴还要伺候行编辑/导出）。
func (em *EngineManager) releaseSuppressProc(job *EngineSuppressJob) {
	job.Mu.Lock()
	pr := job.proc
	job.proc = nil
	job.Mu.Unlock()
	em.recycleProc(pr)
}

// --- Control ---

// Cancel stops a run. taskID 为空时取消该域当前唯一 running 任务（老插件兼容）。
func (em *EngineManager) Cancel(domain, taskID string) error {
	switch domain {
	case "timing":
		job := em.pickTimingForCancel(taskID)
		if job == nil {
			return nil // nothing to cancel
		}
		job.Mu.Lock()
		proc := job.proc
		running := job.Status == "running"
		if running && proc == nil {
			// start goroutine 还没领养到进程：直接标记取消，launchTiming 会自行退出
			job.Status = "canceled"
			job.FinishReason = "Canceled"
		}
		job.Mu.Unlock()
		if !running || proc == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := proc.request(ctx, "subtitle.stop", nil)
		return err
	case "suppress":
		job := em.pickSuppressForCancel(taskID)
		if job == nil {
			return nil
		}
		job.Mu.Lock()
		proc := job.proc
		running := job.Status == "running"
		if running && proc == nil {
			job.Status = "canceled"
			job.FinishReason = "Canceled"
		}
		job.Mu.Unlock()
		if !running || proc == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := proc.request(ctx, "suppress.stop", nil)
		return err
	default:
		return fmt.Errorf("unknown domain: %s", domain)
	}
}

func (em *EngineManager) pickTimingForCancel(taskID string) *EngineTimingJob {
	em.mu.Lock()
	defer em.mu.Unlock()
	if taskID != "" {
		return em.timingJobs[taskID]
	}
	for _, j := range em.timingJobs {
		if j.statusSnapshot() == "running" {
			return j
		}
	}
	return nil
}

func (em *EngineManager) pickSuppressForCancel(taskID string) *EngineSuppressJob {
	em.mu.Lock()
	defer em.mu.Unlock()
	if taskID != "" {
		return em.suppressJobs[taskID]
	}
	for _, j := range em.suppressJobs {
		if j.statusSnapshot() == "running" {
			return j
		}
	}
	return nil
}

// Ping returns the engine's readiness/version handshake (served by the spare proc).
// SuppressProbe asks the engine for the suppress runtime plus the list of
// hardware-verified encoders and the per-platform recommended default. 老引擎
// (≤2.0.0) 的响应没有 encoders/recommended 字段——原样透传，由前端兜底。
func (em *EngineManager) SuppressProbe() (json.RawMessage, error) {
	em.mu.Lock()
	cached := em.probeCache
	em.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	// 探测独占一个进程（takeProc 而非 spareProc）：引擎 dispatcher 是串行的，首跑
	// 试编码要几秒到几十秒，挂在备胎上会把同期领养该备胎的压制任务的 suppress.start
	// 和 Ping 全排在探测后面——表现为"内核未就绪"闪烁、压制迟迟不动。
	proc, err := em.takeProc()
	if err != nil {
		return nil, err
	}
	// 首跑要给每个编进 ffmpeg 的硬件编码器各跑一次试编码（坏驱动挂死的单项在
	// 引擎侧 20s 超时剔除；同家族串行、跨家族并发），给足余量。
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := proc.request(ctx, "suppress.probe", map[string]string{"ffmpegPath": em.ffmpegPath})
	if err != nil {
		// 超时/失败的进程内部可能还在试编码，回收当备胎会污染下一个任务——直接杀。
		proc.kill()
		return nil, err
	}
	em.recycleProc(proc)

	em.mu.Lock()
	em.probeCache = res
	em.mu.Unlock()
	return res, nil
}

func (em *EngineManager) Ping() (map[string]interface{}, error) {
	proc, err := em.spareProc()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := proc.request(ctx, "system.version", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	_ = json.Unmarshal(res, &out)
	return out, nil
}

func (em *EngineManager) TimingJob(taskID string) (*EngineTimingJob, bool) {
	em.mu.Lock()
	defer em.mu.Unlock()
	j, ok := em.timingJobs[taskID]
	return j, ok
}

func (em *EngineManager) SuppressJob(taskID string) (*EngineSuppressJob, bool) {
	em.mu.Lock()
	defer em.mu.Unlock()
	j, ok := em.suppressJobs[taskID]
	return j, ok
}

// EngineTaskSnapshot 是 /engine/tasks 的一行：插件重挂载后据此找回全部任务。
type EngineTaskSnapshot struct {
	TaskID        string  `json:"taskId"`
	Status        string  `json:"status"`
	Percent       float64 `json:"percent"`
	Error         string  `json:"error,omitempty"`
	VideoPath     string  `json:"videoPath,omitempty"`
	ScriptPath    string  `json:"scriptPath,omitempty"`
	ExportAssPath string  `json:"exportAssPath,omitempty"`
	MatchedDialog int     `json:"matchedDialog,omitempty"`
	DialogTotal   int     `json:"dialogTotal,omitempty"`
	SourceVideo   string  `json:"sourceVideo,omitempty"`
	OutputPath    string  `json:"outputPath,omitempty"`
}

// Tasks snapshots every registered job in start order (timing, suppress).
func (em *EngineManager) Tasks() ([]EngineTaskSnapshot, []EngineTaskSnapshot) {
	em.mu.Lock()
	tOrder := append([]string(nil), em.timingOrder...)
	sOrder := append([]string(nil), em.suppressOrder...)
	tJobs := make([]*EngineTimingJob, 0, len(tOrder))
	for _, id := range tOrder {
		if j := em.timingJobs[id]; j != nil {
			tJobs = append(tJobs, j)
		}
	}
	sJobs := make([]*EngineSuppressJob, 0, len(sOrder))
	for _, id := range sOrder {
		if j := em.suppressJobs[id]; j != nil {
			sJobs = append(sJobs, j)
		}
	}
	em.mu.Unlock()

	timing := make([]EngineTaskSnapshot, 0, len(tJobs))
	for _, j := range tJobs {
		j.Mu.Lock()
		timing = append(timing, EngineTaskSnapshot{
			TaskID:        j.TaskID,
			Status:        j.Status,
			Percent:       j.Percent,
			Error:         j.Error,
			VideoPath:     j.VideoPath,
			ScriptPath:    j.ScriptPath,
			ExportAssPath: j.ExportAssPath,
			MatchedDialog: j.MatchedDialog,
			DialogTotal:   j.DialogTotal,
		})
		j.Mu.Unlock()
	}
	suppress := make([]EngineTaskSnapshot, 0, len(sJobs))
	for _, j := range sJobs {
		j.Mu.Lock()
		pct := 0.0
		if j.Total > 0 {
			pct = float64(j.Frame) / float64(j.Total) * 100
			if pct > 100 {
				pct = 100
			}
		}
		suppress = append(suppress, EngineTaskSnapshot{
			TaskID:      j.TaskID,
			Status:      j.Status,
			Percent:     pct,
			Error:       j.Error,
			SourceVideo: j.SourceVideo,
			OutputPath:  j.OutputPath,
		})
		j.Mu.Unlock()
	}
	return timing, suppress
}

// routeTimingNotification updates the bound job's progress state from an engine event.
func routeTimingNotification(j *EngineTimingJob, method string, params json.RawMessage) {
	switch method {
	case "subtitle.started":
		var p struct{ DialogTotal, BannerTotal, MarkerTotal int }
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.DialogTotal, j.BannerTotal, j.MarkerTotal = p.DialogTotal, p.BannerTotal, p.MarkerTotal
		j.Mu.Unlock()
	case "subtitle.progress":
		var p struct{ Percent float64 }
		_ = json.Unmarshal(params, &p)
		// Engine reports percent as a 0..1 fraction; the frontend renders
		// 0..100, so scale here to match the 压制 progress (already 0..100).
		pct := p.Percent * 100
		if pct < 0 {
			pct = 0
		} else if pct > 100 {
			pct = 100
		}
		j.Mu.Lock()
		j.Percent = pct
		j.Mu.Unlock()
	case "subtitle.fps":
		var p struct {
			Fps int
			Eta string
		}
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.Fps, j.Eta = p.Fps, p.Eta
		j.Mu.Unlock()
	case "subtitle.preview":
		var p struct{ Base64 string }
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.PreviewB64 = p.Base64
		j.Mu.Unlock()
	case "subtitle.dialog", "subtitle.banner", "subtitle.marker":
		j.Mu.Lock()
		j.Matched++
		switch method {
		case "subtitle.dialog":
			j.MatchedDialog++
		case "subtitle.banner":
			j.MatchedBanner++
		case "subtitle.marker":
			j.MatchedMarker++
		}
		j.Mu.Unlock()
	case "subtitle.finished":
		var p struct{ Reason string }
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.FinishReason = p.Reason
		// ReadFailed means the engine ran to the end of the video (read past
		// EOF) — a normal successful finish, same as Completed. See native app
		// SekaiToolsApp/Views/Pages/SubtitlePageView.cs:513-518.
		if p.Reason == "Completed" || p.Reason == "ReadFailed" {
			j.Status = "done"
			j.Percent = 100
			// A transient per-frame error (below ExceptionThreshold) can emit
			// subtitle.error mid-run while the run still completes; clear that
			// stale message so the terminal state isn't a done+error contradiction.
			j.Error = ""
		} else if p.Reason == "Canceled" {
			j.Status = "canceled"
			// A transient per-frame subtitle.error may have set j.Error mid-run;
			// a user cancel is not a failure, so clear it lest the progress
			// endpoint surface a spurious error reason for a normal cancel.
			j.Error = ""
		} else {
			j.Status = "error"
			if j.Error == "" {
				j.Error = "打轴未正常完成: " + p.Reason
			}
		}
		// The last preview frame is a multi-MB base64 jpeg and useless once the
		// run is terminal — drop it so it doesn't sit resident until the next run.
		j.PreviewB64 = ""
		j.Mu.Unlock()
	case "subtitle.error":
		var p struct{ Message string }
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.Error = p.Message
		j.Mu.Unlock()
	}
}

func (em *EngineManager) routeSuppressNotification(j *EngineSuppressJob, method string, params json.RawMessage) {
	switch method {
	case "suppress.progress":
		var p struct {
			Frame int
			Total int
			Fps   float64
		}
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.Frame, j.Total, j.Fps = p.Frame, p.Total, p.Fps
		j.Mu.Unlock()
	case "suppress.log", "suppress.progressLog":
		var p struct{ Line string }
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.LastLog = p.Line
		j.appendLogLocked(p.Line, method == "suppress.progressLog")
		j.Mu.Unlock()
	case "suppress.finished":
		var p struct {
			Reason string
			Error  string
		}
		_ = json.Unmarshal(params, &p)
		j.Mu.Lock()
		j.FinishReason = p.Reason
		if p.Reason == "Completed" {
			j.Status = "done"
			j.appendLogLocked("[SekaiText] 压制完成", false)
		} else if p.Reason == "Canceled" {
			j.Status = "canceled"
			j.appendLogLocked("[SekaiText] 已取消", false)
		} else {
			j.Status = "error"
			j.Error = p.Error
			j.appendLogLocked("[SekaiText] 压制失败（"+p.Reason+"）: "+p.Error, false)
		}
		failed := j.Status == "error"
		j.Mu.Unlock()
		// 报错自动导出日志：用户不用在错误发生后到处找现场，直接把文件发给开发者。
		if failed {
			go func() {
				if _, err := em.ExportSuppressLog(j); err != nil {
					log.Printf("[engine] 压制日志自动导出失败: %v", err)
				}
			}()
		}
		// 终态即回收进程；在通知 goroutine 里做，异步避免与读循环互等。
		go em.releaseSuppressProc(j)
	}
}

// appendLogLocked 追加一行滚动日志（调用方必须已持有 j.Mu）。连续的 ffmpeg 进度行
// （suppress.progressLog）原地替换，不让 frame=… 刷屏占满缓冲。
func (j *EngineSuppressJob) appendLogLocked(line string, progress bool) {
	if progress && j.logLastProgress && len(j.LogLines) > 0 {
		j.LogLines[len(j.LogLines)-1] = line
	} else {
		j.LogLines = append(j.LogLines, line)
		if len(j.LogLines) > maxSuppressLogLines {
			// 头部是任务参数（诊断必需），保住；砍掉最旧的正文行。
			head := suppressLogHeadLines
			if head > len(j.LogLines) {
				head = len(j.LogLines)
			}
			keepTail := maxSuppressLogLines - head
			trimmed := make([]string, 0, maxSuppressLogLines+1)
			trimmed = append(trimmed, j.LogLines[:head]...)
			trimmed = append(trimmed, "…（中段日志已截断）…")
			trimmed = append(trimmed, j.LogLines[len(j.LogLines)-keepTail:]...)
			j.LogLines = trimmed
		}
	}
	j.logLastProgress = progress
}

// ExportSuppressLog 把任务日志写到数据目录 logs/ 下（路径按任务固定，重复导出覆盖
// 刷新内容）。报错时自动调用；插件的「导出日志」按钮也走这里。
func (em *EngineManager) ExportSuppressLog(j *EngineSuppressJob) (string, error) {
	if em.logsDir == "" {
		return "", errors.New("日志目录未配置")
	}
	if err := os.MkdirAll(em.logsDir, 0755); err != nil {
		return "", err
	}
	j.Mu.Lock()
	path := j.LogPath
	if path == "" {
		path = filepath.Join(em.logsDir, "suppress-"+time.Now().Format("20060102-150405")+"-"+j.TaskID+".log")
		j.LogPath = path
	}
	content := strings.Join(j.LogLines, "\n") + "\n"
	j.Mu.Unlock()

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		j.Mu.Lock()
		j.LogPath = ""
		j.Mu.Unlock()
		return "", err
	}
	return path, nil
}

func drainStderr(stderr io.Reader) {
	r := bufio.NewReader(stderr)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			log.Printf("[engine:stderr] %s", line)
		}
		if err != nil {
			return
		}
	}
}
