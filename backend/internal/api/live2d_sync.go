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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	live2dSekaiBest = "https://storage.sekai.best/sekai-live2d-assets"
	live2dExmeaning = "https://sakimizuki.accr.cc/sekai-jp-assets"
)

// live2dModelListEntry is one record of model_list.json (sekai.best).
type live2dModelListEntry struct {
	ModelName string `json:"modelName"`
	ModelBase string `json:"modelBase"`
	ModelPath string `json:"modelPath"`
	ModelFile string `json:"modelFile"`
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
	taskID := strconv.FormatInt(time.Now().UnixNano(), 36)
	task := &model.Live2DSyncProgress{TaskID: taskID, Status: "checking"}
	h.live2dSyncTasks.Store(taskID, task)
	go h.runLive2DSync(task, concurrency)
	writeJSON(w, http.StatusOK, map[string]string{"taskId": taskID})
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
	val, ok := h.live2dSyncTasks.Load(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	task := val.(*model.Live2DSyncProgress)

	// Snapshot the mutable fields under the lock into a mutex-free value so
	// encoding happens off-lock and go vet doesn't flag copying a sync.Mutex.
	task.Mu.Lock()
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
		TaskID:       task.TaskID,
		Status:       task.Status,
		Total:        task.Total,
		Current:      task.Current,
		CurrentModel: task.CurrentModel,
		Files:        task.Files,
		Bytes:        task.Bytes,
		Failed:       task.Failed,
		Error:        task.Error,
	}
	task.Mu.Unlock()

	writeJSON(w, http.StatusOK, snap)
	// NOTE: do NOT delete terminal tasks here (see doc comment above).
}

