package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/fsutil"
	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

type notifyingReader struct {
	io.Reader
	read chan struct{}
}

func (r *notifyingReader) Read(p []byte) (int, error) {
	select {
	case <-r.read:
	default:
		close(r.read)
	}
	return r.Reader.Read(p)
}

// The migration copy phase never removes source files, merges directories, and
// preserves conflicting destination files.
func TestCopySaveDirRetainsSource(t *testing.T) {
	old := t.TempDir()
	newd := t.TempDir()

	write := func(root string, rel string, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(root, rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	// 旧根：两个类型目录 + 深层文件
	write(old, "活动剧情/208/【翻译】a.txt", "old-a")
	write(old, "活动剧情/208/【校对】b.txt", "old-b")
	write(old, "主线剧情/ln/c.txt", "old-c")
	// 新根已有：与 活动剧情/208 部分重叠（a.txt 同名=必须保留新根版本，跳过旧版）
	write(newd, "活动剧情/208/【翻译】a.txt", "new-a-keep")

	result, err := copySaveDir(context.Background(), old, newd)
	if err != nil {
		t.Fatal(err)
	}
	// Copy/verify completes before any source cleanup.
	if got := read(old, "主线剧情/ln/c.txt"); got != "old-c" {
		t.Fatalf("copy phase removed source: %q", got)
	}
	moved, skipped := result.counts()

	// 主线剧情整树 rename 走通
	if got := read(newd, "主线剧情/ln/c.txt"); got != "old-c" {
		t.Errorf("main story not moved: %q", got)
	}
	// 合并目录里不冲突的文件搬过来
	if got := read(newd, "活动剧情/208/【校对】b.txt"); got != "old-b" {
		t.Errorf("merge missed file: %q", got)
	}
	// 同名文件绝不覆盖
	if got := read(newd, "活动剧情/208/【翻译】a.txt"); got != "new-a-keep" {
		t.Errorf("conflict file overwritten: %q", got)
	}
	// 冲突分支返回 error → 计入 skipped；无冲突分支 moved
	if moved != 1 || skipped != 1 {
		t.Errorf("moved=%d skipped=%d, want 1/1", moved, skipped)
	}
	// 所有旧文件都保留，避免「校验后、删除前」源文件被替换的竞态。
	for rel, want := range map[string]string{
		"活动剧情/208/【翻译】a.txt": "old-a",
		"活动剧情/208/【校对】b.txt": "old-b",
		"主线剧情/ln/c.txt":      "old-c",
	} {
		if got := read(old, rel); got != want {
			t.Errorf("source %s was not retained: %q", rel, got)
		}
	}
	// 被跳过文件的相对路径要精确回报（相对旧/新根同一形式、正斜杠）——前端据此
	// 把该文档绑定留在旧目录，绝不改写到新根那个同名的陌生文件（数据安全）。
	if len(result.skippedPaths) != 1 || result.skippedPaths[0] != "活动剧情/208/【翻译】a.txt" {
		t.Errorf("skippedPaths=%v, want [活动剧情/208/【翻译】a.txt]", result.skippedPaths)
	}
}

func TestCopySaveDirRefusesDestinationCreatedAfterPrecheck(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	src := filepath.Join(srcRoot, "draft.txt")
	dst := filepath.Join(dstRoot, "draft.txt")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := newSaveDirCopyResult()
	err := copySaveDirEntryWithCopy(context.Background(), src, dst, "draft.txt", "draft.txt", result,
		func(ctx context.Context, src, dst string, perm os.FileMode) error {
			if err := os.WriteFile(dst, []byte("raced-in"), 0o644); err != nil {
				t.Fatal(err)
			}
			return fsutil.CopyFileNoReplaceAtomic(ctx, src, dst, perm)
		})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := os.ReadFile(dst)
	if readErr != nil || string(got) != "raced-in" {
		t.Fatalf("raced-in destination changed: %q, %v", got, readErr)
	}
	if len(result.skippedPaths) != 1 || result.skippedPaths[0] != "draft.txt" {
		t.Fatalf("skipped paths = %v, want [draft.txt]", result.skippedPaths)
	}
}

func newMigrationHandler(t *testing.T, oldDir string) *Handler {
	t.Helper()
	h := &Handler{cfg: &config.AppConfig{CatalogDir: t.TempDir()}}
	settings := model.DefaultSettings()
	settings.SaveBaseDir = oldDir
	if err := h.saveSettings(settings); err != nil {
		t.Fatal(err)
	}
	return h
}

func callMigrateSaveDir(t *testing.T, h *Handler, newDir string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/settings/migrate-save-dir", strings.NewReader(`{"newDir":`+strconv.Quote(newDir)+`}`))
	rec := httptest.NewRecorder()
	h.MigrateSaveDir(rec, req)
	return rec
}

func TestMigrateSaveDirTreatsMissingSourceAsEmpty(t *testing.T) {
	base := t.TempDir()
	oldDir := filepath.Join(base, "never-created")
	newDir := filepath.Join(oldDir, "new-location")
	h := newMigrationHandler(t, oldDir)
	rec := callMigrateSaveDir(t, h, newDir)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	settings, err := h.loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(newDir)
	if settings.SaveBaseDir != want {
		t.Fatalf("SaveBaseDir=%q, want %q", settings.SaveBaseDir, want)
	}
}

func TestMigrateSaveDirSamePhysicalDirectoryIsNoop(t *testing.T) {
	oldDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(oldDir, "draft.txt"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Put the alias inside the old tree: lexical containment must not win over
	// physical identity and turn this no-op into an error.
	alias := filepath.Join(oldDir, "alias")
	if err := os.Symlink(oldDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	h := newMigrationHandler(t, oldDir)
	rec := callMigrateSaveDir(t, h, alias)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(oldDir, "draft.txt")); err != nil || string(got) != "safe" {
		t.Fatalf("source changed during no-op: %q, %v", got, err)
	}
}

func TestPutSettingsSerializesWithMigrationLock(t *testing.T) {
	h := &Handler{cfg: &config.AppConfig{CatalogDir: t.TempDir()}}
	h.saveDirMu.Lock()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("PUT", "/settings", strings.NewReader(`{"fontSize":20}`))
		h.PutSettings(httptest.NewRecorder(), req)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("settings write did not wait for migration lock")
	case <-time.After(50 * time.Millisecond):
	}
	h.saveDirMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("settings write remained blocked after migration lock release")
	}
}

func TestQueuedSaveCannotWriteOldPathAfterMigrationGenerationAdvances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old", "draft.txt")
	h := &Handler{cfg: &config.AppConfig{CatalogDir: t.TempDir()}, editor: service.NewEditorService()}
	body := &notifyingReader{
		Reader: strings.NewReader(`{"filePath":` + strconv.Quote(path) + `,"talks":[],"saveN":false}`),
		read:   make(chan struct{}),
	}
	h.saveDirMu.Lock()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.TranslationSave(rec, httptest.NewRequest("POST", "/translation/save", body))
		close(done)
	}()
	select {
	case <-body.read:
	case <-time.After(time.Second):
		t.Fatal("save request did not capture/decode its old path")
	}
	h.saveDirGeneration.Add(1)
	h.saveDirMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale save remained blocked")
	}
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale save wrote old path: %v", err)
	}
}

