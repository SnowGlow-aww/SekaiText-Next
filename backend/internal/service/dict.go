package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"sekaitext/backend/internal/model"
)

// maxSurfaceLen 是进索引/表面形列表的 surface 长度上限（rune 数）。超过此长度的
// 表面形不参与取词——既避免正文里超长扫描窗口拖慢前端最长匹配，也没有哪个真实
// 词条标题会长到这个程度。
const maxSurfaceLen = 24

// DictStore 是与主术语库物理隔离的**只读字典**存储。每个字典分类是
// {glossaryDir}/dicts/<name>.json 一个文件，导入时把源格式归一化落盘，进程内维护
// 供取词/浏览/搜索用的索引。它绝不进入 GlossaryStore、Export()、团队同步——字典
// 完全独立，不拖累主库任何 CRUD 路径。
//
// 加载策略：19MB / 44776 条解析约几百 ms，放后台 goroutine，端点访问前用
// waitReady() 阻塞到首次加载完成即可（本地 app 启动一次的一次性成本）。
type DictStore struct {
	dir    string        // {glossaryDir}/dicts
	loaded chan struct{} // 首次后台加载完成后 close，waitReady 靠它放行
	ioMu   sync.Mutex    // 串行化磁盘写 + 重建，避免并发导入/删除相互覆盖内存

	mu           sync.RWMutex
	dicts        []model.DictFile      // 全部字典分类（内存副本）
	surfaceIndex map[string][]entryRef // 原样 surface -> 命中条目
	foldedIndex  map[string][]entryRef // 折叠(NFKC+lower) surface -> 命中条目（Lookup 回退用）
	surfacesList []string              // 去重后的全部合格 surface（≤maxSurfaceLen）
	maxLen       int                   // surfacesList 里最长的 rune 数（≤maxSurfaceLen）
}

// entryRef 指向 dicts[dictIdx].Entries[entryIdx]。字典载入后只整体替换、绝不原地
// 改写，所以持有下标是安全的。
type entryRef struct {
	dictIdx  int
	entryIdx int
}

// LookupHit 是一次取词命中的一个义项：来自哪个字典 + 整条释义。
type LookupHit struct {
	DictName string          `json:"dictName"`
	Entry    model.DictEntry `json:"entry"`
}

// srcDictEntry 是源字典（日汉双解学习词典）JSON 数组里的一条原始记录。kanji 可为
// null，故用 *string。page/index 不解析：真实数据里跨页词条的 page 会是数组（如
// [1,2]），而 id 恒存在且形如 "page.index"，归一化只认 id，不碰 page/index。
type srcDictEntry struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Kana   string  `json:"kana"`
	Accent string  `json:"accent"`
	Kanji  *string `json:"kanji"`
	Key    string  `json:"key"`
}

// NewDictStore 记录 dicts/ 目录并起后台 goroutine 加载全部 *.json。
func NewDictStore(glossaryDir string) *DictStore {
	dir := filepath.Join(glossaryDir, "dicts")
	_ = os.MkdirAll(dir, 0755)
	s := &DictStore{
		dir:          dir,
		loaded:       make(chan struct{}),
		surfaceIndex: map[string][]entryRef{},
		foldedIndex:  map[string][]entryRef{},
	}
	go func() {
		if err := s.rebuildFromDisk(); err != nil {
			log.Printf("[dict] initial load: %v (starting empty)", err)
		}
		close(s.loaded)
	}()
	return s
}

// waitReady 阻塞到首次后台加载完成。所有读端点访问内存前先调它；Import/Delete 也
// 先等它，保证不会与初次加载并发 rebuild（省掉一整类竞态）。
func (s *DictStore) waitReady() { <-s.loaded }

// --- 加载 / 索引 ---

// rebuildFromDisk 读 dicts/ 下全部 *.json 重建内存。磁盘读取放 ioMu 临界区内，
// 与 Import/Delete 的写互斥，保证「读盘→建索引→换内存」相对彼此原子。
func (s *DictStore) rebuildFromDisk() error {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()

	ents, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			s.rebuildIndex(nil)
			return nil
		}
		return err
	}
	var files []model.DictFile
	for _, e := range ents {
		// 只吃 *.json（原子写留下的 *.json.tmp 后缀不是 .json，天然跳过）。
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			log.Printf("[dict] read %s: %v (skipped)", e.Name(), err)
			continue
		}
		var df model.DictFile
		if err := json.Unmarshal(data, &df); err != nil {
			log.Printf("[dict] parse %s: %v (skipped)", e.Name(), err)
			continue
		}
		if strings.TrimSpace(df.Name) == "" {
			df.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		files = append(files, df)
	}
	s.rebuildIndex(files)
	log.Printf("[dict] loaded %d dict(s) from %s", len(files), s.dir)
	return nil
}

