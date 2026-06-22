package service

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
	"sekaitext/backend/internal/model"
)

// SheetReport summarizes what the importer did with one worksheet.
type SheetReport struct {
	Sheet   string `json:"sheet"`
	Kind    string `json:"kind"`    // "terms" | "appellations" | "skipped"
	Count   int    `json:"count"`   // entries or appellations produced
	Skipped string `json:"skipped"` // reason / note when not fully ingested
}

// ImportReport is the full result of importing a workbook.
type ImportReport struct {
	Sheets       []SheetReport `json:"sheets"`
	TotalEntries int           `json:"totalEntries"`
	TotalAppell  int           `json:"totalAppellations"`
	TotalGrammar int           `json:"totalGrammar"`
}

// termSheets are the sheets that normalize to source->translation entries.
var termSheets = map[string]bool{
	"专有名词表":            true,
	"服装穿搭·时尚术语常见翻译": true,
	"新名词表（分类整理中）":     true,
	"25用专业名词表":         true,
}

// appellationSheet is the 人称表 matrix sheet.
const appellationSheet = "人称表"

// grammarSheet is the 语法用例 sheet (parsed into GrammarUsage, separate page).
const grammarSheet = "语法用例"

// normCell strips newlines (multi-line cells like "MORE\nMORE\nJUMP!") and
// surrounding whitespace for stable matching/storage.
func normCell(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", ""))
}

// isSubcategoryLabel decides whether a source-only cell is a section header
// (e.g. "人称", "地名", "音乐类") rather than a stray annotation that happens to
// sit alone in column A. Headers are short and free of sentence punctuation;
// annotations are long and contain CJK/Latin sentence marks.
func isSubcategoryLabel(s string) bool {
	if s == "" {
		return false
	}
	if len([]rune(s)) > 12 {
		return false
	}
	if strings.ContainsAny(s, "。，、：；！？,.!?：“”\"'（）()") {
		return false
	}
	return true
}

// ParseWorkbook reads a .xlsx and returns normalized entries + appellations +
// grammar usages and a per-sheet report. Sheets not recognized (应援色, anything
// new) are reported as skipped rather than silently dropped.
func ParseWorkbook(path string) ([]model.GlossaryEntry, []model.Appellation, []model.GrammarUsage, ImportReport, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, nil, ImportReport{}, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	var entries []model.GlossaryEntry
	var appellations []model.Appellation
	var grammar []model.GrammarUsage
	var report ImportReport

	for _, name := range f.GetSheetList() {
		rows, err := f.GetRows(name)
		if err != nil {
			report.Sheets = append(report.Sheets, SheetReport{Sheet: name, Kind: "skipped", Skipped: "read error: " + err.Error()})
			continue
		}
		switch {
		case name == appellationSheet:
			aps := parseAppellations(rows)
			appellations = append(appellations, aps...)
			report.Sheets = append(report.Sheets, SheetReport{Sheet: name, Kind: "appellations", Count: len(aps), Skipped: "「其他角色」附录(自由文本)未导入"})
		case name == grammarSheet:
			gs := parseGrammar(rows)
			grammar = append(grammar, gs...)
			report.Sheets = append(report.Sheets, SheetReport{Sheet: name, Kind: "grammar", Count: len(gs)})
		case termSheets[name]:
			es, note := parseTermSheet(name, rows)
			entries = append(entries, es...)
			report.Sheets = append(report.Sheets, SheetReport{Sheet: name, Kind: "terms", Count: len(es), Skipped: note})
		default:
			report.Sheets = append(report.Sheets, SheetReport{Sheet: name, Kind: "skipped", Skipped: "暂不处理此表"})
		}
	}

	report.TotalEntries = len(entries)
	report.TotalAppell = len(appellations)
	report.TotalGrammar = len(grammar)
	return entries, appellations, grammar, report, nil
}

// colMap holds the resolved column roles for a term sheet. For 专有名词表 /
// 新名词表 the repeated "译名" headers are the translations OF the preceding
// 别称 columns, not a parallel 日文/译文 block — so aliases and their
// translations are paired positionally (see resolveColumns).
type colMap struct {
	source  int
	trans   int
	aliases []int
	notes   []int
}

// cellAt safely reads row[i] (GetRows trims trailing blanks → rows are ragged).
func cellAt(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return normCell(row[i])
}

// resolveColumns finds the header row and maps column roles by header text.
// Returns (headerRowIdx, colMap, found). When no header is found, found=false
// and the caller falls back to a fixed A/B/C layout.
func resolveColumns(rows [][]string) (int, colMap, bool) {
	for r := 0; r < len(rows) && r < 5; r++ {
		row := rows[r]
		cm := colMap{source: -1, trans: -1}
		for i := range row {
			c := normCell(row[i])
			switch {
			case (c == "原名" || c == "日文") && cm.source < 0:
				cm.source = i
			case (c == "译名" || c == "译文") && cm.trans < 0:
				cm.trans = i
			case strings.HasPrefix(c, "别称"):
				cm.aliases = append(cm.aliases, i)
			case strings.HasPrefix(c, "备注"):
				cm.notes = append(cm.notes, i)
			}
		}
		if cm.source >= 0 && cm.trans >= 0 {
			return r, cm, true
		}
	}
	return -1, colMap{}, false
}

