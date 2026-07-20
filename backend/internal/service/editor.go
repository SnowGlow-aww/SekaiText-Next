package service

import (
	"encoding/json"
	"fmt"
	"math"

	diff "github.com/sergi/go-diff/diffmatchpatch"
	"os"
	"strings"

	"sekaitext/backend/internal/model"
)

// EditorService implements the core editor logic (port of Editor.py).
type EditorService struct{}

// NewEditorService creates a new EditorService.
func NewEditorService() *EditorService {
	return &EditorService{}
}

// CreateFile creates dstTalks from source talks (translation template).
func (e *EditorService) CreateFile(srctalks []model.SourceTalk, jp bool) []model.DstTalk {
	var dsttalks []model.DstTalk

	for idx, srctalk := range srctalks {
		subsrctalks := strings.Split(srctalk.Text, "\n")
		for iidx, subsrctalk := range subsrctalks {
			text := ""
			if jp {
				text = subsrctalk
			} else {
				for _, char := range subsrctalk {
					if strings.ContainsRune("♪☆/『』", char) {
						text += string(char)
					}
				}
			}
			dsttalks = append(dsttalks, model.DstTalk{
				Idx:     idx + 1,
				Speaker: srctalk.Speaker,
				Text:    text,
				Start:   iidx == 0,
				End:     false,
				Checked: true,
				Save:    true,
			})
		}
		dsttalks[len(dsttalks)-1].End = true
	}

	// Replace Japanese speaker names with Chinese
	for i := range dsttalks {
		speaker := strings.ReplaceAll(dsttalks[i].Speaker, "の声", "")
		parts := strings.Split(speaker, "・")
		for pi, part := range parts {
			if char, ok := model.FindCharacterByJapaneseName(part); ok {
				parts[pi] = char.NameC
			}
		}
		newSpeaker := strings.Join(parts, "・")
		newSpeaker = strings.ReplaceAll(newSpeaker, "の声", "的声音")
		newSpeaker = strings.ReplaceAll(newSpeaker, "ネネロボ", "宁宁号")
		dsttalks[i].Speaker = newSpeaker
	}

	// Assign each row its own DstIdx = position in the slice, same as
	// LoadContent / CheckLines. Without this every row keeps the zero value 0,
	// so editing any line via talks[row].DstIdx writes to dstTalks[0] — every
	// edit silently collapses onto the first line and all other translations are
	// lost on save. This "从源文本新建翻译" path never runs through CheckLines,
	// so it must set DstIdx itself.
	for i := range dsttalks {
		dsttalks[i].DstIdx = i
	}

	return dsttalks
}

// LoadFile parses a translation .txt file into DstTalk entries.
func (e *EditorService) LoadFile(filepath string) ([]model.DstTalk, *model.SaveMetadata, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read file: %w", err)
	}
	return e.LoadContent(string(data))
}

// LoadContent parses translation content from a string instead of a file.
// Returns the parsed talks and any embedded SaveMetadata (nil if absent).
func (e *EditorService) LoadContent(content string) ([]model.DstTalk, *model.SaveMetadata, error) {
	var meta *model.SaveMetadata
	if strings.HasPrefix(content, "#SekaiText ") {
		idx := strings.Index(content, "\n")
		if idx > 0 {
			header := content[len("#SekaiText "):idx]
			var m model.SaveMetadata
			if json.Unmarshal([]byte(header), &m) == nil {
				meta = &m
			}
			content = content[idx+1:]
		}
	}

	lines := strings.Split(content, "\n")
	var talks []model.DstTalk
	preblank := false

	for idx, line := range lines {
		line = strings.Replace(line, ":", "：", 1)
		var speaker, fulltext string

		if strings.Contains(line, "：") {
			parts := strings.SplitN(line, "：", 2)
			speaker = parts[0]
			fulltext = parts[1]
		} else if strings.Contains(line, "/") {
			speaker = "选项"
			fulltext = line
		} else {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				speaker = ""
				fulltext = line
			} else {
				speaker = "场景"
				fulltext = line
			}
		}

		if speaker == "" {
			if preblank {
				continue
			}
			preblank = true
		} else {
			preblank = false
		}

		texts := strings.Split(fulltext, "\\N")
		for iidx, text := range texts {
			text, checked, msg := e.checkText(speaker, text)
			talk := model.DstTalk{
				Idx:     idx + 1,
				Speaker: speaker,
				Text:    text,
				Start:   iidx == 0,
				End:     false,
				Checked: checked,
				Save:    true,
				Message: msg,
			}
			talks = append(talks, talk)
		}
		talks[len(talks)-1].End = true
	}

	if preblank && len(talks) > 0 {
		talks = talks[:len(talks)-1]
	}

	// Assign each row its own DstIdx = position in the slice. Without this every
	// row keeps the zero value 0, so when the editor maps talks[row] -> the
	// matching dstTalks entry via DstIdx, EVERY edit writes to dstTalks[0] —
	// editing any line silently overwrites the FIRST line on save. (talks and
	// dstTalks are the same array on open until a compare/align runs, which makes
	// the collapse to row 0 invisible until export.)
	for i := range talks {
		talks[i].DstIdx = i
	}

	return talks, meta, nil
}

