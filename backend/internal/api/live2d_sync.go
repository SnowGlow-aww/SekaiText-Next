package api

// Live2D online-asset sync: download the locally-missing models + their motion
// data from the CDNs into the local mirror (config.Live2DLocalDir), with
// progress. Any user can run it. Triggered by the Live2D plugin's settings UI.
//
// Sourcing mirrors the plugin's runtime loader (utils/live2d/modelLoader.ts):
//   - model_list.json (the model index) + motion data: sekai.best.
//   - model bodies (buildmodeldata + model3 + moc/textures/physics): exmeaning.
// Every asset is written to live2dLocalPath(root, url) so the Live2DProxy
// local-first lookup reads exactly what we wrote.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sekaitext/backend/internal/fsutil"
	"sekaitext/backend/internal/model"
)

// CDN bases for the Live2D asset mirror. The path structure MUST match the plugin's
// constants/live2d.ts (SEKAI_BEST_LIVE2D / EXMEANING_BASE) so the derived local
// paths line up with what playback fetches.
//
// Model bodies go through the project's own OSS-backed edge CDN, which mirror-caches
// them from exmeaning on a miss (bucket rule: prefix sekai-jp-assets/ →
// storage2.exmeaning.com). The host differs from the plugin's EXMEANING_BASE but the
// path after the host is identical, so live2dLocalPath still resolves to the same
// on-disk location. model_list + motion stay on sekai.best — it sits behind
// Cloudflare and can't be mirror-fetched from the mainland.
const (
	live2dSekaiBest           = "https://storage.sekai.best/sekai-live2d-assets"
	live2dExmeaning           = "https://sakimizuki.accr.cc/sekai-jp-assets"
	maxLive2DAssetBytes int64 = 128 << 20
	live2dTaskGrace           = 5 * time.Minute
	live2dTaskMaxAge          = 60 * time.Minute
	live2dTaskSweep           = time.Minute
)

type live2dSyncTask struct {
	progress   *model.Live2DSyncProgress
	ctx        context.Context
	cancel     context.CancelFunc
	root       string
	createdAt  time.Time
	finishedAt time.Time // guarded by Handler.live2dSyncMu
}

// live2dModelListEntry is one record of model_list.json (sekai.best).
type live2dModelListEntry struct {
	ModelName string `json:"modelName"`
	ModelBase string `json:"modelBase"`
	ModelPath string `json:"modelPath"`
	ModelFile string `json:"modelFile"`
}

type live2dModelRef struct {
	modelPath string
	modelBase string
	modelFile string
}

// live2dBuildModelData is exmeaning's buildmodeldata.json. Only Moc3FileName is
// load-bearing (it yields the model3 base name); the other fields are parsed for
// completeness but the authoritative file set comes from the model3.
type live2dBuildModelData struct {
	Moc3FileName    string   `json:"Moc3FileName"`
	TextureNames    []string `json:"TextureNames"`
	PhysicsFileName string   `json:"PhysicsFileName"`
}

// live2dModel3 is the subset of a Cubism model3 we need to enumerate body files.
type live2dModel3 struct {
	FileReferences struct {
		Moc      string   `json:"Moc"`
		Textures []string `json:"Textures"`
		Physics  string   `json:"Physics"`
	} `json:"FileReferences"`
}

// live2dMotionList is sekai.best's BuildMotionData.json.
type live2dMotionList struct {
	Motions     []string `json:"motions"`
	Expressions []string `json:"expressions"`
}

// Live2DSync starts an async sync of the Live2D online asset library: it diffs
// the upstream model_list against the local mirror and downloads whatever is
// missing (model bodies + motion data). It returns {"taskId":...} immediately;
// progress is polled via Live2DSyncProgress.
func (h *Handler) Live2DSync(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Live2DLocalDir == "" {
		writeError(w, http.StatusInternalServerError, "live2d local dir not configured")
		return
	}
	// Parallel download fan-out, chosen by the user (1–50, default 5).
	concurrency := 5
	if c := r.URL.Query().Get("concurrency"); c != "" {
		if n, err := strconv.Atoi(c); err == nil {
			concurrency = n
		}
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 50 {
		concurrency = 50
	}
	root := live2dRootKey(h.cfg.Live2DLocalDir)
	task, reused := h.registerLive2DSync(root)
	if !reused {
		go h.runLive2DSync(task, concurrency)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"taskId": task.progress.TaskID, "reused": reused})
}