// runLive2DSync is the background worker. It never panics the goroutine: any
// panic is recovered and surfaced as an error status.
func (h *Handler) runLive2DSync(task *model.Live2DSyncProgress, concurrency int) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[live2d-sync] panic: %v", rec)
			task.Mu.Lock()
			task.Status = "error"
			if task.Error == "" {
				task.Error = fmt.Sprintf("internal error: %v", rec)
			}
			task.Mu.Unlock()
		}
	}()

	root := h.cfg.Live2DLocalDir

	// 1. Fetch the upstream model index.
	listURL := live2dSekaiBest + "/live2d/model_list.json"
	listBody, err := h.live2dFetch(listURL)
	if err != nil {
		h.live2dSyncFail(task, "fetch model_list.json: "+err.Error())
		return
	}
	var entries []live2dModelListEntry
	if err := json.Unmarshal(listBody, &entries); err != nil {
		h.live2dSyncFail(task, "parse model_list.json: "+err.Error())
		return
	}

	// 2. Dedupe by modelPath (preserve order), keeping a representative
	//    modelBase for motion-base derivation.
	type modelRef struct{ modelPath, modelBase, modelFile string }
	seen := map[string]bool{}
	var unique []modelRef
	for _, e := range entries {
		if e.ModelPath == "" || seen[e.ModelPath] {
			continue
		}
		// modelPath comes from the upstream model_list.json and is later joined with
		// root for the on-disk completeness ReadDir (and to derive write paths). A
		// ".." / absolute segment would let filepath.Join escape the mirror and
		// ReadDir an arbitrary directory. Writes are already blocked by
		// live2dLocalPath; reject the traversal here so the diff-stage reads stay
		// in-root too. Mark it seen so a repeat doesn't re-log.
		seen[e.ModelPath] = true
		if !live2dSafeModelPath(e.ModelPath) {
			log.Printf("[live2d-sync] skip unsafe modelPath %q", e.ModelPath)
			continue
		}
		unique = append(unique, modelRef{e.ModelPath, e.ModelBase, e.ModelFile})
	}

	// 3. Diff vs the local mirror: a model needs (re)downloading unless every body
	//    file playback needs is present on disk — build metadata, model3, moc3, all
	//    referenced textures, and physics. Checking only the moc3 (the old behaviour)
	//    missed a deleted/partial texture or physics — the model looked complete but
	//    wasn't, so the sync reported "done" without restoring the file. See
	//    live2dModelComplete.
	var missing []modelRef
	for _, m := range unique {
		dir := filepath.Join(root, "model", filepath.FromSlash(m.modelPath))
		if !live2dModelComplete(dir) {
			missing = append(missing, m)
		}
	}

	task.Mu.Lock()
	task.Status = "downloading"
	task.Total = len(missing)
	task.Mu.Unlock()

	// 4. Download each missing model. Per the spec, a body failure
	//    (buildmodeldata/model3/moc3) skips just that model; textures/motions are
	//    individually resilient. Current advances per processed model regardless,
	//    so the progress bar always completes.
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, m := range missing {
		wg.Add(1)
		sem <- struct{}{}
		go func(m modelRef) {
			defer wg.Done()
			defer func() { <-sem }()
			// Advance progress in a defer so a panicked model still counts: it runs
			// during the panic unwind (and after the recover below), whereas the old
			// inline increment was skipped on panic — leaving Current<Total while the
			// task was still forced to "done". A model that didn't complete (error,
			// incomplete on disk, or panic) counts as Failed.
			completed := false
			defer func() {
				task.Mu.Lock()
				task.Current++
				if !completed {
					task.Failed++
				}
				task.Mu.Unlock()
			}()
			// Per-model panic guard so one bad model can't take down its worker.
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[live2d-sync] model %s panic: %v", m.modelPath, rec)
				}
			}()

			task.Mu.Lock()
			task.CurrentModel = m.modelPath
			task.Mu.Unlock()

			err := h.live2dSyncModel(task, m.modelPath, m.modelBase, m.modelFile)
			// nil only means the fatal body files loaded; a best-effort texture/physics
			// missing on BOTH mirrors still leaves the model incomplete. Re-check the
			// on-disk set so the tally is honest instead of always reporting success.
			dir := filepath.Join(root, "model", filepath.FromSlash(m.modelPath))
			ok := err == nil && live2dModelComplete(dir)
			if err != nil {
				log.Printf("[live2d-sync] skip model %s: %v", m.modelPath, err)
			} else if !ok {
				log.Printf("[live2d-sync] model %s incomplete after download (asset missing upstream)", m.modelPath)
			}
			completed = ok
		}(m)
	}
	wg.Wait()

	// 5. Refresh the local model_list.json index so the mirror reflects upstream.
	if dst := live2dLocalPath(root, listURL); dst != "" {
		if err := live2dWriteFile(dst, listBody); err != nil {
			log.Printf("[live2d-sync] refresh model_list.json: %v", err)
		}
	}

	task.Mu.Lock()
	task.Status = "done"
	if task.Failed > 0 {
		task.Error = fmt.Sprintf("%d/%d 个模型未能完整下载(个别贴图/资源上游缺失)", task.Failed, task.Total)
		if task.Failed >= task.Total {
			task.Status = "error" // nothing usable downloaded — don't report success
		}
	}
	task.Mu.Unlock()
}

