package service

import (
	"testing"

	"sekaitext/backend/internal/model"
)

// sampleDictJSON 覆盖归一化的各种分支：
//   - 1.1 单假名 kana(あ) 剔除、单汉字 kanji(亜) 保留
//   - 1.2 / 1.3 同一 key "ああ"（同形多义词），两条都必须保留
//   - 2.1 多表记 kanji "[相打ち・相討ち]" 按 ・ 拆分
//   - 3.1 单汉字 "[愛]" 保留 + 多字假名
//   - 5.1 全角 "[ＡＢＣ]"（供折叠回退 Lookup 命中）
//   - 6.1 / 6.2 供搜索排序：key 精确 > text 含
//   - 7.1 / 7.2 供搜索排序：key 前缀 > key 含
//   - 缺 key 字段的条目必须跳过
const sampleDictJSON = `[
  {"page":1,"index":1,"id":"1.1","text":"亜の意味","kana":"あ","accent":"◎","kanji":"[亜]","key":"あ[亜]"},
  {"page":1,"index":2,"id":"1.2","text":"意味その一","kana":"ああ","accent":"◎","kanji":null,"key":"ああ"},
  {"page":1,"index":3,"id":"1.3","text":"意味その二","kana":"ああ","accent":"①","kanji":null,"key":"ああ"},
  {"page":2,"index":1,"id":"2.1","text":"相打ちの意味","kana":"あいうち","accent":"","kanji":"[相打ち・相討ち]","key":"あいうち[相打ち]"},
  {"page":3,"index":1,"id":"3.1","text":"愛の意味","kana":"あい","accent":"◎","kanji":"[愛]","key":"あい[愛]"},
  {"page":5,"index":1,"id":"5.1","text":"ラテン文字","kana":"ラテン","accent":"","kanji":"[ＡＢＣ]","key":"ラテンＡＢＣ"},
  {"page":6,"index":1,"id":"6.1","text":"unrelated content","kana":"てすと","accent":"","kanji":null,"key":"テスト"},
  {"page":6,"index":2,"id":"6.2","text":"ここにテストという語","kana":"べつ","accent":"","kanji":null,"key":"べつ"},
  {"page":7,"index":1,"id":"7.1","text":"ice cream","kana":"あいす","accent":"","kanji":null,"key":"アイス"},
  {"page":7,"index":2,"id":"7.2","text":"contains アイ inside","kana":"わあい","accent":"","kanji":null,"key":"ワアイ"},
  {"page":8,"index":1,"id":"8.1","text":"no key -> skipped","kana":"xxx","accent":"","kanji":null}
]`

// newImportedStore 建一个临时目录的 DictStore 并导入样例字典，返回 store。
func newImportedStore(t *testing.T) (*DictStore, model.DictInfo) {
	t.Helper()
	s := NewDictStore(t.TempDir())
	info, err := s.Import("日汉双解学习词典.json", []byte(sampleDictJSON))
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	return s, info
}

// entryByID 从某字典分类里按 id 找条目（测试辅助，直接读内存）。
func (s *DictStore) entryByID(name, id string) (model.DictEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.dicts {
		if s.dicts[i].Name != name {
			continue
		}
		for _, e := range s.dicts[i].Entries {
			if e.ID == id {
				return e, true
			}
		}
	}
	return model.DictEntry{}, false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestImportNormalization(t *testing.T) {
	s, info := newImportedStore(t)

	// 上传名去 .json 扩展 + 跳过 1 条缺 key，共 10 条有效。
	if info.Name != "日汉双解学习词典" {
		t.Errorf("info.Name = %q, want 日汉双解学习词典", info.Name)
	}
	if info.Count != 10 {
		t.Errorf("info.Count = %d, want 10", info.Count)
	}

	cases := []struct {
		id   string
		want []string
	}{
		{"1.1", []string{"亜"}},                  // 单假名 あ 剔除，单汉字 亜 保留
		{"1.2", []string{"ああ"}},                 // null kanji，只留假名
		{"2.1", []string{"あいうち", "相打ち", "相討ち"}}, // 多表记按 ・ 拆
		{"3.1", []string{"あい", "愛"}},            // 单汉字 愛 保留
	}
	for _, c := range cases {
		e, ok := s.entryByID("日汉双解学习词典", c.id)
		if !ok {
			t.Fatalf("entry %s not found", c.id)
		}
		if !equalStrings(e.Surfaces, c.want) {
			t.Errorf("entry %s surfaces = %v, want %v", c.id, e.Surfaces, c.want)
		}
	}
}

func TestImportRejectsNonArray(t *testing.T) {
	s := NewDictStore(t.TempDir())
	if _, err := s.Import("bad.json", []byte(`{"not":"an array"}`)); err == nil {
		t.Error("Import of non-array JSON should error")
	}
	// 全部无效（都缺 key）也应报错。
	if _, err := s.Import("empty.json", []byte(`[{"page":1,"index":1,"kana":"x"}]`)); err == nil {
		t.Error("Import with no valid entries should error")
	}
}

func TestLookup(t *testing.T) {
	s, _ := newImportedStore(t)

	// 同形多义词：Lookup("ああ") 两条都在。
	hits := s.Lookup("ああ")
	if len(hits) != 2 {
		t.Fatalf("Lookup(ああ) = %d hits, want 2", len(hits))
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.Entry.ID] = true
		if h.DictName != "日汉双解学习词典" {
			t.Errorf("hit DictName = %q, want 日汉双解学习词典", h.DictName)
		}
	}
	if !ids["1.2"] || !ids["1.3"] {
		t.Errorf("Lookup(ああ) ids = %v, want both 1.2 and 1.3", ids)
	}

	// 单汉字表面形。
	if hits := s.Lookup("愛"); len(hits) != 1 || hits[0].Entry.ID != "3.1" {
		t.Errorf("Lookup(愛) = %+v, want single hit 3.1", hits)
	}
	if hits := s.Lookup("亜"); len(hits) != 1 || hits[0].Entry.ID != "1.1" {
		t.Errorf("Lookup(亜) = %+v, want single hit 1.1", hits)
	}

	// 多表记两个写法都能命中同一条。
	if hits := s.Lookup("相討ち"); len(hits) != 1 || hits[0].Entry.ID != "2.1" {
		t.Errorf("Lookup(相討ち) = %+v, want single hit 2.1", hits)
	}

	// 折叠回退：全角 ＡＢＣ 存进索引，用半角小写 abc 查得到。
	if hits := s.Lookup("abc"); len(hits) != 1 || hits[0].Entry.ID != "5.1" {
		t.Errorf("Lookup(abc) = %+v, want folded hit 5.1", hits)
	}

	// 未命中返回空（非 nil）。
	if hits := s.Lookup("存在しない"); len(hits) != 0 {
		t.Errorf("Lookup(存在しない) = %d hits, want 0", len(hits))
	}
}