// Live2DSyncProgress returns a snapshot of a sync task. Unlike DownloadProgress,
// terminal (done/error) tasks are intentionally NOT deleted on read, so the
// frontend can reliably observe the final state across poll cycles.
func (h *Handler) Live2DSyncProgress(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}
	h.live2dSyncMu.Lock()
	task, ok := h.live2dSyncTasks[taskID]
	h.live2dSyncMu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	progress := task.progress

	// Snapshot the mutable fields under the lock into a mutex-free value so
	// encoding happens off-lock and go vet doesn't flag copying a sync.Mutex.
	progress.Mu.Lock()
	snap := struct {
		TaskID       string `json:"taskId"`
		Status       string `json:"status"`
		Total        int    `json:"total"`
		Current      int    `json:"current"`
		CurrentModel string `json:"currentModel"`
		Files        int    `json:"files"`
		Bytes        int64  `json:"bytes"`
		Failed       int    `json:"failed"`
		Error        string `json:"error,omitempty"`
	}{
		TaskID:       progress.TaskID,
		Status:       progress.Status,
		Total:        progress.Total,
		Current:      progress.Current,
		CurrentModel: progress.CurrentModel,
		Files:        progress.Files,
		Bytes:        progress.Bytes,
		Failed:       progress.Failed,
		Error:        progress.Error,
	}
	progress.Mu.Unlock()

	writeJSON(w, http.StatusOK, snap)
	// NOTE: do NOT delete terminal tasks here (see doc comment above).
}

// Live2DSyncCancel cancels an active task. CDN requests and lock waits carry the
// task context, so cancellation does not wait for the HTTP client's full timeout.
func (h *Handler) Live2DSyncCancel(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}
	h.live2dSyncMu.Lock()
	task, ok := h.live2dSyncTasks[taskID]
	h.live2dSyncMu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	markLive2DCanceled(task.progress, "sync canceled")
	task.cancel()
	writeJSON(w, http.StatusOK, map[string]string{"status": "canceled"})
}

func (h *Handler) registerLive2DSync(root string) (*live2dSyncTask, bool) {
	h.live2dSyncMu.Lock()
	defer h.live2dSyncMu.Unlock()
	if h.live2dSyncTasks == nil {
		h.live2dSyncTasks = make(map[string]*live2dSyncTask)
		h.live2dSyncRoots = make(map[string]string)
	}
	if taskID := h.live2dSyncRoots[root]; taskID != "" {
		if task := h.live2dSyncTasks[taskID]; task != nil && task.finishedAt.IsZero() {
			return task, true
		}
		delete(h.live2dSyncRoots, root)
	}
	createdAt := time.Now()
	taskID := strconv.FormatInt(createdAt.UnixNano(), 36)
	ctx, cancel := context.WithCancel(context.Background())
	task := &live2dSyncTask{
		progress:  &model.Live2DSyncProgress{TaskID: taskID, Status: "checking"},
		ctx:       ctx,
		cancel:    cancel,
		root:      root,
		createdAt: createdAt,
	}
	h.live2dSyncTasks[taskID] = task
	h.live2dSyncRoots[root] = taskID
	return task, false
}

func (h *Handler) finishLive2DSync(task *live2dSyncTask) {
	h.live2dSyncMu.Lock()
	defer h.live2dSyncMu.Unlock()
	if task.finishedAt.IsZero() {
		task.finishedAt = time.Now()
	}
	if h.live2dSyncRoots[task.root] == task.progress.TaskID {
		delete(h.live2dSyncRoots, task.root)
	}
}

func live2dProgressTerminal(progress *model.Live2DSyncProgress) bool {
	progress.Mu.Lock()
	defer progress.Mu.Unlock()
	return progress.Status == "done" || progress.Status == "error" || progress.Status == "canceled"
}

func markLive2DCanceled(progress *model.Live2DSyncProgress, reason string) {
	progress.Mu.Lock()
	defer progress.Mu.Unlock()
	if progress.Status == "done" || progress.Status == "error" {
		return
	}
	progress.Status = "canceled"
	progress.Error = reason
}

func (h *Handler) startLive2DTaskGC() {
	go func() {
		ticker := time.NewTicker(live2dTaskSweep)
		defer ticker.Stop()
		for now := range ticker.C {
			h.gcLive2DTasks(now)
		}
	}()
}