// live2dSyncModel downloads one model's body (exmeaning) + motion data
// (sekai.best). Returning an error means the model body is unusable and the
// model is skipped; texture/motion/physics failures are logged and tolerated.
func (h *Handler) live2dSyncModel(task *model.Live2DSyncProgress, modelPath, modelBase, modelFile string) error {
	root := h.cfg.Live2DLocalDir
	bodyDir := live2dExmeaning + "/live2d/model/" + modelPath + "/"

	// --- body: buildmodeldata.json (mirrored; its Moc3FileName is a fallback) ---
	// live2dFetchAndMirror prefers the on-disk copy, so an already-mirrored model
	// (even one left incomplete by an upstream-deleted texture) doesn't re-fetch and
	// re-count this JSON on every manual sync.
	bmdURL := bodyDir + "buildmodeldata.json"
	bmdBody, err := h.live2dFetchAndMirror(task, root, bmdURL)
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
	mfBase := strings.TrimSuffix(modelFile, ".model3.json")
	mfBase = strings.TrimSuffix(mfBase, ".model3")
	mocBase := strings.TrimSuffix(bmd.Moc3FileName, ".moc3.bytes")
	mocBase = strings.TrimSuffix(mocBase, ".moc3")
	baseName := mocBase
	if mocBase == "" {
		baseName = mfBase
	} else if mfBase != "" && strings.EqualFold(mfBase, mocBase) {
		baseName = mfBase
	}
	if baseName == "" {
		return fmt.Errorf("cannot determine model3 base name")
	}

	// --- body: {baseName}.model3 (no .json ext) -> FileReferences ---
	model3URL := bodyDir + baseName + ".model3"
	model3Body, err := h.live2dFetchAndMirror(task, root, model3URL)
	if err != nil {
		return fmt.Errorf("model3: %w", err)
	}
	var m3 live2dModel3
	if err := json.Unmarshal(model3Body, &m3); err != nil {
		return fmt.Errorf("parse model3: %w", err)
	}

	// --- body: moc3 / textures / physics ---
	// The model3's FileReferences can declare a different CASE than the files that
	// actually exist (some April2026 mains: "April" inside the model3, but the files
	// are "april"). baseName (from model_list modelFile) is the authoritative case,
	// so rebuild the moc/physics names from it and swap the texture path's prefix.
	refBase := strings.TrimSuffix(m3.FileReferences.Moc, ".moc3")

	// moc3 (the file the delta check looks for) — fatal on failure.
	if err := h.live2dDownload(task, root, bodyDir+baseName+".moc3"); err != nil {
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
		if err := h.live2dDownload(task, root, bodyDir+realTex); err != nil {
			log.Printf("[live2d-sync] %s: skip texture %s: %v", modelPath, realTex, err)
		}
	}

	// physics — the real CDN file drops the .json suffix (model3 declares .physics3.json).
	if m3.FileReferences.Physics != "" {
		if err := h.live2dDownload(task, root, bodyDir+baseName+".physics3"); err != nil {
			log.Printf("[live2d-sync] %s: skip physics: %v", modelPath, err)
		}
	}

	// --- motion data (sekai.best) — never fatal; a model with no motions is
	//     still usable (it just won't animate) ---
	h.live2dSyncMotion(task, root, modelPath, modelBase)

	return nil
}