func TestSurfaces(t *testing.T) {
	s, _ := newImportedStore(t)
	surfaces, maxLen := s.Surfaces()
	if len(surfaces) == 0 {
		t.Fatal("Surfaces() empty, want合格表面形")
	}
	// 单假名 あ 不应进列表。
	for _, sf := range surfaces {
		if sf == "あ" {
			t.Error("single-kana あ should not be indexed")
		}
	}
	if maxLen <= 0 || maxLen > maxSurfaceLen {
		t.Errorf("maxLen = %d, want 1..%d", maxLen, maxSurfaceLen)
	}

	// 空 store 的 surfaces 是空切片（非 nil）。
	empty, emptyMax := NewDictStore(t.TempDir()).Surfaces()
	if empty == nil || len(empty) != 0 || emptyMax != 0 {
		t.Errorf("empty store Surfaces() = (%v,%d), want ([],0)", empty, emptyMax)
	}
}

func TestEntriesPagePagination(t *testing.T) {
	s, _ := newImportedStore(t)

	// q 为空=浏览全部：total=10，分页取前 4 条。
	items, total := s.EntriesPage("日汉双解学习词典", "", 0, 4)
	if total != 10 {
		t.Errorf("browse total = %d, want 10", total)
	}
	if len(items) != 4 {
		t.Errorf("browse page len = %d, want 4", len(items))
	}
	// offset 越界只截到末尾，不 panic。
	items, total = s.EntriesPage("日汉双解学习词典", "", 8, 50)
	if total != 10 || len(items) != 2 {
		t.Errorf("browse offset=8 -> (len=%d,total=%d), want (2,10)", len(items), total)
	}
	// 不存在的字典返回空。
	if items, total := s.EntriesPage("不存在", "", 0, 50); total != 0 || len(items) != 0 {
		t.Errorf("unknown dict -> (len=%d,total=%d), want (0,0)", len(items), total)
	}
}

func TestEntriesPageSearchOrder(t *testing.T) {
	s, _ := newImportedStore(t)

	// key 精确 > text 含：q="テスト" → 6.1(key 精确) 排在 6.2(text 含) 前。
	items, total := s.EntriesPage("日汉双解学习词典", "テスト", 0, 50)
	if total != 2 {
		t.Fatalf("search テスト total = %d, want 2", total)
	}
	if items[0].Key != "テスト" || items[1].Key != "べつ" {
		t.Errorf("search テスト order = [%q,%q], want [テスト,べつ]", items[0].Key, items[1].Key)
	}

	// key 前缀 > key 含：q="アイ" → アイス(前缀) 排在 ワアイ(含) 前。
	items, total = s.EntriesPage("日汉双解学习词典", "アイ", 0, 50)
	if total != 2 {
		t.Fatalf("search アイ total = %d, want 2", total)
	}
	if items[0].Key != "アイス" || items[1].Key != "ワアイ" {
		t.Errorf("search アイ order = [%q,%q], want [アイス,ワアイ]", items[0].Key, items[1].Key)
	}
}

func TestDeleteAndList(t *testing.T) {
	s, _ := newImportedStore(t)
	if got := s.List(); len(got) != 1 || got[0].Name != "日汉双解学习词典" || got[0].Count != 10 {
		t.Fatalf("List() = %+v, want single 日汉双解学习词典/10", got)
	}
	if err := s.Delete("日汉双解学习词典"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("List() after delete = %+v, want empty", got)
	}
	// 删不存在的字典报错。
	if err := s.Delete("日汉双解学习词典"); err == nil {
		t.Error("Delete of missing dict should error")
	}
}