func (h *Handler) gcLive2DTasks(now time.Time) {
	var cancel []*live2dSyncTask
	h.live2dSyncMu.Lock()
	for taskID, task := range h.live2dSyncTasks {
		terminal := live2dProgressTerminal(task.progress)
		if !terminal && now.Sub(task.createdAt) > live2dTaskMaxAge {
			if h.live2dSyncRoots[task.root] == taskID {
				delete(h.live2dSyncRoots, task.root)
			}
			if task.finishedAt.IsZero() {
				task.finishedAt = now
			}
			cancel = append(cancel, task)
			continue
		}
		if terminal && task.finishedAt.IsZero() {
			task.finishedAt = now
		}
		if !task.finishedAt.IsZero() && now.Sub(task.finishedAt) > live2dTaskGrace {
			delete(h.live2dSyncTasks, taskID)
		}
	}
	h.live2dSyncMu.Unlock()
	for _, task := range cancel {
		markLive2DCanceled(task.progress, "sync exceeded maximum task age")
		task.cancel()
	}
}

func live2dRootKey(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	probe := abs
	var missing []string
	for {
		if resolved, resolveErr := filepath.EvalSymlinks(probe); resolveErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved)
		} else if !os.IsNotExist(resolveErr) {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
	return filepath.Clean(abs)
}

// runLive2DSync is the background worker. It never panics the goroutine: any
// panic is recovered and surfaced as an error status.
func (h *Handler) runLive2DSync(task *live2dSyncTask, concurrency int) {
	progress := task.progress
	defer h.finishLive2DSync(task)
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[live2d-sync] panic: %v", rec)
			h.live2dSyncFail(progress, fmt.Sprintf("internal error: %v", rec))
		}
	}()

	root := task.root
	ctx := task.ctx
	unlockRoot, err := live2dLockPath(ctx, root)
	if err != nil {
		if ctx.Err() != nil {
			markLive2DCanceled(progress, ctx.Err().Error())
		} else {
			h.live2dSyncFail(progress, "lock live2d root: "+err.Error())
		}
		return
	}
	defer unlockRoot()

	// 1. Fetch the upstream model index.
	listURL := live2dSekaiBest + "/live2d/model_list.json"
	listBody, err := h.live2dFetch(ctx, listURL)
	if err != nil {
		h.live2dSyncFail(progress, "fetch model_list.json: "+err.Error())
		return
	}
	var entries []live2dModelListEntry
	if err := json.Unmarshal(listBody, &entries); err != nil {
		h.live2dSyncFail(progress, "parse model_list.json: "+err.Error())
		return
	}

	// 2. Validate the remote index before trusting any path from it. An empty or
	// malformed index must never replace the known-good local publication.
	unique, err := validateLive2DModelList(entries)
	if err != nil {
		h.live2dSyncFail(progress, "validate model_list.json: "+err.Error())
		return
	}

	// 3. Diff vs the local mirror: a model needs (re)downloading unless every body
	//    file playback needs is present on disk — build metadata, model3, moc3, all
	//    referenced textures, and physics. Checking only the moc3 (the old behaviour)
	//    missed a deleted/partial texture or physics — the model looked complete but
	//    wasn't, so the sync reported "done" without restoring the file. See
	//    live2dModelComplete.
	var missing []live2dModelRef
	for _, m := range unique {
		dir := filepath.Join(root, "model", filepath.FromSlash(m.modelPath))
		if !live2dModelComplete(dir, m.modelFile) {
			missing = append(missing, m)
		}
	}

	progress.Mu.Lock()
	if progress.Status == "canceled" {
		progress.Mu.Unlock()
		return
	}
	progress.Status = "downloading"
	progress.Total = len(missing)
	progress.Mu.Unlock()

	// 4. Download each missing model. Per the spec, a body failure
	//    (buildmodeldata/model3/moc3) skips just that model; textures/motions are
	//    individually resilient. Current advances per processed model regardless,
	//    so the progress bar always completes.
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, m := range missing {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			markLive2DCanceled(progress, ctx.Err().Error())
			return
		}
		wg.Add(1)
		go func(m live2dModelRef) {
			defer wg.Done()
			defer func() { <-sem }()
			// Advance progress in a defer so a panicked model still counts: it runs
			// during the panic unwind (and after the recover below), whereas the old
			// inline increment was skipped on panic — leaving Current<Total while the
			// task was still forced to "done". A model that didn't complete (error,
			// incomplete on disk, or panic) counts as Failed.
			completed := false
			defer func() {
				progress.Mu.Lock()
				progress.Current++
				if !completed {
					progress.Failed++
				}
				progress.Mu.Unlock()
			}()
			// Per-model panic guard so one bad model can't take down its worker.
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[live2d-sync] model %s panic: %v", m.modelPath, rec)
				}
			}()

			progress.Mu.Lock()
			progress.CurrentModel = m.modelPath
			progress.Mu.Unlock()

			err := h.live2dSyncModel(ctx, progress, root, m.modelPath, m.modelBase, m.modelFile)
			// nil only means the fatal body files loaded; a best-effort texture/physics
			// missing on BOTH mirrors still leaves the model incomplete. Re-check the
			// on-disk set so the tally is honest instead of always reporting success.
			dir := filepath.Join(root, "model", filepath.FromSlash(m.modelPath))
			ok := err == nil && live2dModelComplete(dir, m.modelFile)
			if err != nil {
				log.Printf("[live2d-sync] skip model %s: %v", m.modelPath, err)
			} else if !ok {
				log.Printf("[live2d-sync] model %s incomplete after download (asset missing upstream)", m.modelPath)
			}
			completed = ok
		}(m)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		markLive2DCanceled(progress, err.Error())
		return
	}

	// 5. Refresh the local model_list.json index so the mirror reflects upstream.
	dst := live2dLocalPath(root, listURL)
	if dst == "" {
		h.live2dSyncFail(progress, "publish model_list.json: destination is not mirrorable")
		return
	}
	unlock, lockErr := live2dLockPath(ctx, dst)
	if lockErr != nil {
		if ctx.Err() != nil {
			markLive2DCanceled(progress, ctx.Err().Error())
		} else {
			h.live2dSyncFail(progress, "publish model_list.json: "+lockErr.Error())
		}
		return
	}
	writeErr := fsutil.WriteFileAtomic(dst, listBody, 0o644)
	unlock()
	if writeErr != nil {
		h.live2dSyncFail(progress, "publish model_list.json: "+writeErr.Error())
		return
	}

	completeLive2DSync(ctx, progress)
}

