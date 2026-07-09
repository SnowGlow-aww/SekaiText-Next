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
// The engine is single-job-per-domain (one timing + one suppress at a time, no
// correlation ids on notifications), so the manager keeps one active job pointer
// per domain and routes notifications by method prefix.
type EngineManager struct {
	enginePath string
	ffmpegPath string

	mu      sync.Mutex // guards cmd/stdin/started + the active job pointers
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	started bool

	writeMu sync.Mutex // serializes writes so each JSON object is one atomic line
	nextID  int64      // atomic request-id counter
	pending sync.Map   // map[int64]chan rawResponse

	timingJob   *EngineTimingJob
	suppressJob *EngineSuppressJob
}

type rawResponse struct {
	Result json.RawMessage
	Error  string
}

// Busy errors are returned when a single-job-per-domain start is rejected because
// a run is already in progress; the HTTP layer maps these to 409 Conflict.
var (
	ErrTimingBusy   = errors.New("已有打轴任务在进行中")
	ErrSuppressBusy = errors.New("已有压制任务在进行中")
)

// envelope is the union of response + notification fields; "id" presence selects.
type ipcEnvelope struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *string         `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// EngineTimingJob mirrors ProgressTracker for an auto-timing run. Mu guards the
// mutable fields, written by the notification-router goroutine and read by the
// progress HTTP handler.
type EngineTimingJob struct {
	Mu           sync.Mutex
	TaskID       string
	ScriptPath   string // scenario JSON path; used to name the exported .ass
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
}

// EngineSuppressJob mirrors ProgressTracker for a 压制 (encode) run.
type EngineSuppressJob struct {
	Mu           sync.Mutex
	TaskID       string
	Status       string // running, done, error, canceled
	Frame        int
	Total        int
	Fps          float64
	OutputPath   string
	LastLog      string
	FinishReason string
	Error        string
}

// NewEngineManager constructs a manager. The engine is NOT spawned until the
// first job; callers should gate features on Available().
func NewEngineManager(enginePath, ffmpegPath string) *EngineManager {
	return &EngineManager{enginePath: enginePath, ffmpegPath: ffmpegPath}
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

// ensureStarted spawns the engine process lazily and starts the reader goroutine.
func (em *EngineManager) ensureStarted() error {
	em.mu.Lock()
	defer em.mu.Unlock()
	if em.started {
		return nil
	}
	if !em.Available() {
		return fmt.Errorf("engine binary not found: %s", em.enginePath)
	}

	cmd := exec.Command(em.enginePath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("engine stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("engine stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("engine stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("engine start: %w", err)
	}

	em.cmd = cmd
	em.stdin = stdin
	em.started = true

	go em.readLoop(stdout)
	go drainStderr(stderr)
	go func() {
		_ = cmd.Wait()
		em.onExit()
	}()

	return nil
}

// readLoop parses one NDJSON object per line. Uses bufio.Reader.ReadString (which
// grows to any line length) rather than bufio.Scanner, whose 64KB token cap would
// be blown by a base64 preview jpeg.
func (em *EngineManager) readLoop(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			em.dispatchLine(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("[engine] read error: %v", err)
			}
			return
		}
	}
}

func (em *EngineManager) dispatchLine(line string) {
	var env ipcEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		// The engine does not log to stdout, so a non-JSON line is unexpected; skip it.
		return
	}
	if env.ID != nil {
		// Response: deliver to the waiting request.
		if ch, ok := em.pending.LoadAndDelete(*env.ID); ok {
			errStr := ""
			if env.Error != nil {
				errStr = *env.Error
			}
			ch.(chan rawResponse) <- rawResponse{Result: env.Result, Error: errStr}
		}
		return
	}
	if env.Method != "" {
		em.routeNotification(env.Method, env.Params)
	}
}

// onExit tears down state when the engine process dies so pending requests fail
// fast and a later job re-spawns a fresh engine.
func (em *EngineManager) onExit() {
	em.mu.Lock()
	em.started = false
	em.stdin = nil
	em.cmd = nil
	// Capture the active job pointers under em.mu, then release before touching
	// each job's own Mu — same lock order as routeNotification (em.mu -> job.Mu)
	// to avoid deadlock.
	timing := em.timingJob
	suppress := em.suppressJob
	em.mu.Unlock()

	// A still-running job will never get its finished notification once the
	// engine dies, so fail it explicitly instead of leaving it stuck on running.
	if timing != nil {
		timing.Mu.Lock()
		if timing.Status == "running" {
			timing.Status = "error"
			timing.Error = "内核进程已退出"
			timing.FinishReason = "EngineExited"
		}
		timing.Mu.Unlock()
	}
	if suppress != nil {
		suppress.Mu.Lock()
		if suppress.Status == "running" {
			suppress.Status = "error"
			suppress.Error = "内核进程已退出"
			suppress.FinishReason = "EngineExited"
		}
		suppress.Mu.Unlock()
	}

	em.pending.Range(func(k, v interface{}) bool {
		em.pending.Delete(k)
		select {
		case v.(chan rawResponse) <- rawResponse{Error: "engine exited"}:
		default:
		}
		return true
	})
}

// request sends a method call and waits for the matching response.
func (em *EngineManager) request(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if err := em.ensureStarted(); err != nil {
		return nil, err
	}

	id := atomic.AddInt64(&em.nextID, 1)
	ch := make(chan rawResponse, 1)
	em.pending.Store(id, ch)

	payload := map[string]interface{}{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		em.pending.Delete(id)
		return nil, err
	}

	em.mu.Lock()
	stdin := em.stdin
	em.mu.Unlock()
	if stdin == nil {
		em.pending.Delete(id)
		return nil, errors.New("engine not running")
	}

	em.writeMu.Lock()
	_, werr := stdin.Write(append(buf, '\n'))
	em.writeMu.Unlock()
	if werr != nil {
		em.pending.Delete(id)
		return nil, fmt.Errorf("engine write: %w", werr)
	}

	select {
	case <-ctx.Done():
		em.pending.Delete(id)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return resp.Result, nil
	}
}

func (em *EngineManager) activeTiming() *EngineTimingJob {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.timingJob
}

func (em *EngineManager) activeSuppress() *EngineSuppressJob {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.suppressJob
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

// StartTiming launches an auto-timing run and registers the active job. The
// engine returns "ok" immediately; matching runs async and streams notifications.
func (em *EngineManager) StartTiming(taskID string, p TimingParams) (*EngineTimingJob, error) {
	job := &EngineTimingJob{TaskID: taskID, ScriptPath: p.ScriptPath, Status: "running"}
	em.mu.Lock()
	// Single-flight: refuse a second run rather than overwriting the active job
	// pointer (which would orphan the old job and let its notifications bleed into
	// the new one).
	if prev := em.timingJob; prev != nil {
		prev.Mu.Lock()
		running := prev.Status == "running"
		prev.Mu.Unlock()
		if running {
			em.mu.Unlock()
			return nil, ErrTimingBusy
		}
	}
	em.timingJob = job
	em.mu.Unlock()

	// Fire subtitle.start asynchronously and return the taskId now. The engine
	// awaits EnsureResource (first-run download of VideoProcess templates/fonts)
	// before acking, which on a fresh machine with a slow network can take minutes;
	// blocking the HTTP start that long would freeze the UI and, worse, leave it
	// unable to cancel. A start failure instead surfaces through the job's terminal
	// state, which the progress poll reads.
	go func() {
		// A cancel issued before the engine was spawned (Cancel's !started path)
		// disowns the job; if that already happened, don't spawn an engine to run a
		// run the user already canceled.
		if em.activeTiming() != job {
			return
		}
		// The engine only acks subtitle.start after EnsureResource (first-run download
		// of VideoProcess templates/fonts), which on a fresh machine with a slow
		// network can take many minutes; the timeout is a generous backstop, not the
		// job's deadline. A deadline here does NOT mean the run failed — the engine may
		// still be downloading and will drive the job to a terminal state through its
		// notifications (or onExit). So on deadline we leave the job registered and
		// running to preserve single-flight and keep routing those notifications to it;
		// only a genuine start error (engine error response, write failure, missing
		// binary) fails the job and clears the pointer.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := em.request(ctx, "subtitle.start", p); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return
			}
			job.Mu.Lock()
			if job.Status == "running" {
				job.Status = "error"
				job.Error = "启动打轴失败: " + err.Error()
			}
			job.Mu.Unlock()
			em.mu.Lock()
			if em.timingJob == job {
				em.timingJob = nil
			}
			em.mu.Unlock()
		}
	}()
	return job, nil
}

// Export pulls the assembled ASS subtitle from the active timing run.
func (em *EngineManager) Export() (string, error) {
	// Subtitle assembly from already-collected frame sets can take a while on a long
	// video / large dialog set; a tight 30s would spuriously fail an otherwise-fine export.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := em.request(ctx, "subtitle.export", nil)
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
func (em *EngineManager) TimingLines() (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return em.request(ctx, "subtitle.lines", nil)
}

// TimingLineCall 代理单行编辑类方法（subtitle.setSeparator / subtitle.setTranslation /
// subtitle.estimateSeparator），params 原样透传。
func (em *EngineManager) TimingLineCall(method string, params interface{}) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return em.request(ctx, method, params)
}

// TimingFrame 取指定帧的画面（base64 jpeg），给分隔帧微调的实时预览用。
func (em *EngineManager) TimingFrame(frame, maxWidth int) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	params := map[string]int{"frame": frame}
	if maxWidth > 0 {
		params["maxWidth"] = maxWidth
	}
	return em.request(ctx, "subtitle.frame", params)
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

// StartSuppress launches an encode run and registers the active job.
func (em *EngineManager) StartSuppress(taskID string, p SuppressParams) (*EngineSuppressJob, error) {
	if p.FfmpegPath == "" {
		p.FfmpegPath = em.ffmpegPath
	}
	job := &EngineSuppressJob{TaskID: taskID, Status: "running", OutputPath: p.OutputPath}
	em.mu.Lock()
	// Single-flight: refuse a second run rather than overwriting the active job
	// pointer (which would orphan the old job and let its notifications bleed into
	// the new one).
	if prev := em.suppressJob; prev != nil {
		prev.Mu.Lock()
		running := prev.Status == "running"
		prev.Mu.Unlock()
		if running {
			em.mu.Unlock()
			return nil, ErrSuppressBusy
		}
	}
	em.suppressJob = job
	em.mu.Unlock()

	// Async start (see StartTiming): return the taskId immediately so the UI stays
	// responsive and cancelable; a start failure surfaces via the job's terminal state.
	go func() {
		// See StartTiming: bail if a cancel disowned the job before the engine was
		// spawned, and treat a start deadline as a benign backstop (the engine may
		// still be running EnsureResource) rather than a job failure.
		if em.activeSuppress() != job {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := em.request(ctx, "suppress.start", p); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return
			}
			job.Mu.Lock()
			if job.Status == "running" {
				job.Status = "error"
				job.Error = "启动压制失败: " + err.Error()
			}
			job.Mu.Unlock()
			em.mu.Lock()
			if em.suppressJob == job {
				em.suppressJob = nil
			}
			em.mu.Unlock()
		}
	}()
	return job, nil
}

// --- Control ---

// Cancel stops the active run in a domain ("timing" or "suppress").
func (em *EngineManager) Cancel(domain string) error {
	method := ""
	switch domain {
	case "timing":
		method = "subtitle.stop"
	case "suppress":
		method = "suppress.stop"
	default:
		return fmt.Errorf("unknown domain: %s", domain)
	}
	// Nothing to send a stop to if the engine isn't up yet — don't spawn a fresh one
	// just to fire a stop into the void (ensureStarted would otherwise relaunch it).
	em.mu.Lock()
	started := em.started
	em.mu.Unlock()
	if !started {
		// StartTiming/StartSuppress register the job as "running" before their async
		// goroutine lazily spawns the engine, so a job can be presented to the UI as
		// running while started==false. Returning success here would drop the cancel
		// silently while the goroutine goes on to spawn the engine and run. Instead
		// disown the registered running job (mark it canceled and clear its pointer);
		// the start goroutine's pre-spawn check then sees the job is no longer active
		// and does not spawn the engine. Capture the pointer under em.mu, then touch
		// the job's own Mu without holding em.mu — same lock discipline as onExit.
		switch domain {
		case "timing":
			em.mu.Lock()
			j := em.timingJob
			em.mu.Unlock()
			if j != nil {
				j.Mu.Lock()
				canceled := j.Status == "running"
				if canceled {
					j.Status = "canceled"
					j.FinishReason = "Canceled"
				}
				j.Mu.Unlock()
				if canceled {
					em.mu.Lock()
					if em.timingJob == j {
						em.timingJob = nil
					}
					em.mu.Unlock()
				}
			}
		case "suppress":
			em.mu.Lock()
			j := em.suppressJob
			em.mu.Unlock()
			if j != nil {
				j.Mu.Lock()
				canceled := j.Status == "running"
				if canceled {
					j.Status = "canceled"
					j.FinishReason = "Canceled"
				}
				j.Mu.Unlock()
				if canceled {
					em.mu.Lock()
					if em.suppressJob == j {
						em.suppressJob = nil
					}
					em.mu.Unlock()
				}
			}
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := em.request(ctx, method, nil)
	return err
}

// Ping returns the engine's readiness/version handshake.
func (em *EngineManager) Ping() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := em.request(ctx, "system.version", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	_ = json.Unmarshal(res, &out)
	return out, nil
}

func (em *EngineManager) TimingJob(taskID string) (*EngineTimingJob, bool) {
	j := em.activeTiming()
	if j != nil && j.TaskID == taskID {
		return j, true
	}
	return nil, false
}

func (em *EngineManager) SuppressJob(taskID string) (*EngineSuppressJob, bool) {
	j := em.activeSuppress()
	if j != nil && j.TaskID == taskID {
		return j, true
	}
	return nil, false
}

// routeNotification updates the active job's progress state from an engine event.
func (em *EngineManager) routeNotification(method string, params json.RawMessage) {
	switch {
	case method == "subtitle.started":
		var p struct{ DialogTotal, BannerTotal, MarkerTotal int }
		_ = json.Unmarshal(params, &p)
		if j := em.activeTiming(); j != nil {
			j.Mu.Lock()
			j.DialogTotal, j.BannerTotal, j.MarkerTotal = p.DialogTotal, p.BannerTotal, p.MarkerTotal
			j.Mu.Unlock()
		}
	case method == "subtitle.progress":
		var p struct{ Percent float64 }
		_ = json.Unmarshal(params, &p)
		if j := em.activeTiming(); j != nil {
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
		}
	case method == "subtitle.fps":
		var p struct {
			Fps int
			Eta string
		}
		_ = json.Unmarshal(params, &p)
		if j := em.activeTiming(); j != nil {
			j.Mu.Lock()
			j.Fps, j.Eta = p.Fps, p.Eta
			j.Mu.Unlock()
		}
	case method == "subtitle.preview":
		var p struct{ Base64 string }
		_ = json.Unmarshal(params, &p)
		if j := em.activeTiming(); j != nil {
			j.Mu.Lock()
			j.PreviewB64 = p.Base64
			j.Mu.Unlock()
		}
	case method == "subtitle.dialog" || method == "subtitle.banner" || method == "subtitle.marker":
		if j := em.activeTiming(); j != nil {
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
		}
	case method == "subtitle.finished":
		var p struct{ Reason string }
		_ = json.Unmarshal(params, &p)
		if j := em.activeTiming(); j != nil {
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
		}
	case method == "subtitle.error":
		var p struct{ Message string }
		_ = json.Unmarshal(params, &p)
		if j := em.activeTiming(); j != nil {
			j.Mu.Lock()
			j.Error = p.Message
			j.Mu.Unlock()
		}

	case method == "suppress.progress":
		var p struct {
			Frame int
			Total int
			Fps   float64
		}
		_ = json.Unmarshal(params, &p)
		if j := em.activeSuppress(); j != nil {
			j.Mu.Lock()
			j.Frame, j.Total, j.Fps = p.Frame, p.Total, p.Fps
			j.Mu.Unlock()
		}
	case method == "suppress.log" || method == "suppress.progressLog":
		var p struct{ Line string }
		_ = json.Unmarshal(params, &p)
		if j := em.activeSuppress(); j != nil {
			j.Mu.Lock()
			j.LastLog = p.Line
			j.Mu.Unlock()
		}
	case method == "suppress.finished":
		var p struct {
			Reason string
			Error  string
		}
		_ = json.Unmarshal(params, &p)
		if j := em.activeSuppress(); j != nil {
			j.Mu.Lock()
			j.FinishReason = p.Reason
			if p.Reason == "Completed" {
				j.Status = "done"
			} else if p.Reason == "Canceled" {
				j.Status = "canceled"
			} else {
				j.Status = "error"
				j.Error = p.Error
			}
			j.Mu.Unlock()
		}
	}
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