// rebuildIndex 由字典切片重建全部索引，最后一次性换进内存（写锁只在换入时短暂
// 持有，重活在锁外做）。
func (s *DictStore) rebuildIndex(files []model.DictFile) {
	surfaceIndex := map[string][]entryRef{}
	foldedIndex := map[string][]entryRef{}
	seen := map[string]bool{}
	list := []string{}
	maxLen := 0
	for di := range files {
		for ei := range files[di].Entries {
			ref := entryRef{dictIdx: di, entryIdx: ei}
			for _, surf := range files[di].Entries[ei].Surfaces {
				n := utf8.RuneCountInString(surf)
				if n == 0 || n > maxSurfaceLen {
					continue // 超长表面形不进索引/列表
				}
				surfaceIndex[surf] = append(surfaceIndex[surf], ref)
				foldedIndex[foldSurface(surf)] = append(foldedIndex[foldSurface(surf)], ref)
				if !seen[surf] {
					seen[surf] = true
					list = append(list, surf)
				}
				if n > maxLen {
					maxLen = n
				}
			}
		}
	}
	s.mu.Lock()
	s.dicts = files
	s.surfaceIndex = surfaceIndex
	s.foldedIndex = foldedIndex
	s.surfacesList = list
	s.maxLen = maxLen
	s.mu.Unlock()
}

// foldSurface 把 surface 折叠成大小写/全半角不敏感的键：NFKC 归一 + 转小写。
// x/text 已在 go.mod（indirect），直接用 norm.NFKC，无需新增依赖。
func foldSurface(s string) string {
	return strings.ToLower(norm.NFKC.String(s))
}

// --- 归一化 ---

// computeSurfaces 由 kana + kanji 预计算合格匹配表面形：
//   - 候选 = kana，加上 kanji 去掉首尾 [] 后按 ・ 拆出的每个表记；
//   - 逐个 TrimSpace、去空、去重（保持顺序）；
//   - 匹配资格：utf8 长度≥2，或长度==1 且是 CJK 统一汉字——单假名(あ)剔除防噪音，
//     单汉字(愛)保留。不合格者不进 surfaces（该条仍能被浏览/搜索到）。
func computeSurfaces(kana, kanji string) []string {
	var cands []string
	if kana != "" {
		cands = append(cands, kana)
	}
	k := strings.TrimSpace(kanji)
	k = strings.TrimPrefix(k, "[")
	k = strings.TrimSuffix(k, "]")
	if k != "" {
		cands = append(cands, strings.Split(k, "・")...)
	}

	seen := map[string]bool{}
	out := []string{}
	for _, c := range cands {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if surfaceQualifies(c) {
			out = append(out, c)
		}
	}
	return out
}

// surfaceQualifies 判断一个表面形是否够格参与取词：≥2 rune 一律收；单 rune 仅收
// CJK 统一汉字（单假名会满屏误命中）。
func surfaceQualifies(s string) bool {
	n := utf8.RuneCountInString(s)
	if n >= 2 {
		return true
	}
	if n == 1 {
		r, _ := utf8.DecodeRuneInString(s)
		return unicode.Is(unicode.Han, r)
	}
	return false
}

// --- 写（导入/删除）---

// Import 解析源格式数组→归一化→原子落盘 dicts/<name>.json→重建内存。name 已存在
// 即覆盖。源格式校验：顶层非数组直接报错；缺 key 字段的条目跳过；全部无效则报错。
func (s *DictStore) Import(name string, raw []byte) (model.DictInfo, error) {
	// 先等首次加载完成，避免与初次 rebuild 并发（Import 本身会整体重建内存）。
	s.waitReady()

	name, err := sanitizeDictName(name)
	if err != nil {
		return model.DictInfo{}, err
	}

	var src []srcDictEntry
	if err := json.Unmarshal(raw, &src); err != nil {
		return model.DictInfo{}, fmt.Errorf("解析源字典失败（应为 JSON 数组）：%w", err)
	}

	entries := make([]model.DictEntry, 0, len(src))
	for i, e := range src {
		if strings.TrimSpace(e.Key) == "" {
			continue // 缺 key 字段跳过
		}
		kanji := ""
		if e.Kanji != nil {
			kanji = *e.Kanji
		}
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = fmt.Sprintf("entry-%d", i) // 兜底：源缺 id 时用数组位置，保证唯一
		}
		entries = append(entries, model.DictEntry{
			ID:       id,
			Key:      e.Key,
			Kana:     e.Kana,
			Accent:   e.Accent,
			Kanji:    kanji,
			Text:     e.Text,
			Surfaces: computeSurfaces(e.Kana, kanji),
		})
	}
	if len(entries) == 0 {
		return model.DictInfo{}, errors.New("字典为空或没有任何有效条目")
	}

	df := model.DictFile{Name: name, Count: len(entries), Entries: entries}
	// 紧凑序列化（非 Indent），44776 条别把磁盘/内存吃太狠。
	data, err := json.Marshal(df)
	if err != nil {
		return model.DictInfo{}, err
	}
	if err := s.writeDictFile(name, data); err != nil {
		return model.DictInfo{}, err
	}
	if err := s.rebuildFromDisk(); err != nil {
		return model.DictInfo{}, err
	}
	return model.DictInfo{Name: name, Count: len(entries)}, nil
}