func completeLive2DSync(ctx context.Context, progress *model.Live2DSyncProgress) {
	progress.Mu.Lock()
	defer progress.Mu.Unlock()
	if progress.Status == "canceled" {
		return
	}
	if err := ctx.Err(); err != nil {
		progress.Status = "canceled"
		progress.Error = err.Error()
		return
	}
	progress.Status = "done"
	if progress.Failed > 0 {
		progress.Error = fmt.Sprintf("%d/%d 个模型未能完整下载(个别贴图/资源上游缺失)", progress.Failed, progress.Total)
		if progress.Failed >= progress.Total {
			progress.Status = "error"
		}
	}
}

// live2dSyncModel downloads one model's body (exmeaning) + motion data
// (sekai.best). Returning an error means the model body is unusable and the
// model is skipped; texture/motion/physics failures are logged and tolerated.
func (h *Handler) live2dSyncModel(ctx context.Context, task *model.Live2DSyncProgress, root, modelPath, modelBase, modelFile string) error {
	bodyDir := live2dExmeaning + "/live2d/model/" + modelPath + "/"

	// --- body: buildmodeldata.json (mirrored; its Moc3FileName is a fallback) ---
	// live2dFetchAndMirror prefers the on-disk copy, so an already-mirrored model
	// (even one left incomplete by an upstream-deleted texture) doesn't re-fetch and
	// re-count this JSON on every manual sync.
	bmdURL := bodyDir + "buildmodeldata.json"
	bmdBody, err := h.live2dFetchAndMirror(ctx, task, root, bmdURL)
	if err != nil {
		return fmt.Errorf("buildmodeldata: %w", err)
	}
	var bmd live2dBuildModelData
	if err := json.Unmarshal(bmdBody, &bmd); err != nil {
		return fmt.Errorf("parse buildmodeldata: %w", err)
	}
	// buildmodeldata's Moc3FileName is the AUTHORITATIVE base name for exmeaning's body
	// files: it carries the correct REVISION (e.g. ...t08). model_list's modelFile can
	// name an OLDER revision (...t06/t01) — preferring it outright 404s those models — but
	// it DOES carry the correct CASE (April2026 mains: "April" in buildmodeldata, lowercase
	// "april" files). So keep Moc3FileName's revision and only borrow modelFile's case when
	// the two differ by case alone.
	baseName := selectedLive2DBase(bmd, modelFile)
	if !live2dSafeAssetName(baseName) {
		return fmt.Errorf("cannot determine model3 base name")
	}

	// --- body: {baseName}.model3 (no .json ext) -> FileReferences ---
	model3URL := bodyDir + baseName + ".model3"
	model3Body, err := h.live2dFetchAndMirror(ctx, task, root, model3URL)
	if err != nil {
		return fmt.Errorf("model3: %w", err)
	}
	var m3 live2dModel3
	if err := json.Unmarshal(model3Body, &m3); err != nil {
		return fmt.Errorf("parse model3: %w", err)
	}
	if !live2dSafeAssetName(m3.FileReferences.Moc) || !strings.HasSuffix(strings.ToLower(m3.FileReferences.Moc), ".moc3") {
		return fmt.Errorf("model3 has invalid moc reference")
	}
	for _, texture := range m3.FileReferences.Textures {
		if !live2dSafeModelPath(texture) {
			return fmt.Errorf("model3 has invalid texture reference %q", texture)
		}
	}

	// --- body: moc3 / textures / physics ---
	// The model3's FileReferences can declare a different CASE than the files that
	// actually exist (some April2026 mains: "April" inside the model3, but the files
	// are "april"). baseName (from model_list modelFile) is the authoritative case,
	// so rebuild the moc/physics names from it and swap the texture path's prefix.
	refBase := strings.TrimSuffix(m3.FileReferences.Moc, ".moc3")

	// moc3 (the file the delta check looks for) — fatal on failure.
	if err := h.live2dDownload(ctx, task, root, bodyDir+baseName+".moc3"); err != nil {
		return fmt.Errorf("moc3: %w", err)
	}

	// textures — per-file resilient.
	for _, tex := range m3.FileReferences.Textures {
		if tex == "" {
			continue
		}
		realTex := tex
		if refBase != "" && refBase != baseName {
			realTex = strings.Replace(tex, refBase, baseName, 1)
		}
		if err := h.live2dDownload(ctx, task, root, bodyDir+realTex); err != nil {
			log.Printf("[live2d-sync] %s: skip texture %s: %v", modelPath, realTex, err)
		}
	}

	// physics — the real CDN file drops the .json suffix (model3 declares .physics3.json).
	if m3.FileReferences.Physics != "" {
		if err := h.live2dDownload(ctx, task, root, bodyDir+baseName+".physics3"); err != nil {
			log.Printf("[live2d-sync] %s: skip physics: %v", modelPath, err)
		}
	}

	// --- motion data (sekai.best) — never fatal; a model with no motions is
	//     still usable (it just won't animate) ---
	h.live2dSyncMotion(ctx, task, root, modelPath, modelBase)

	return nil
}

