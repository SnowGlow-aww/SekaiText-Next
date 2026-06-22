package model

// GlossaryEntry is one normalized term: source (usually Japanese) -> translation
// (usually Chinese), plus optional aliases and a free-form note. Category is the
// originating sheet name (e.g. "专有名词表"); SubCategory is the in-sheet section
// header (e.g. "人称", "音乐类"). Origin tracks where the entry came from so that
// re-import / remote sync can replace import|remote rows while preserving the
// user's hand-added rows.
type GlossaryEntry struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Translation string   `json:"translation"`
	Aliases     []string `json:"aliases,omitempty"`
	Note        string   `json:"note,omitempty"`
	Category    string   `json:"category"`
	SubCategory string   `json:"subCategory,omitempty"`
	Origin      string   `json:"origin"` // "import" | "user" | "remote"

	// Team-mode collaboration fields, populated from a remote /export sync.
	// All omitempty so local-only libraries keep their existing JSON shape.
	ContributorName string `json:"contributorName,omitempty"`
	UpdatedBy       string `json:"updatedBy,omitempty"`
	Version         int    `json:"version,omitempty"`
}

// Origin values.
const (
	OriginImport = "import"
	OriginUser   = "user"
	OriginRemote = "remote"
)

// Appellation is one cell of the 人称表 matrix: how Speaker addresses Target,
// in Japanese (JP) and Chinese (CN). Not searched in v1 — surfaced through the
// dedicated double-dropdown lookup ("A 称呼 B 为 ...").
type Appellation struct {
	Speaker string `json:"speaker"`
	Target  string `json:"target"`
	JP      string `json:"jp,omitempty"`
	CN      string `json:"cn,omitempty"`
}

// GrammarUsage is one row of the 语法用例 sheet: a grammar point with its
// connection pattern, an example sentence, and provenance. Surfaced through the
// dedicated Grammar page, separate from the term glossary.
type GrammarUsage struct {
	ID         string `json:"id"`
	Item       string `json:"item"`                 // 语法项目
	Location   string `json:"location,omitempty"`   // 出现位置
	Index      string `json:"index,omitempty"`      // 例句标号
	Connection string `json:"connection,omitempty"` // 接续
	Note       string `json:"note,omitempty"`       // 备注
	Unit       string `json:"unit,omitempty"`       // 来自哪个团
	Example    string `json:"example,omitempty"`    // 例句（含溢出列拼接）
	Reference  string `json:"reference,omitempty"`  // 参考翻译
}

// GlossaryData is the full persisted payload.
type GlossaryData struct {
	Entries      []GlossaryEntry `json:"entries"`
	Appellations []Appellation   `json:"appellations"`
	Grammar      []GrammarUsage  `json:"grammar,omitempty"`
}