// writeDictFile 原子写 dicts/<name>.json（临时文件 + rename），参照 glossary.go
// persist：中途失败也只留下旧文件，不会写出半截文件被误当有效字典载入。
func (s *DictStore) writeDictFile(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, name+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Delete 删除某字典文件并重建内存。字典不存在报错。
func (s *DictStore) Delete(name string) error {
	s.waitReady()
	name, err := sanitizeDictName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("字典不存在：%s", name)
		}
		return err
	}
	return s.rebuildFromDisk()
}

// sanitizeDictName 把上传文件名/URL path 清洗成安全的字典名：取 basename、去 .json
// 扩展、trim；拒绝空名与会逃逸目录的取值（防路径穿越）。
func sanitizeDictName(name string) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".json") {
		name = name[:len(name)-len(ext)]
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", errors.New("非法的字典名称")
	}
	return name, nil
}

// --- 读 ---

// List 返回全部字典分类摘要。
func (s *DictStore) List() []model.DictInfo {
	s.waitReady()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.DictInfo, 0, len(s.dicts))
	for i := range s.dicts {
		out = append(out, model.DictInfo{Name: s.dicts[i].Name, Count: s.dicts[i].Count})
	}
	return out
}

// Surfaces 返回全部合格表面形与其最长 rune 数（供前端最长匹配扫描的窗口上限）。
// 无字典时返回空切片（非 nil）+ 0。
func (s *DictStore) Surfaces() ([]string, int) {
	s.waitReady()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.surfacesList))
	copy(out, s.surfacesList)
	return out, s.maxLen
}

// Lookup 取词：先按原样精确查 surfaceIndex；查不到再折叠(NFKC+lower)查 foldedIndex。
// 同一 surface 可能对应同形多义词（多条），全部返回。字典数据载入后不可变，返回
// 共享底层切片是安全的（重建时整体替换、绝不原地改写）。
func (s *DictStore) Lookup(surface string) []LookupHit {
	s.waitReady()
	s.mu.RLock()
	defer s.mu.RUnlock()

	refs := s.surfaceIndex[surface]
	if len(refs) == 0 {
		refs = s.foldedIndex[foldSurface(surface)]
	}
	hits := make([]LookupHit, 0, len(refs))
	seen := map[entryRef]bool{} // 折叠回退可能让同一条经不同表面形重复命中，去重
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if ref.dictIdx >= len(s.dicts) || ref.entryIdx >= len(s.dicts[ref.dictIdx].Entries) {
			continue
		}
		hits = append(hits, LookupHit{
			DictName: s.dicts[ref.dictIdx].Name,
			Entry:    s.dicts[ref.dictIdx].Entries[ref.entryIdx],
		})
	}
	return hits
}

// EntriesPage 浏览/搜索某字典分类：q 为空=浏览全部（分页）；q 非空=大小写不敏感
// substring 匹配，按 key 精确>key 前缀>key 含>kana 含>text 含排序，匹配集合上再
// 分页。参照 glossary.go Search 的 matchScore 风格。
func (s *DictStore) EntriesPage(name, q string, offset, limit int) ([]model.DictEntry, int) {
	s.waitReady()
	s.mu.RLock()
	defer s.mu.RUnlock()

	var df *model.DictFile
	for i := range s.dicts {
		if s.dicts[i].Name == name {
			df = &s.dicts[i]
			break
		}
	}
	if df == nil {
		return []model.DictEntry{}, 0
	}

	q = strings.TrimSpace(q)
	var matched []model.DictEntry
	if q == "" {
		matched = df.Entries
	} else {
		lq := strings.ToLower(q)
		type scored struct {
			e     model.DictEntry
			score int
		}
		var hits []scored
		for _, e := range df.Entries {
			if sc := dictMatchScore(lq, e); sc > 0 {
				hits = append(hits, scored{e, sc})
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
		matched = make([]model.DictEntry, len(hits))
		for i := range hits {
			matched[i] = hits[i].e
		}
	}

	total := len(matched)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	page := matched[offset:end]
	out := make([]model.DictEntry, len(page))
	copy(out, page)
	return out, total
}

// dictMatchScore：key 精确=5 > key 前缀=4 > key 含=3 > kana 含=2 > text 含=1 > 0 无。
func dictMatchScore(lq string, e model.DictEntry) int {
	lkey := strings.ToLower(e.Key)
	switch {
	case lkey == lq:
		return 5
	case strings.HasPrefix(lkey, lq):
		return 4
	case strings.Contains(lkey, lq):
		return 3
	}
	if strings.Contains(strings.ToLower(e.Kana), lq) {
		return 2
	}
	if strings.Contains(strings.ToLower(e.Text), lq) {
		return 1
	}
	return 0
}