func selectedLive2DBase(bmd live2dBuildModelData, modelFile string) string {
	mfBase := strings.TrimSuffix(modelFile, ".model3.json")
	mfBase = strings.TrimSuffix(mfBase, ".model3")
	mocBase := strings.TrimSuffix(bmd.Moc3FileName, ".moc3.bytes")
	mocBase = strings.TrimSuffix(mocBase, ".moc3")
	if mocBase == "" {
		return mfBase
	}
	if mfBase != "" && strings.EqualFold(mfBase, mocBase) {
		return mfBase
	}
	return mocBase
}

// live2dSyncMotion downloads the model's motion + facial clips from sekai.best.
// motionDir = modelPath minus its last segment; motionBase starts at modelBase
// and is shortened one "_segment" at a time until BuildMotionData.json returns
// 200 (or only one segment remains, in which case the model is left motionless).
func (h *Handler) live2dSyncMotion(ctx context.Context, task *model.Live2DSyncProgress, root, modelPath, modelBase string) {
	parts := strings.Split(modelPath, "/")
	if len(parts) <= 1 {
		return
	}
	motionDir := strings.Join(parts[:len(parts)-1], "/")

	base := modelBase
	var motionListBody []byte
	var motionBase string
	for base != "" {
		url := live2dSekaiBest + "/live2d/motion/" + motionDir + "/" + base + "_motion_base/BuildMotionData.json"
		// Multiple costume models of one character collapse to the same
		// {motionBase}_motion_base dir, so its BuildMotionData.json is shared.
		// live2dFetchAndMirror prefers the on-disk copy and single-flights the
		// fetch+write+count per destination, so it's fetched/counted once instead of
		// once per model (and concurrent siblings don't double-count it).
		if body, err := h.live2dFetchAndMirror(ctx, task, root, url); err == nil {
			motionListBody = body
			motionBase = base
			break
		}
		segs := strings.Split(base, "_")
		if len(segs) <= 1 {
			break
		}
		base = strings.Join(segs[:len(segs)-1], "_")
	}
	if motionListBody == nil {
		log.Printf("[live2d-sync] %s: no motion list (model usable but motionless)", modelPath)
		return
	}

	var ml live2dMotionList
	if err := json.Unmarshal(motionListBody, &ml); err != nil {
		log.Printf("[live2d-sync] %s: parse BuildMotionData: %v", modelPath, err)
		return
	}

	motionRoot := live2dSekaiBest + "/live2d/motion/" + motionDir + "/" + motionBase + "_motion_base/"
	for _, n := range ml.Motions {
		if n == "" {
			continue
		}
		if err := h.live2dDownload(ctx, task, root, motionRoot+"motion/"+n+".motion3.json"); err != nil {
			log.Printf("[live2d-sync] %s: skip motion %s: %v", modelPath, n, err)
		}
	}
	for _, n := range ml.Expressions {
		if n == "" {
			continue
		}
		if err := h.live2dDownload(ctx, task, root, motionRoot+"facial/"+n+".motion3.json"); err != nil {
			log.Printf("[live2d-sync] %s: skip facial %s: %v", modelPath, n, err)
		}
	}
}