func TestQueuedEnsureDirCannotRecreateOldPathAfterMigrationGenerationAdvances(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "old", "draft.txt")
	h := &Handler{cfg: &config.AppConfig{CatalogDir: t.TempDir()}}
	body := &notifyingReader{
		Reader: strings.NewReader(`{"path":` + strconv.Quote(path) + `}`),
		read:   make(chan struct{}),
	}
	h.saveDirMu.Lock()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.EnsureDir(rec, httptest.NewRequest("POST", "/ensure-dir", body))
		close(done)
	}()
	select {
	case <-body.read:
	case <-time.After(time.Second):
		t.Fatal("ensure-dir request did not capture its old generation")
	}
	h.saveDirGeneration.Add(1)
	h.saveDirMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale ensure-dir remained blocked")
	}
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("stale ensure-dir recreated old directory: %v", err)
	}
}

func TestQueuedRenameCannotMutateOldPathAfterMigrationGenerationAdvances(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old", "draft.txt")
	newPath := filepath.Join(root, "old", "renamed.txt")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: &config.AppConfig{CatalogDir: t.TempDir()}}
	body := &notifyingReader{
		Reader: strings.NewReader(`{"oldPath":` + strconv.Quote(oldPath) + `,"newPath":` + strconv.Quote(newPath) + `}`),
		read:   make(chan struct{}),
	}
	h.saveDirMu.Lock()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.RenameFile(rec, httptest.NewRequest("POST", "/rename-file", body))
		close(done)
	}()
	select {
	case <-body.read:
	case <-time.After(time.Second):
		t.Fatal("rename request did not capture its old generation")
	}
	h.saveDirGeneration.Add(1)
	h.saveDirMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale rename remained blocked")
	}
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, err := os.ReadFile(oldPath); err != nil || string(got) != "safe" {
		t.Fatalf("stale rename changed source: %q, %v", got, err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("stale rename created target: %v", err)
	}
}