// parseTermSheet normalizes one term sheet into entries. Rows with a source but
// no translation/alias/note are treated as in-sheet subcategory headers.
func parseTermSheet(sheet string, rows [][]string) ([]model.GlossaryEntry, string) {
	headerIdx, cm, found := resolveColumns(rows)
	if !found {
		// No header row (e.g. 25用专业名词表): fixed A=source, B=trans, C=note.
		cm = colMap{source: 0, trans: 1, notes: []int{2}}
		headerIdx = -1
	}

	var out []model.GlossaryEntry
	sub := ""
	for r := headerIdx + 1; r < len(rows); r++ {
		row := rows[r]
		source := cellAt(row, cm.source)
		if source == "" || source == "原名" || source == "+" {
			continue
		}
		trans := cellAt(row, cm.trans)

		var aliases []string
		for _, ai := range cm.aliases {
			if v := cellAt(row, ai); v != "" {
				aliases = append(aliases, v)
			}
		}
		var noteParts []string
		for _, ni := range cm.notes {
			if v := cellAt(row, ni); v != "" {
				noteParts = append(noteParts, v)
			}
		}
		note := strings.Join(noteParts, " / ")

		// Source-only row → subcategory header, but only if it looks like a
		// short label. Free-floating annotations (full sentences with CJK
		// punctuation, or long text) sometimes sit in column A with no
		// translation; those must not become a carried-forward subcategory.
		if trans == "" && len(aliases) == 0 && note == "" {
			if isSubcategoryLabel(source) {
				sub = source
			}
			continue
		}

		out = append(out, model.GlossaryEntry{
			Source:      source,
			Translation: trans,
			Aliases:     aliases,
			Note:        note,
			Category:    sheet,
			SubCategory: sub,
			Origin:      model.OriginImport,
		})
	}

	return out, ""
}

// parseAppellations parses the 人称表 dual-block matrix. The header row is the
// one containing "说话人↓"; each occurrence starts a block whose speaker column
// is that cell and whose target columns are the non-empty header cells to its
// right (up to the next block). Within a block, a row with the speaker column
// filled is a JP row; the following row with it empty is the CN row.
func parseAppellations(rows [][]string) []model.Appellation {
	const marker = "说话人↓"

	headerIdx := -1
	for r := 0; r < len(rows) && headerIdx < 0; r++ {
		for i := range rows[r] {
			if normCell(rows[r][i]) == marker {
				headerIdx = r
				break
			}
		}
	}
	if headerIdx < 0 {
		return nil
	}
	header := rows[headerIdx]

	// Speaker-column indices (block starts).
	var speakerCols []int
	for i := range header {
		if normCell(header[i]) == marker {
			speakerCols = append(speakerCols, i)
		}
	}

	type block struct {
		speakerCol int
		targets    map[int]string // col -> target name
		order      []int
	}
	var blocks []block
	canon := map[string]bool{} // canonical target full-names, for boundary detection
	for bi, sc := range speakerCols {
		end := len(header)
		if bi+1 < len(speakerCols) {
			end = speakerCols[bi+1]
		}
		b := block{speakerCol: sc, targets: map[int]string{}}
		for i := sc + 1; i < end; i++ {
			name := normCell(header[i])
			if name != "" && name != marker {
				b.targets[i] = name
				b.order = append(b.order, i)
				canon[name] = true
			}
		}
		if len(b.order) > 0 {
			blocks = append(blocks, b)
		}
	}

	// The 人称表 stacks several matrices vertically (a second/third header with
	// VS singers and 其他角色 appears lower down). Only the primary matrix is
	// ingested in v1; bound the data-row scan at the next header row — the first
	// row below the header whose cells repeat ≥6 canonical full-names — so rows
	// from lower matrices don't corrupt this one's columns.
	endRow := len(rows)
	for r := headerIdx + 1; r < len(rows); r++ {
		matches := 0
		for _, v := range rows[r] {
			if canon[normCell(v)] {
				matches++
			}
		}
		if matches >= 6 {
			endRow = r
			break
		}
	}

	// keyed by speaker+target so JP/CN merge into one Appellation.
	type key struct{ s, t string }
	idx := map[key]int{}
	var out []model.Appellation
	upsert := func(speaker, target, val string, cn bool) {
		if val == "" {
			return
		}
		k := key{speaker, target}
		if pos, ok := idx[k]; ok {
			if cn {
				out[pos].CN = val
			} else {
				out[pos].JP = val
			}
			return
		}
		a := model.Appellation{Speaker: speaker, Target: target}
		if cn {
			a.CN = val
		} else {
			a.JP = val
		}
		idx[k] = len(out)
		out = append(out, a)
	}

	for _, b := range blocks {
		curSpeaker := ""
		cnRecorded := false
		for r := headerIdx + 1; r < endRow; r++ {
			row := rows[r]
			sp := cellAt(row, b.speakerCol)
			if sp != "" && sp != marker {
				curSpeaker = sp
				cnRecorded = false
				for _, tc := range b.order {
					upsert(curSpeaker, b.targets[tc], cellAt(row, tc), false)
				}
				continue
			}
			if curSpeaker == "" || cnRecorded {
				continue
			}
			// Empty speaker cell after a JP row: CN row if any target filled.
			any := false
			for _, tc := range b.order {
				if cellAt(row, tc) != "" {
					any = true
					break
				}
			}
			if any {
				for _, tc := range b.order {
					upsert(curSpeaker, b.targets[tc], cellAt(row, tc), true)
				}
				cnRecorded = true
			} else {
				curSpeaker = "" // blank separator row
			}
		}
	}

	// Append the lower per-unit cross-appellation matrices (r47+).
	out = append(out, parseUnitBlocks(rows)...)
	return out
}