// live2dFetch GETs a model-body asset, trying the exmeaning/CDN URL first and
// falling back to the sekai.best equivalent on failure. exmeaning mirrors most
// bodies but is missing some models' textures (which live only on sekai.best);
// without the fallback those models could never complete and the sync would loop
// forever reporting them "missing". model_list/motion URLs are already sekai.best
// so the fallback is a no-op for them.
func (h *Handler) live2dFetch(ctx context.Context, url string) ([]byte, error) {
	body, err := h.live2dFetchOnce(ctx, url)
	if err == nil {
		return body, nil
	}
	if alt := live2dSekaiFallback(url); alt != "" {
		if body2, err2 := h.live2dFetchOnce(ctx, alt); err2 == nil {
			return body2, nil
		}
	}
	return nil, err
}

// live2dSyncHTTP uses normal platform TLS verification and re-checks the exact
// HTTPS host allowlist on every redirect hop.
var live2dSyncHTTP = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if !live2dHostAllowed(req.URL.String()) {
			return fmt.Errorf("redirect to disallowed host: %s", req.URL.String())
		}
		return nil
	},
}

// live2dFetchOnce GETs a single allowed URL through a redirect-guarded client and
// returns its bounded body (200 only). The host
// is restricted to the known Live2D asset CDNs (anti-SSRF), reusing live2dAllowedHosts,
// and live2dSyncHTTP re-checks that guard on every redirect hop.
func (h *Handler) live2dFetchOnce(ctx context.Context, url string) ([]byte, error) {
	if !live2dHostAllowed(url) {
		return nil, fmt.Errorf("url host not allowed: %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := live2dSyncHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readLive2DBoundedBody(resp.Body, resp.ContentLength, maxLive2DAssetBytes)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	if !live2dAssetBodyValid(url, body) {
		return nil, fmt.Errorf("response content does not match asset type")
	}
	return body, nil
}

func readLive2DBoundedBody(r io.Reader, contentLength, limit int64) ([]byte, error) {
	if contentLength > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return body, nil
}

func live2dAssetBodyValid(rawURL string, body []byte) bool {
	u, err := url.Parse(rawURL)
	if err != nil || len(body) == 0 {
		return false
	}
	path := strings.ToLower(u.Path)
	switch {
	case strings.HasSuffix(path, ".json"), strings.HasSuffix(path, ".model3"), strings.HasSuffix(path, ".physics3"):
		return json.Valid(body)
	case strings.HasSuffix(path, ".png"):
		return len(body) >= 8 && string(body[:8]) == "\x89PNG\r\n\x1a\n"
	case strings.HasSuffix(path, ".moc3"):
		return len(body) >= 4 && string(body[:4]) == "MOC3"
	default:
		return false
	}
}

func live2dCachedFileValid(path, rawURL string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxLive2DAssetBytes {
		return false
	}
	body, err := os.ReadFile(path)
	return err == nil && live2dAssetBodyValid(rawURL, body)
}

// live2dSekaiFallback maps an exmeaning/CDN model-body URL to its sekai.best
// equivalent (…/sekai-jp-assets/… → …/sekai-live2d-assets/…, same path after the
// live2d/ segment, so it resolves to the same local mirror path). Returns "" when
// the URL isn't an exmeaning/CDN body URL.
func live2dSekaiFallback(url string) string {
	if strings.HasPrefix(url, live2dExmeaning+"/") {
		return live2dSekaiBest + strings.TrimPrefix(url, live2dExmeaning)
	}
	return ""
}

// live2dDownload fetches url and writes it into the local mirror. If the file is
// already present (non-empty) it is skipped: CDN assets are immutable per path, so
// this makes the sync idempotent/resumable — a retry after an interruption doesn't
// re-fetch everything. (The model_list.json refresh is a separate write and still runs.)
func (h *Handler) live2dDownload(ctx context.Context, task *model.Live2DSyncProgress, root, url string) error {
	dst := live2dLocalPath(root, url)
	if dst == "" {
		// Not mirrorable — fetch anyway so live2dStore surfaces the same error as before.
		body, err := h.live2dFetch(ctx, url)
		if err != nil {
			return err
		}
		return h.live2dStore(task, root, url, body)
	}
	// Single-flight per destination. Shared motion dirs (sibling costume models) mean
	// several concurrent workers can resolve to the same clip; without this they each
	// Stat "missing", re-fetch the same file and double-count Files/Bytes (a TOCTOU
	// between the Stat and the write). Only same-path workers serialize here, so this
	// can't stall unrelated downloads.
	unlock, err := live2dLockPath(ctx, dst)
	if err != nil {
		return err
	}
	defer unlock()
	if live2dCachedFileValid(dst, url) {
		return nil
	}
	body, err := h.live2dFetch(ctx, url)
	if err != nil {
		return err
	}
	return h.live2dStore(task, root, url, body)
}

// live2dStore writes an already-fetched body to live2dLocalPath(root, url) and
// bumps the task's file/byte counters.
func (h *Handler) live2dStore(task *model.Live2DSyncProgress, root, url string, body []byte) error {
	dst := live2dLocalPath(root, url)
	if dst == "" {
		return fmt.Errorf("url not mirrorable: %s", url)
	}
	if err := fsutil.WriteFileAtomic(dst, body, 0o644); err != nil {
		return err
	}
	task.Mu.Lock()
	task.Files++
	task.Bytes += int64(len(body))
	task.Mu.Unlock()
	return nil
}

// live2dPathMu holds one mutex per local mirror path, so the "check-exists →
// fetch → write → count" sequence can be single-flighted per destination without a
// coarse global lock. Sibling costume models share motion/BuildMotionData files, and
// the diff can hand several to concurrent workers at once.
type live2dPathLockEntry struct {
	token chan struct{}
	refs  int
}

var live2dPathLocks = struct {
	sync.Mutex
	entries map[string]*live2dPathLockEntry
}{entries: make(map[string]*live2dPathLockEntry)}

// live2dLockPath acquires a cancelable per-destination lock. Refcounts are
// changed under the map mutex and the entry is removed after the final holder or
// waiter leaves, preventing the old one-mutex-per-asset lifetime leak.
func live2dLockPath(ctx context.Context, dst string) (func(), error) {
	key := live2dRootKey(dst)
	live2dPathLocks.Lock()
	entry := live2dPathLocks.entries[key]
	if entry == nil {
		entry = &live2dPathLockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		live2dPathLocks.entries[key] = entry
	}
	entry.refs++
	live2dPathLocks.Unlock()

	select {
	case <-entry.token:
		var once sync.Once
		return func() {
			once.Do(func() {
				entry.token <- struct{}{}
				live2dReleasePathLock(key, entry)
			})
		}, nil
	case <-ctx.Done():
		live2dReleasePathLock(key, entry)
		return nil, ctx.Err()
	}
}

func live2dReleasePathLock(key string, entry *live2dPathLockEntry) {
	live2dPathLocks.Lock()
	defer live2dPathLocks.Unlock()
	entry.refs--
	if entry.refs == 0 && live2dPathLocks.entries[key] == entry {
		delete(live2dPathLocks.entries, key)
	}
}

// live2dFetchAndMirror returns url's body for parsing while mirroring it to the local
// store at most once. It prefers the on-disk copy (so an already-mirrored — even
// incomplete — model isn't re-fetched and re-counted every manual sync) and, on a
// miss, single-flights the fetch+write+count across concurrent workers sharing the
// destination. A write error is non-fatal: the body is still returned (the caller
// needs it to parse), and the model's on-disk completeness re-check is the real
// arbiter of success. Returns an error only when neither disk nor network had the body.
func (h *Handler) live2dFetchAndMirror(ctx context.Context, task *model.Live2DSyncProgress, root, url string) ([]byte, error) {
	dst := live2dLocalPath(root, url)
	if dst == "" {
		return h.live2dFetch(ctx, url)
	}
	unlock, err := live2dLockPath(ctx, dst)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if data, err := os.ReadFile(dst); err == nil && live2dAssetBodyValid(url, data) {
		return data, nil
	}
	body, err := h.live2dFetch(ctx, url)
	if err != nil {
		return nil, err
	}
	if serr := h.live2dStore(task, root, url, body); serr != nil {
		log.Printf("[live2d-sync] mirror %s: %v", url, serr)
	}
	return body, nil
}

// live2dSafeModelPath rejects a CDN-sourced modelPath that could escape the local
// mirror when joined with root (path traversal / absolute path). It mirrors the ".."
// guard live2dLocalPath applies to writes so the diff-stage ReadDir stays in-root.
func live2dSafeModelPath(p string) bool {
	if p == "" || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return !filepath.IsAbs(filepath.FromSlash(p))
}

func live2dSafeAssetName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\") && !strings.ContainsAny(name, "\r\n\x00")
}