// SerializeWithMeta serializes dstTalks to translation text. The proofread /
// agreement output must be byte-for-byte the same format as the translation
// file (plain text, CRLF, no header) — proofread edits overwrite the original
// lines in place rather than being annotated. The #SekaiText metadata header is
// therefore NOT written; meta is accepted only for backward compatibility and
// ignored. (Re-opening such a file requires picking the story/mode manually,
// which matches how the translation files already work.)
func (e *EditorService) SerializeWithMeta(dsttalks []model.DstTalk, saveN bool, meta *model.SaveMetadata) string {
	return e.SerializeContent(dsttalks, saveN)
}

// SerializeContent serializes dstTalks to translation text format and returns the string.
func (e *EditorService) SerializeContent(dsttalks []model.DstTalk, saveN bool) string {
	var out strings.Builder
	for _, talk := range dsttalks {
		switch talk.Speaker {
		case "场景", "左上场景", "选项", "":
			if (talk.Speaker == "场景" || talk.Speaker == "左上场景") && talk.Text == "" {
				talk.Text = talk.Speaker
			} else if talk.Speaker == "选项" && !strings.Contains(talk.Text, "/") {
				talk.Text = talk.Text + "/"
			}
			if talk.Speaker == "" && talk.Text != "" {
				out.WriteString(talk.Text)
			} else {
				out.WriteString(talk.Text)
			}
			out.WriteString("\n")

		default:
			if talk.Start {
				out.WriteString(talk.Speaker + "：")
			}
			lines := strings.Split(talk.Text, "\n")
			out.WriteString(lines[0])
			if !talk.End {
				if saveN {
					out.WriteString("\\N")
				}
			} else {
				out.WriteString("\n")
			}
		}
	}

	result := strings.TrimRight(out.String(), "\n")
	// Normalize line endings to CRLF so saved files match the translation files
	// the team works with (Windows line endings). The serializer builds with
	// "\n"; collapse any pre-existing "\r\n" first to avoid doubling, then
	// convert every "\n" to "\r\n".
	result = strings.ReplaceAll(result, "\r\n", "\n")
	result = strings.ReplaceAll(result, "\n", "\r\n")
	return result
}

// SourceTalksAsDst 把解析出的原文行直接当作译文行（「导出原文 txt」用）：对话框
// 正文按行拆开，首行 Start（序列化出「说话人：」前缀），每行都 End（各占一个物理
// 行，不做 \N 合并）——与译者既有的 ja-JP 原文稿形状一致，加载器也能按续行对齐。
func SourceTalksAsDst(src []model.SourceTalk) []model.DstTalk {
	out := make([]model.DstTalk, 0, len(src))
	for i, s := range src {
		switch s.Speaker {
		case "场景", "左上场景", "选项", "":
			out = append(out, model.DstTalk{Idx: i + 1, Speaker: s.Speaker, Text: s.Text, Start: true, End: true, Checked: true, Save: true})
		default:
			for li, line := range strings.Split(s.Text, "\n") {
				out = append(out, model.DstTalk{Idx: i + 1, Speaker: s.Speaker, Text: line, Start: li == 0, End: true, Checked: true, Save: true})
			}
		}
	}
	return out
}

