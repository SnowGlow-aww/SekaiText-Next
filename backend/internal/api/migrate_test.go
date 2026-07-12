package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// moveMerge 是迁移用户文稿的核心：rename 快路径、目录递归合并、同名文件跳过
// 不覆盖。任何回归都直接意味着用户译文丢失，锁死这些语义。
func TestMoveMerge(t *testing.T) {
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

	entries, err := os.ReadDir(old)
	if err != nil {
		t.Fatal(err)
	}
	moved, skipped := 0, 0
	skippedPaths := []string{}
	for _, e := range entries {
		if err := moveMerge(filepath.Join(old, e.Name()), filepath.Join(newd, e.Name()), e.Name(), &skippedPaths); err != nil {
			skipped++
		} else {
			moved++
		}
	}

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
	// 被跳过的旧文件仍留在原目录（用户可手动处理）
	if got := read(old, "活动剧情/208/【翻译】a.txt"); got != "old-a" {
		t.Errorf("skipped file lost from old dir: %q", got)
	}
	// 被跳过文件的相对路径要精确回报（相对旧/新根同一形式、正斜杠）——前端据此
	// 把该文档绑定留在旧目录，绝不改写到新根那个同名的陌生文件（数据安全）。
	if len(skippedPaths) != 1 || skippedPaths[0] != "活动剧情/208/【翻译】a.txt" {
		t.Errorf("skippedPaths=%v, want [活动剧情/208/【翻译】a.txt]", skippedPaths)
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