// parseUnitBlocks parses the 5 vertically-stacked per-unit matrices below the
// primary 人称表 matrix. Each block is headed by a row where col 7 == "初音ミク"
// (the first virtual-singer target). Inside a block: col 1 = speaker (JP row;
// the following col-1-empty row is its CN translations); target columns are the
// 4 unit members at cols 2-5 (names from the block header) plus the 6 virtual
// singers at cols 7-10 + 12-13. Layout is identical across all 5 blocks.
func parseUnitBlocks(rows [][]string) []model.Appellation {
	// Virtual-singer target columns are fixed across every block.
	vsingerCols := []int{7, 8, 9, 10, 12, 13}

	isBlockHeader := func(row []string) bool {
		return cellAt(row, 7) == "初音ミク"
	}

	type key struct{ s, t string }
	idx := map[key]int{}
	var out []model.Appellation
	upsert := func(speaker, target, val string, cn bool) {
		if val == "" || speaker == "" || target == "" || speaker == target {
			return
		}
		k := key{speaker, target}
		if pos, ok := idx[k]; ok {
			if cn {
				out[pos].CN = val
			} else {
				out[pos].JP = val
			}
			return
		}
		a := model.Appellation{Speaker: speaker, Target: target}
		if cn {
			a.CN = val
		} else {
			a.JP = val
		}
		idx[k] = len(out)
		out = append(out, a)
	}

	for r := 0; r < len(rows); r++ {
		if !isBlockHeader(rows[r]) {
			continue
		}
		header := rows[r]
		// Target columns: 4 members (cols 2-5) + virtual singers, names from header.
		targets := map[int]string{}
		var order []int
		for _, c := range []int{2, 3, 4, 5} {
			if name := cellAt(header, c); name != "" {
				targets[c] = name
				order = append(order, c)
			}
		}
		for _, c := range vsingerCols {
			if name := cellAt(header, c); name != "" {
				targets[c] = name
				order = append(order, c)
			}
		}

		// Data rows until the next block header or a long empty gap.
		curSpeaker := ""
		cnRecorded := false
		for rr := r + 1; rr < len(rows); rr++ {
			if isBlockHeader(rows[rr]) {
				break
			}
			row := rows[rr]
			sp := cellAt(row, 1)
			if sp != "" {
				curSpeaker = sp
				cnRecorded = false
				for _, tc := range order {
					upsert(curSpeaker, targets[tc], cellAt(row, tc), false)
				}
				continue
			}
			if curSpeaker == "" || cnRecorded {
				continue
			}
			any := false
			for _, tc := range order {
				if cellAt(row, tc) != "" {
					any = true
					break
				}
			}
			if any {
				for _, tc := range order {
					upsert(curSpeaker, targets[tc], cellAt(row, tc), true)
				}
				cnRecorded = true
			} else {
				curSpeaker = ""
			}
		}
	}
	return out
}

// parseGrammar parses the 语法用例 sheet. Header is on row 8 (0-based); data
// rows run from row 9. Column roles: 0=语法项目, 1=出现位置, 2=标号, 3=接续,
// 4=备注, 5=团, 6=例句 (+ overflow cols 7-14 joined), 15=参考翻译.
func parseGrammar(rows [][]string) []model.GrammarUsage {
	const headerRow = 8
	var out []model.GrammarUsage
	for r := headerRow + 1; r < len(rows); r++ {
		row := rows[r]
		item := cellAt(row, 0)
		connection := cellAt(row, 3)
		// Build the example from col 6 plus non-empty overflow columns 7-14.
		var exParts []string
		for c := 6; c <= 14; c++ {
			if v := cellAt(row, c); v != "" {
				exParts = append(exParts, v)
			}
		}
		example := strings.Join(exParts, " ")
		// Skip rows with no grammar item and no usable content.
		if item == "" && connection == "" && example == "" {
			continue
		}
		out = append(out, model.GrammarUsage{
			Item:       item,
			Location:   cellAt(row, 1),
			Index:      cellAt(row, 2),
			Connection: connection,
			Note:       cellAt(row, 4),
			Unit:       cellAt(row, 5),
			Example:    example,
			Reference:  cellAt(row, 15),
		})
	}
	return out
}