// CheckLines aligns loaded talks with source talks (handles line mismatches).
func (e *EditorService) CheckLines(srctalks []model.SourceTalk, loadtalks []model.DstTalk) []model.DstTalk {
	// Reconcile the LEADING run of scene/option lines with the source.
	//
	// The translator sometimes rewrites the opening narration and splits one
	// source scene line into several lines (e.g. source "ストリートのセカイ"
	// becomes "我们跟这家店…" + "才获准…"). The old code blindly trimmed the
	// excess lines off the HEAD, which deleted real first-line content and
	// shifted every following row up — the "首行被覆盖" bug.
	//
	// Correct handling: look only at the first contiguous block of non-empty
	// scene/option lines (up to the first blank / dialogue line) on each side.
	// If the loaded block has MORE lines than the source block, the extra lines
	// are a translator split: merge them back into a single entry joined by
	// "\n". On save, scene lines write their text verbatim with the embedded
	// "\n", so both lines round-trip to the file unchanged — no content lost,
	// and alignment to the single source slot is restored. Empty placeholder
	// lines in the block are dropped (template padding).
	srcLead := leadingSceneRun(srctalks)
	dstLead := 0
	for _, lt := range loadtalks {
		if lt.Speaker == "场景" || lt.Speaker == "左上场景" || lt.Speaker == "选项" {
			dstLead++
		} else {
			break
		}
	}
	if dstLead > srcLead && srcLead > 0 {
		// Merge the leading dstLead lines down to srcLead entries: keep the
		// first (srcLead-1) as-is, fold the rest into the srcLead-th, joining
		// non-empty texts with "\n".
		var merged []model.DstTalk
		merged = append(merged, loadtalks[:srcLead-1]...)
		tail := loadtalks[srcLead-1 : dstLead]
		base := tail[0]
		var texts []string
		for _, t := range tail {
			if strings.TrimSpace(t.Text) != "" {
				texts = append(texts, t.Text)
			}
		}
		base.Text = strings.Join(texts, "\n")
		base.End = true
		merged = append(merged, base)
		merged = append(merged, loadtalks[dstLead:]...)
		loadtalks = merged
	}
	for len(loadtalks) > 0 && loadtalks[0].Text == "" {
		loadtalks = loadtalks[1:]
	}

	var newtalks []model.DstTalk
	idx := 0
	dstend := false

	for srcidx, srctalk := range srctalks {
		if idx >= len(loadtalks) {
			dstend = true
		}

		// Fix "左上场景" vs "场景"
		if !dstend && srctalk.Speaker == "左上场景" && loadtalks[idx].Speaker == "场景" {
			loadtalks[idx].Speaker = "左上场景"
		}

		// Scene/option lines
		if srctalk.Speaker == "场景" || srctalk.Speaker == "左上场景" || srctalk.Speaker == "选项" || srctalk.Speaker == "" {
			if dstend || srctalk.Speaker != loadtalks[idx].Speaker {
				newtalks = append(newtalks, model.DstTalk{
					Idx:     srcidx + 1,
					Speaker: srctalk.Speaker,
					Text:    srctalk.Text,
					Start:   true,
					End:     true,
					Checked: true,
					Save:    true,
				})
				continue
			}
		}

		subsrctalks := strings.Split(srctalk.Text, "\n")
		dstidx := -1
		if !dstend {
			dstidx = loadtalks[idx].Idx
		}

		for iidx := range subsrctalks {
			if idx >= len(loadtalks) {
				dstend = true
			}

			if !dstend && loadtalks[idx].Idx == dstidx {
				text, _, msg := e.checkText(srctalk.Speaker, loadtalks[idx].Text)
				talk := model.DstTalk{
					Idx:     srcidx + 1,
					Speaker: loadtalks[idx].Speaker,
					Text:    text,
					Start:   iidx == 0,
					End:     false,
					Checked: true,
					Save:    true,
					Message: msg,
				}
				idx++
				newtalks = append(newtalks, talk)
			} else if dstend {
				talk := model.DstTalk{
					Idx:     srcidx + 1,
					Speaker: srctalk.Speaker,
					Text:    " ",
					Start:   iidx == 0,
					End:     false,
					Checked: false,
					Save:    true,
				}
				newtalks = append(newtalks, talk)
			} else {
				// \N lost
				if len(newtalks) > 0 {
					newtalks[len(newtalks)-1].Text += "\n【分行不一致】"
					newtalks[len(newtalks)-1].End = true
					newtalks[len(newtalks)-1].Checked = false
				}
				continue
			}
		}

		// Too many \N
		for idx < len(loadtalks) && loadtalks[idx].Idx == dstidx {
			text, _, chkMsg := e.checkText(srctalk.Speaker, loadtalks[idx].Text)
			lineMsg := "分行不一致"
			if chkMsg != "" {
				lineMsg = chkMsg + "\n" + lineMsg
			}
			talk := model.DstTalk{
				Idx:     srcidx + 1,
				Speaker: loadtalks[idx].Speaker,
				Text:    text,
				Start:   false,
				End:     true,
				Checked: false,
				Save:    true,
				Message: lineMsg,
			}
			idx++
			newtalks = append(newtalks, talk)
		}

		if len(newtalks) > 0 {
			newtalks[len(newtalks)-1].End = true
		}
	}

	// Extra lines at the end
	if idx < len(loadtalks) {
		idxdiff := 0
		if len(newtalks) > 0 && idx < len(loadtalks) {
			idxdiff = newtalks[len(newtalks)-1].Idx - loadtalks[idx].Idx + 1
		}
		for _, talk := range loadtalks[idx:] {
			newtalk := talk
			newtalk.Idx = talk.Idx + idxdiff
			newtalk.Checked = false
			if newtalk.Message != "" {
				newtalk.Message += "\n多余行"
			} else {
				newtalk.Message = "多余行"
			}
			newtalks = append(newtalks, newtalk)
		}
	}

	// Assign DstIdx = slice position so the editor can map each aligned row to
	// its dstTalks counterpart. Without this all rows share DstIdx 0 and every
	// edit overwrites the first line on export (same bug as LoadContent).
	for i := range newtalks {
		newtalks[i].DstIdx = i
	}

	return newtalks
}

