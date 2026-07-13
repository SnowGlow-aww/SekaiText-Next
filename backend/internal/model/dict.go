package model

// DictEntry 是词典里的一条释义（归一化后落盘/载入内存的最小单元）。字段与磁盘
// JSON 一一对应，均为小驼峰。Surfaces 是导入时预计算的「匹配表面形」——供编辑器
// 取词悬浮用；浏览/搜索不依赖它，所以某条没有合格表面形（如单假名）也照样出现在
// 分类浏览里，只是不参与正文取词。
type DictEntry struct {
	ID       string   `json:"id"`       // 原始 "page.index"，全库唯一（同形多义词靠它区分）
	Key      string   `json:"key"`      // 词条标题（假名+汉字，如 "あ[亜]"）
	Kana     string   `json:"kana"`     // 读音假名
	Accent   string   `json:"accent"`   // 声调标记（◎/①/…/空）
	Kanji    string   `json:"kanji"`    // 汉字表记（源为 null 时归一化成空串）
	Text     string   `json:"text"`     // 全文释义（日文+中文对照，含 \n）
	Surfaces []string `json:"surfaces"` // 合格匹配表面形（假名/各汉字表记，去噪后）
}

// DictFile 是一个词典分类归一化后的整体，落盘于 {glossaryDir}/dicts/<name>.json。
type DictFile struct {
	Name    string      `json:"name"`
	Count   int         `json:"count"`
	Entries []DictEntry `json:"entries"`
}

// DictInfo 是词典分类的轻量摘要（列表/管理界面用，不带条目正文）。
type DictInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