func validateLive2DModelList(entries []live2dModelListEntry) ([]live2dModelRef, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("model list is empty")
	}
	seen := make(map[string]struct{}, len(entries))
	unique := make([]live2dModelRef, 0, len(entries))
	for i, entry := range entries {
		if strings.TrimSpace(entry.ModelName) == "" || !live2dSafeModelPath(entry.ModelPath) ||
			!live2dSafeAssetName(entry.ModelBase) || !live2dSafeAssetName(entry.ModelFile) ||
			!strings.HasSuffix(strings.ToLower(entry.ModelFile), ".model3.json") {
			return nil, fmt.Errorf("entry %d has invalid required fields", i)
		}
		if _, ok := seen[entry.ModelPath]; ok {
			continue
		}
		seen[entry.ModelPath] = struct{}{}
		unique = append(unique, live2dModelRef{entry.ModelPath, entry.ModelBase, entry.ModelFile})
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("model list has no usable entries")
	}
	return unique, nil
}

// live2dHostAllowed reports whether rawURL targets a known Live2D asset CDN.
// Parsing instead of string-prefix matching prevents hosts such as
// storage.sekai.best.attacker.invalid from passing the check.
func live2dHostAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	hostname := strings.ToLower(u.Hostname())
	for _, allowed := range live2dAllowedHosts {
		a, err := url.Parse(allowed)
		if err == nil && hostname == strings.ToLower(a.Hostname()) {
			return true
		}
	}
	return false
}