// CompareText builds editor rows by pairing referTalks (baseline) with
// checkTalks (current text) sub-line by sub-line, grouped by source idx.
// Each sub-line produces EXACTLY ONE row carrying both Text (check) and
// Baseline (refer); the character diff is stored as a single merged,
// ordered DiffParts list that the frontend filters per side (baseline shows
// same+remove, edit shows same+add). No row splitting, no shared dstidx —
// row count per idx is fixed, which structurally prevents duplicate rows.
func (e *EditorService) CompareText(refertalks, checktalks []model.DstTalk, editormode int) []model.DstTalk {
	refGrp := groupByIdx(refertalks)
	chkGrp := groupByIdx(checktalks)
	// True slice position of each chk row, grouped the same way as chkGrp.
	// DstIdx must be the row's position IN checktalks (= the dstTalks the editor
	// keeps and saves), not a running counter over emitted rows: deletion rows
	// (ref-only) emit a row but consume no chk slot, so a shared counter drifts
	// ahead and every later edit writes into the WRONG dstTalks line.
	chkPos := make(map[int][]int)
	for i, t := range checktalks {
		chkPos[t.Idx] = append(chkPos[t.Idx], i)
	}

	// Collect all unique idxs in order
	var keys []int
	seen := make(map[int]bool)
	for _, t := range refertalks {
		if !seen[t.Idx] {
			seen[t.Idx] = true
			keys = append(keys, t.Idx)
		}
	}
	for _, t := range checktalks {
		if !seen[t.Idx] {
			seen[t.Idx] = true
			keys = append(keys, t.Idx)
		}
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	var newtalks []model.DstTalk

	for _, idx := range keys {
		ref := refGrp[idx]
		chk := chkGrp[idx]
		maxN := len(ref)
		if len(chk) > maxN {
			maxN = len(chk)
		}

		for k := 0; k < maxN; k++ {
			var row model.DstTalk
			var baseText, curText string
			switch {
			case k < len(chk) && k < len(ref):
				row = chk[k]
				baseText = ref[k].Text
				curText = chk[k].Text
				row.DstIdx = chkPos[idx][k]
			case k < len(chk):
				// New sub-line with no baseline counterpart (pure addition).
				row = chk[k]
				baseText = ""
				curText = chk[k].Text
				row.DstIdx = chkPos[idx][k]
			default:
				// Baseline sub-line dropped in current text; keep it as an
				// empty edit row so the deletion is visible under compare.
				// It has NO counterpart slot in checktalks/dstTalks, so DstIdx
				// is -1; ChangeText inserts a slot at the right position if the
				// user types into it (writing to a shared counter index here
				// would overwrite an unrelated line instead).
				row = ref[k]
				row.Text = ""
				baseText = ref[k].Text
				curText = ""
				row.DstIdx = -1
			}

			row.CheckMode = false
			row.Proofread = nil
			// Only produce a diff when there is a real, non-empty baseline that
			// differs from the current text. An empty baseline (fresh/blank line
			// with no prior version) has nothing to compare against — diffing it
			// would mark the whole line as an addition (spurious all-green rows).
			if editormode >= 1 && baseText != "" && baseText != curText {
				row.Baseline = baseText
				row.DiffParts = mergedDiff(baseText, curText)
			} else {
				row.Baseline = curText
				row.DiffParts = nil
			}
			newtalks = append(newtalks, row)
		}
	}

	return newtalks
}

// leadingSceneRun counts the first contiguous block of scene/option lines in
// the source, stopping at the first line that is neither (a dialogue line or a
// blank line). Mirrors the dstLead count in CheckLines so the two sides are
// compared on the same footing.
func leadingSceneRun(srctalks []model.SourceTalk) int {
	n := 0
	for _, st := range srctalks {
		if st.Speaker == "场景" || st.Speaker == "左上场景" || st.Speaker == "选项" {
			n++
		} else {
			break
		}
	}
	return n
}

// groupByIdx collects rows with the same source idx into slices.
func groupByIdx(talks []model.DstTalk) map[int][]model.DstTalk {
	groups := make(map[int][]model.DstTalk)
	for _, t := range talks {
		groups[t.Idx] = append(groups[t.Idx], t)
	}
	return groups
}

// ChangeText handles text editing with proofread mode logic.
func (e *EditorService) ChangeText(row int, text string, editormode int,
	talks, dsttalks, refertalks []model.DstTalk) ([]model.DstTalk, []model.DstTalk) {

	if row < 0 || row >= len(talks) {
		return talks, dsttalks
	}

	speaker := talks[row].Speaker
	text, checked, msg := e.checkText(speaker, text)

	if len(strings.Split(text, "\n")) > 1 {
		checked = false
		msg = "文本含换行符，请拆分至多行"
	}

	// Empty-speaker rows can be real narration. Only an actually blank row is a
	// synthetic separator and must remain read-only.
	if speaker == "" && strings.TrimSpace(talks[row].Text) == "" {
		return talks, dsttalks
	}

	// Translate mode
	if editormode == 0 {
		talks[row].Text = text
		talks[row].Checked = checked
		talks[row].Message = msg
		dstidx := talks[row].DstIdx
		if dstidx >= 0 && dstidx < len(dsttalks) {
			dsttalks[dstidx].Text = text
			dsttalks[dstidx].Checked = checked
			dsttalks[dstidx].Message = msg
		}
		return talks, dsttalks
	}

	// Proofread (1) and 合意 (2) modes share one in-place update path. Each row
	// already carries its own Baseline (set by CompareText / on load), so editing
	// never splits or inserts rows — it just updates Text and recomputes the diff
	// against this row's baseline. Fixed row count = no duplicate-row class of bug.
	if editormode >= 1 {
		talks[row].Text = text
		talks[row].Checked = true
		talks[row].Message = msg
		talks[row].CheckMode = false
		talks[row].Proofread = nil
		// Only diff against a real, non-empty baseline. An empty baseline means
		// there's no prior version to compare (e.g. a freshly created blank line),
		// so editing it must NOT mark the whole line green; adopt the new text as
		// the baseline instead. Mirrors the guard in CompareText.
		if talks[row].Baseline != "" && talks[row].Baseline != text {
			talks[row].DiffParts = mergedDiff(talks[row].Baseline, text)
		} else {
			talks[row].DiffParts = nil
			talks[row].Baseline = text
		}
		dstidx := talks[row].DstIdx
		if dstidx >= 0 && dstidx < len(dsttalks) {
			dsttalks[dstidx].Text = text
			dsttalks[dstidx].Checked = checked
			dsttalks[dstidx].Message = msg
		} else {
			// Compared "deletion" rows (合意/校对) have no counterpart slot in
			// dstTalks (DstIdx -1 from CompareText). The edit must not be dropped
			// (the frontend persists dstTalks, not talks — it would be lost on
			// save), and it must not be appended at the end either (wrong position
			// in the saved file). Insert a new slot right after the nearest
			// preceding row that has a real slot, then shift the later rows'
			// DstIdx to keep every mapping consistent.
			insertPos := 0
			for j := row - 1; j >= 0; j-- {
				if d := talks[j].DstIdx; d >= 0 && d < len(dsttalks) {
					insertPos = d + 1
					break
				}
			}
			if insertPos > len(dsttalks) {
				insertPos = len(dsttalks)
			}
			slot := talks[row]
			slot.Checked = checked
			slot.Message = msg
			dsttalks = insertDstTalk(dsttalks, insertPos, slot)
			for j := range talks {
				if j != row && talks[j].DstIdx >= insertPos {
					talks[j].DstIdx++
				}
			}
			talks[row].DstIdx = insertPos
		}
	}

	return talks, dsttalks
}

// AddLine adds a sub-line after the given row.
func (e *EditorService) AddLine(row int, talks, dsttalks []model.DstTalk, isProofreading bool) ([]model.DstTalk, []model.DstTalk) {
	// Bound row on both sides, same as ChangeText / RemoveLine / ReplaceBrackets:
	// a negative row would panic at talks[row] below.
	if row < 0 || row >= len(talks) {
		return talks, dsttalks
	}

	newtalk := talks[row]
	newtalk.Text = " "
	newtalk.End = true
	newtalk.Checked = true
	newtalk.Save = true
	newtalk.Start = false
	newtalk.Proofread = nil
	newtalk.CheckMode = false
	// A freshly added sub-line has no baseline counterpart: it is a pure
	// addition, so leave Baseline empty (renders fully green under compare).
	newtalk.Baseline = ""
	newtalk.DiffParts = nil

	dstidx := talks[row].DstIdx
	if dstidx >= 0 && dstidx < len(dsttalks) {
		dstInsert := dstidx + 1
		newtalk.DstIdx = dstInsert
		// Shift by DstIdx VALUE, not by talks position: in 合意/校对 the list can
		// contain "deletion" rows with DstIdx -1 interleaved, so position-based
		// shifting would corrupt their sentinel (and rows are only guaranteed to
		// map correctly through their own DstIdx). Runs before inserting newtalk
		// so the new row's explicit index is not double-shifted.
		for i := range talks {
			if talks[i].DstIdx >= dstInsert {
				talks[i].DstIdx++
			}
		}
		dsttalks = insertDstTalk(dsttalks, dstInsert, newtalk)
	} else {
		// The anchor row has no dstTalks slot (compared deletion row): the new
		// sub-line gets none either. ChangeText inserts a real slot at the right
		// position as soon as the user types into it.
		newtalk.DstIdx = -1
	}

	talks = insertTalk(talks, row+1, newtalk)

	// Update the original row
	talks[row].End = false
	talks[row].Checked = true
	talks[row].Save = true
	if dstidx >= 0 && dstidx < len(dsttalks) {
		dsttalks[dstidx].End = false
		dsttalks[dstidx].Checked = true
		dsttalks[dstidx].Save = true
	}

	return talks, dsttalks
}

// RemoveLine removes a sub-line at the given row.
func (e *EditorService) RemoveLine(row int, talks, dsttalks []model.DstTalk) ([]model.DstTalk, []model.DstTalk) {
	if row >= len(talks) || row < 0 {
		return talks, dsttalks
	}

	dstidx := talks[row].DstIdx

	// The preceding row inherits the group tail only when the removed row is
	// itself the tail; deleting a head/middle sub-line leaves the group's real
	// End marker further down untouched.
	if row > 0 && talks[row].End {
		talks[row-1].End = true
	}
	if dstidx > 0 && dstidx < len(dsttalks) && dsttalks[dstidx].End {
		dsttalks[dstidx-1].End = true
	}

	talks = append(talks[:row], talks[row+1:]...)
	if dstidx >= 0 && dstidx < len(dsttalks) {
		dsttalks = append(dsttalks[:dstidx], dsttalks[dstidx+1:]...)
		// Renumber by DstIdx VALUE and only when a dstTalks slot was actually
		// removed. The old position-based loop decremented every later row even
		// when the removed row had no slot (compared deletion row, DstIdx -1) —
		// after that, every subsequent edit landed one line UP in dstTalks and
		// the file saved with lines overwritten by their neighbours.
		for i := range talks {
			if talks[i].DstIdx > dstidx {
				talks[i].DstIdx--
			}
		}
	}

	return talks, dsttalks
}

// ReplaceBrackets replaces all bracket types with the specified pair
// on every sub-line that shares the same source idx, and on the corresponding dstTalks.
func (e *EditorService) ReplaceBrackets(talks []model.DstTalk, dstTalks []model.DstTalk, row int, brackets string) ([]model.DstTalk, []model.DstTalk) {
	// Guard on rune count, not byte length: a single multi-byte CJK bracket like
	// "【" has len()==3 but only one rune, so a byte-length check would pass and
	// then runes[1] would panic. Also bound row on both sides.
	runes := []rune(brackets)
	if row < 0 || row >= len(talks) || len(runes) < 2 {
		return talks, dstTalks
	}
	targetIdx := talks[row].Idx
	openB := runes[0]
	closeB := runes[1]

	replace := func(text string) string {
		var b strings.Builder
		for _, ch := range text {
			if strings.ContainsRune("「『（“‘【", ch) {
				b.WriteRune(openB)
			} else if strings.ContainsRune("」』）”’】", ch) {
				b.WriteRune(closeB)
			} else {
				b.WriteRune(ch)
			}
		}
		return b.String()
	}

	for i := range talks {
		if talks[i].Idx == targetIdx {
			talks[i].Text = replace(talks[i].Text)
			if talks[i].DstIdx >= 0 && talks[i].DstIdx < len(dstTalks) {
				dstTalks[talks[i].DstIdx].Text = replace(dstTalks[talks[i].DstIdx].Text)
			}
		}
	}
	return talks, dstTalks
}

// checkText validates text content and returns fixed text, pass/fail, and warning message.
func (e *EditorService) checkText(speaker, text string) (string, bool, string) {
	var msg string

	if speaker != "" && speaker != "场景" && speaker != "左上场景" && speaker != "选项" && text == "" {
		msg = "空行，若不需要改行请点右侧“-”删去本行"
		return text, false, msg
	}

	// Scene / 左上场景 / blank lines may legitimately contain multiple lines
	// joined by "\n" — e.g. the opening narration that a translator split across
	// several lines and CheckLines merged back into one entry. They need no
	// sentence-end / length checks, so trim each line but KEEP the "\n"s; the
	// "[0]"-only split below would silently drop everything after the first line.
	if speaker == "场景" || speaker == "左上场景" || speaker == "" {
		parts := strings.Split(text, "\n")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return strings.Join(parts, "\n"), true, ""
	}

	text = strings.TrimSpace(strings.Split(text, "\n")[0])
	if text == "" {
		return text, true, ""
	}

	if speaker == "选项" {
		if !strings.Contains(text, "/") {
			text += "/"
		}
		if strings.HasSuffix(text, "/") {
			msg = "选项必须用/分隔"
			return text, false, msg
		}
		return text, true, ""
	}

	// Standard text replacements
	text = strings.ReplaceAll(text, "…", "...")
	text = strings.ReplaceAll(text, "(", "（")
	text = strings.ReplaceAll(text, ")", "）")
	text = strings.ReplaceAll(text, ",", "，")
	text = strings.ReplaceAll(text, "?", "？")
	text = strings.ReplaceAll(text, "!", "！")
	text = strings.ReplaceAll(text, "~", "～")
	text = strings.ReplaceAll(text, "欸", "诶")

	check := true
	normalEnd := []rune{'、', '，', '。', '？', '！', '～', '♪', '☆', '.', '—'}
	unusualEnd := []rune{'）', '」', '』', '”'}

	runes := []rune(text)
	if len(runes) > 0 {
		last := runes[len(runes)-1]
		if containsRune(normalEnd, last) {
			if strings.Contains(text, ".，") || strings.Contains(text, ".。") {
				msg = "「……。」和「……，」只保留省略号"
				check = false
			}
		} else if containsRune(unusualEnd, last) {
			if len(runes) > 1 && !containsRune(normalEnd, runes[len(runes)-2]) {
				msg = "句尾缺少逗号句号"
				check = false
			}
		} else {
			msg = "句尾缺少逗号句号"
			check = false
		}
	}

	// Check dashes (only if no msg yet, to avoid overwriting)
	if msg == "" && strings.Contains(text, "—") {
		dashCount := strings.Count(text, "—")
		doubleDashCount := strings.Count(text, "——")
		if dashCount != doubleDashCount*2 {
			msg = "破折号用双破折——，或者视情况删掉"
			check = false
		}
	}

	// Check line length (only if no msg yet)
	if msg == "" {
		lineLen := lineLength(strings.Split(text, "\n")[0])
		if lineLen >= 30 {
			msg = "单行过长，请删减或换行"
			check = false
		}
	}

	return text, check, msg
}

// lineLength calculates display width (half-width for ASCII).
func lineLength(s string) int {
	count := 0
	for _, ch := range s {
		if ch <= 127 {
			count++
		} else {
			count += 2
		}
	}
	return int(math.Ceil(float64(count) / 2.0))
}

// mergedDiff returns a single ordered diff list (same/add/remove interleaved)
// between baseline `a` and current `b`. The frontend filters it per side:
// the baseline row renders same+remove, the edit row renders same+add.
func mergedDiff(a, b string) []model.DiffPart {
	if a == b {
		return nil
	}
	dmp := diff.New()
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var parts []model.DiffPart
	for _, d := range diffs {
		switch d.Type {
		case diff.DiffEqual:
			parts = append(parts, model.DiffPart{Text: d.Text, Type: "same"})
		case diff.DiffDelete:
			parts = append(parts, model.DiffPart{Text: d.Text, Type: "remove"})
		case diff.DiffInsert:
			parts = append(parts, model.DiffPart{Text: d.Text, Type: "add"})
		}
	}
	return parts
}

func containsRune(runes []rune, r rune) bool {
	for _, v := range runes {
		if v == r {
			return true
		}
	}
	return false
}

func insertTalk(slice []model.DstTalk, index int, talk model.DstTalk) []model.DstTalk {
	slice = append(slice, model.DstTalk{})
	copy(slice[index+1:], slice[index:])
	slice[index] = talk
	return slice
}

func insertDstTalk(slice []model.DstTalk, index int, talk model.DstTalk) []model.DstTalk {
	slice = append(slice, model.DstTalk{})
	copy(slice[index+1:], slice[index:])
	slice[index] = talk
	return slice
}

// GetTextCheck performs text validation and returns the result.
func (e *EditorService) GetTextCheck(req model.CheckTextRequest) model.CheckTextResponse {
	text, checked, msg := e.checkText(req.Speaker, req.Text)
	resp := model.CheckTextResponse{
		Text:    text,
		Checked: checked,
		Message: msg,
	}
	return resp
}