func TestMigrateSaveDirAdvancesGenerationAfterCommittedSettingsWarning(t *testing.T) {
	oldDir := t.TempDir()
	newDir := filepath.Join(t.TempDir(), "new")
	h := newMigrationHandler(t, oldDir)
	wantErr := errors.New("directory sync failed")
	h.writeSettingsFile = func(path string, data []byte, perm os.FileMode) error {
		if err := os.WriteFile(path, data, perm); err != nil {
			return err
		}
		return &fsutil.PostCommitError{Err: wantErr}
	}

	before := h.saveDirGeneration.Load()
	rec := callMigrateSaveDir(t, h, newDir)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.saveDirGeneration.Load() != before+1 {
		t.Fatalf("save directory generation did not advance after committed settings write")
	}
	settings, err := h.loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.Abs(newDir)
	if settings.SaveBaseDir != wantDir {
		t.Fatalf("committed SaveBaseDir=%q, want %q", settings.SaveBaseDir, wantDir)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if warning, _ := response["warning"].(string); !strings.Contains(warning, wantErr.Error()) {
		t.Fatalf("response warning = %q", warning)
	}
}

func TestQueuedSaveRejectsOldPathAfterCommittedMigrationWarning(t *testing.T) {
	oldDir := t.TempDir()
	newDir := filepath.Join(t.TempDir(), "new")
	oldPath := filepath.Join(oldDir, "queued.txt")
	h := newMigrationHandler(t, oldDir)
	h.editor = service.NewEditorService()
	settingsWriteStarted := make(chan struct{})
	releaseSettingsWrite := make(chan struct{})
	wantErr := errors.New("directory sync failed")
	h.writeSettingsFile = func(path string, data []byte, perm os.FileMode) error {
		if err := os.WriteFile(path, data, perm); err != nil {
			return err
		}
		close(settingsWriteStarted)
		<-releaseSettingsWrite
		return &fsutil.PostCommitError{Err: wantErr}
	}

	migrationRec := httptest.NewRecorder()
	migrationDone := make(chan struct{})
	go func() {
		req := httptest.NewRequest("POST", "/settings/migrate-save-dir", strings.NewReader(`{"newDir":`+strconv.Quote(newDir)+`}`))
		h.MigrateSaveDir(migrationRec, req)
		close(migrationDone)
	}()
	select {
	case <-settingsWriteStarted:
	case <-time.After(time.Second):
		t.Fatal("migration did not reach committed settings write")
	}

	body := &notifyingReader{
		Reader: strings.NewReader(`{"filePath":` + strconv.Quote(oldPath) + `,"talks":[],"saveN":false}`),
		read:   make(chan struct{}),
	}
	saveRec := httptest.NewRecorder()
	saveDone := make(chan struct{})
	go func() {
		h.TranslationSave(saveRec, httptest.NewRequest("POST", "/translation/save", body))
		close(saveDone)
	}()
	select {
	case <-body.read:
	case <-time.After(time.Second):
		t.Fatal("queued save did not capture its old generation")
	}
	close(releaseSettingsWrite)
	select {
	case <-migrationDone:
	case <-time.After(time.Second):
		t.Fatal("migration remained blocked")
	}
	select {
	case <-saveDone:
	case <-time.After(time.Second):
		t.Fatal("queued save remained blocked")
	}
	if migrationRec.Code != 200 || !strings.Contains(migrationRec.Body.String(), wantErr.Error()) {
		t.Fatalf("migration status=%d body=%s", migrationRec.Code, migrationRec.Body.String())
	}
	if saveRec.Code != 409 {
		t.Fatalf("save status=%d body=%s", saveRec.Code, saveRec.Body.String())
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("queued save recreated the old path: %v", err)
	}
}

func TestMigrateSaveDirRejectsMalformedSettingsWithoutWritingDefaults(t *testing.T) {
	catalogDir := t.TempDir()
	h := &Handler{cfg: &config.AppConfig{CatalogDir: catalogDir}}
	settingsPath := filepath.Join(catalogDir, "settings.json")
	malformed := []byte(`{"saveBaseDir":`)
	if err := os.WriteFile(settingsPath, malformed, 0o644); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(t.TempDir(), "new")
	rec := callMigrateSaveDir(t, h, newDir)
	if rec.Code != 500 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil || string(got) != string(malformed) {
		t.Fatalf("malformed settings were replaced: %q, %v", got, err)
	}
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("migration performed filesystem work with malformed settings: %v", err)
	}
}