// live2dModelComplete reports whether dir holds a fully-mirrored model body: the
// build metadata, the model3, its moc3, EVERY texture the model3 references, and
// physics when declared. It reads the LOCAL model3 (no network) and mirrors the
// name/case rules live2dSyncModel applies when writing, so a model counts as
// complete only when every file playback needs is actually on disk. Any unreadable
// or missing piece → false, so a partially-deleted model gets repaired on next sync.
//
// Motion is intentionally excluded: it lives on sekai.best, is fetched best-effort,
// and a motionless model is still usable (matches live2dSyncModel's "never fatal").
func live2dModelComplete(dir, modelFile string) bool {
	bmdPath := filepath.Join(dir, "buildmodeldata.json")
	bmdBody, err := os.ReadFile(bmdPath)
	if err != nil || !live2dAssetBodyValid("buildmodeldata.json", bmdBody) {
		return false
	}
	var bmd live2dBuildModelData
	if json.Unmarshal(bmdBody, &bmd) != nil {
		return false
	}
	baseName := selectedLive2DBase(bmd, modelFile)
	if !live2dSafeAssetName(baseName) {
		return false
	}
	model3Name := baseName + ".model3"
	model3Path := filepath.Join(dir, model3Name)
	model3Body, err := os.ReadFile(model3Path)
	if err != nil || !live2dAssetBodyValid(model3Name, model3Body) {
		return false
	}
	var m3 live2dModel3
	if json.Unmarshal(model3Body, &m3) != nil || !live2dSafeAssetName(m3.FileReferences.Moc) ||
		!strings.HasSuffix(strings.ToLower(m3.FileReferences.Moc), ".moc3") {
		return false
	}
	validAsset := func(rel string) bool {
		if !live2dSafeModelPath(rel) {
			return false
		}
		return live2dCachedFileValid(filepath.Join(dir, filepath.FromSlash(rel)), rel)
	}
	if !validAsset(baseName + ".moc3") {
		return false
	}
	// textures — apply the same case-swap the downloader uses when the model3's Moc
	// base differs only in case from the model_list-derived baseName.
	refBase := strings.TrimSuffix(m3.FileReferences.Moc, ".moc3")
	for _, tex := range m3.FileReferences.Textures {
		if tex == "" || !live2dSafeModelPath(tex) {
			return false
		}
		realTex := tex
		if refBase != "" && refBase != baseName {
			realTex = strings.Replace(tex, refBase, baseName, 1)
		}
		if !validAsset(realTex) {
			return false
		}
	}
	// physics (only when the model3 declares it)
	if m3.FileReferences.Physics != "" && !validAsset(baseName+".physics3") {
		return false
	}
	return true
}

// live2dSyncFail marks a task as errored.
func (h *Handler) live2dSyncFail(task *model.Live2DSyncProgress, msg string) {
	log.Printf("[live2d-sync] error: %s", msg)
	task.Mu.Lock()
	if task.Status == "canceled" {
		task.Mu.Unlock()
		return
	}
	task.Status = "error"
	task.Error = msg
	task.Mu.Unlock()
}