// live2dSyncMotion downloads the model's motion + facial clips from sekai.best.
// motionDir = modelPath minus its last segment; motionBase starts at modelBase
// and is shortened one "_segment" at a time until BuildMotionData.json returns
// 200 (or only one segment remains, in which case the model is left motionless).
func (h *Handler) live2dSyncMotion(task *model.Live2DSyncProgress, root, modelPath, modelBase string) {
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
		if body, err := h.live2dFetchAndMirror(task, root, url); err == nil {
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
		if err := h.live2dDownload(task, root, motionRoot+"motion/"+n+".motion3.json"); err != nil {
			log.Printf("[live2d-sync] %s: skip motion %s: %v", modelPath, n, err)
		}
	}
	for _, n := range ml.Expressions {
		if n == "" {
			continue
		}
		if err := h.live2dDownload(task, root, motionRoot+"facial/"+n+".motion3.json"); err != nil {
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
func (h *Handler) live2dFetch(url string) ([]byte, error) {
	body, err := h.live2dFetchOnce(url)
	if err == nil {
		return body, nil
	}
	if alt := live2dSekaiFallback(url); alt != "" {
		if body2, err2 := h.live2dFetchOnce(alt); err2 == nil {
			return body2, nil
		}
	}
	return nil, err
}

// live2dSyncHTTP is the HTTP client the sync uses for CDN fetches. It mirrors the
// shared downloader's TLS posture (some asset CDNs serve certs Go's macOS verifier
// wrongly rejects — see service.NewDownloader) but ADDS a redirect guard: the host
// allowlist in live2dFetchOnce only vets the INITIAL URL, so without re-checking each
// hop a compromised/misconfigured CDN could 3xx the fetch to an internal address
// (169.254.169.254, 127.0.0.1, …) — a classic SSRF. Every redirect target's host is
// re-run through live2dHostAllowed.
var live2dSyncHTTP = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
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

// live2dFetchOnce GETs a single allowed URL through a redirect-guarded client (which
// also skips the macOS TLS verifier quirk) and returns its body (200 only). The host
// is restricted to the known Live2D asset CDNs (anti-SSRF), reusing live2dAllowedHosts,
// and live2dSyncHTTP re-checks that guard on every redirect hop.
func (h *Handler) live2dFetchOnce(url string) ([]byte, error) {
	if !live2dHostAllowed(url) {
		return nil, fmt.Errorf("url host not allowed: %s", url)
	}
	resp, err := live2dSyncHTTP.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
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
func (h *Handler) live2dDownload(task *model.Live2DSyncProgress, root, url string) error {
	dst := live2dLocalPath(root, url)
	if dst == "" {
		// Not mirrorable — fetch anyway so live2dStore surfaces the same error as before.
		body, err := h.live2dFetch(url)
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
	unlock := live2dLockPath(dst)
	defer unlock()
	if info, err := os.Stat(dst); err == nil && info.Size() > 0 {
		return nil
	}
	body, err := h.live2dFetch(url)
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
	if err := live2dWriteFile(dst, body); err != nil {
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
var live2dPathMu sync.Map // dst string -> *sync.Mutex

// live2dLockPath locks the mutex guarding dst and returns its unlock func. Distinct
// destinations never block each other; only workers racing on the SAME file
// serialize (which is exactly the duplicate we want to collapse).
func live2dLockPath(dst string) func() {
	mi, _ := live2dPathMu.LoadOrStore(dst, &sync.Mutex{})
	mu := mi.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// live2dFetchAndMirror returns url's body for parsing while mirroring it to the local
// store at most once. It prefers the on-disk copy (so an already-mirrored — even
// incomplete — model isn't re-fetched and re-counted every manual sync) and, on a
// miss, single-flights the fetch+write+count across concurrent workers sharing the
// destination. A write error is non-fatal: the body is still returned (the caller
// needs it to parse), and the model's on-disk completeness re-check is the real
// arbiter of success. Returns an error only when neither disk nor network had the body.
func (h *Handler) live2dFetchAndMirror(task *model.Live2DSyncProgress, root, url string) ([]byte, error) {
	dst := live2dLocalPath(root, url)
	if dst == "" {
		return h.live2dFetch(url)
	}
	unlock := live2dLockPath(dst)
	defer unlock()
	if data, err := os.ReadFile(dst); err == nil && len(data) > 0 {
		return data, nil
	}
	body, err := h.live2dFetch(url)
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
	if p == "" || strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
		return false
	}
	return !filepath.IsAbs(filepath.FromSlash(p))
}

// live2dWriteFile creates the parent dir and writes the file.
func live2dWriteFile(dst string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0644)
}

// live2dHostAllowed reports whether url targets a known Live2D asset CDN.
func live2dHostAllowed(url string) bool {
	for _, host := range live2dAllowedHosts {
		if strings.HasPrefix(url, host) {
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
func live2dModelComplete(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var model3Name string
	hasBuildData := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".model3") {
			model3Name = e.Name()
		} else if e.Name() == "buildmodeldata.json" {
			hasBuildData = true
		}
	}
	if model3Name == "" || !hasBuildData {
		return false
	}
	baseName := strings.TrimSuffix(model3Name, ".model3")
	data, err := os.ReadFile(filepath.Join(dir, model3Name))
	if err != nil {
		return false
	}
	var m3 live2dModel3
	if err := json.Unmarshal(data, &m3); err != nil {
		return false
	}
	exists := func(rel string) bool {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err == nil && !info.IsDir() && info.Size() > 0
	}
	// moc3
	if !exists(baseName + ".moc3") {
		return false
	}
	// textures — apply the same case-swap the downloader uses when the model3's Moc
	// base differs only in case from the model_list-derived baseName.
	refBase := strings.TrimSuffix(m3.FileReferences.Moc, ".moc3")
	for _, tex := range m3.FileReferences.Textures {
		if tex == "" {
			continue
		}
		realTex := tex
		if refBase != "" && refBase != baseName {
			realTex = strings.Replace(tex, refBase, baseName, 1)
		}
		if !exists(realTex) {
			return false
		}
	}
	// physics (only when the model3 declares it)
	if m3.FileReferences.Physics != "" && !exists(baseName+".physics3") {
		return false
	}
	return true
}

// live2dSyncFail marks a task as errored.
func (h *Handler) live2dSyncFail(task *model.Live2DSyncProgress, msg string) {
	log.Printf("[live2d-sync] error: %s", msg)
	task.Mu.Lock()
	task.Status = "error"
	task.Error = msg
	task.Mu.Unlock()
}