func TestMigrateSaveDirRejectsUnreadableSettingsPath(t *testing.T) {
	catalogDir := t.TempDir()
	h := &Handler{cfg: &config.AppConfig{CatalogDir: catalogDir}}
	if err := os.Mkdir(filepath.Join(catalogDir, "settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(t.TempDir(), "new")
	rec := callMigrateSaveDir(t, h, newDir)
	if rec.Code != 500 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("migration proceeded with unreadable settings: %v", err)
	}
}

// RenameFile 支撑「标题译文/模式标签变了就地改名」：目标已存在必须 409（前端
// 收到即回落原路径写入，绝不覆盖别的文稿），成功后旧路径消失新路径接管。
func TestRenameFileHandler(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{}
	call := func(oldPath, newPath string) int {
		body := `{"oldPath":` + strconv.Quote(oldPath) + `,"newPath":` + strconv.Quote(newPath) + `}`
		req := httptest.NewRequest("POST", "/translation/rename-file", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.RenameFile(rec, req)
		return rec.Code
	}

	src := filepath.Join(dir, "【翻译】208-05 まさかの到着地.txt")
	dst := filepath.Join(dir, "【翻译】208-05 意料之外的目的地.txt")
	if err := os.WriteFile(src, []byte("译文内容"), 0644); err != nil {
		t.Fatal(err)
	}

	if code := call(src, dst); code != 200 {
		t.Fatalf("rename: got %d", code)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("old path should be gone")
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "译文内容" {
		t.Fatalf("content not carried over: %v %q", err, b)
	}

	// 同路径 no-op
	if code := call(dst, dst); code != 200 {
		t.Fatalf("same-path: got %d", code)
	}
	// 目标已存在 → 409，双方文件都原样保留
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("别人的"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := call(dst, other); code != 409 {
		t.Fatalf("conflict: got %d, want 409", code)
	}
	if b, _ := os.ReadFile(other); string(b) != "别人的" {
		t.Fatal("conflict target was overwritten")
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("source should survive a conflict")
	}

	// 跨目录归位（索引标签修正 208 → 208 褪せない今を、彩って）：目标目录自动
	// 创建，空的旧目录顺手删除；非空旧目录必须保留
	oldDir := filepath.Join(dir, "208")
	newDir := filepath.Join(dir, "208 褪せない今を、彩って")
	moved := filepath.Join(oldDir, "【翻译】208-05 x.txt")
	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved, []byte("v"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := call(moved, filepath.Join(newDir, "【翻译】208-05 x.txt")); code != 200 {
		t.Fatalf("cross-dir rename: got %d", code)
	}
	if _, err := os.Stat(filepath.Join(newDir, "【翻译】208-05 x.txt")); err != nil {
		t.Fatal("moved file missing")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("empty old dir should be removed")
	}
}

func TestRenameFileRefusesDestinationCreatedAfterPrecheck(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	h.moveFileNoReplace = func(oldPath, newPath string) error {
		if err := os.WriteFile(newPath, []byte("raced-in"), 0o644); err != nil {
			t.Fatal(err)
		}
		return fsutil.MoveFileNoReplace(oldPath, newPath)
	}
	body := `{"oldPath":` + strconv.Quote(src) + `,"newPath":` + strconv.Quote(dst) + `}`
	rec := httptest.NewRecorder()
	h.RenameFile(rec, httptest.NewRequest("POST", "/translation/rename-file", strings.NewReader(body)))
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "raced-in" {
		t.Fatalf("raced-in destination changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "source" {
		t.Fatalf("source changed after conflict: %q, %v", got, err)
	}
}
