package api

import (
	"os"
	"path/filepath"
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
	for _, e := range entries {
		if err := moveMerge(filepath.Join(old, e.Name()), filepath.Join(newd, e.Name())); err != nil {
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
}
