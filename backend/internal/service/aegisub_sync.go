package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Aegisub 侧的同步宏：随导出写到 .ass 同目录。用户把它装进 Aegisub 的
// automation/autoload（或经 自动化管理器 手动加载）后，热键一按即可把轴机侧
// 推送的变更按 Effect 里的 st:<doc-id>:N 标识原位替换进当前打开的字幕，带撤销点。
// 反方向（Aegisub→轴机）不需要脚本：用户 Ctrl+S 保存后，后端监测到文件变化
// 会自动回读分隔时间。
const aegisubSyncScriptName = "SekaiText同步.lua"

const aegisubSyncScript = `-- SekaiText 同步宏（由 SekaiText 自动生成，可整个文件复制进 Aegisub 的 automation/autoload）
-- 用法：在轴机里点「推送到 Aegisub」后，于 Aegisub 中运行 自动化 → SekaiText → 从轴机拉取。
script_name = "SekaiText"
script_description = "与 SekaiText 轴机同步（带文档、版本和内容前置条件）"
script_author = "SekaiText"
script_version = "3.0"

local META_DOCUMENT = "SekaiText Document ID"
local META_REVISION = "SekaiText Revision"
local META_HASH = "SekaiText Content Hash"

local function path_sep()
    return package.config:sub(1, 1)
end

local function current_document(subs)
    local docs, doc_count, legacy = {}, 0, false
    for i = 1, #subs do
        local line = subs[i]
        if line.class == "dialogue" and line.effect then
            local doc = line.effect:match("^st:([%w_-]+):%d+$")
            if doc and not docs[doc] then
                docs[doc] = true
                doc_count = doc_count + 1
            elseif line.effect:match("^st:%d+$") then
                legacy = true
            end
        end
    end
    if legacy then return nil, "旧 st:N 不具备安全同步前置条件；请从 SekaiText 重新导出。" end
    if doc_count ~= 1 then return nil, "当前 ASS 没有唯一的 SekaiText document ID，已拒绝拉取。" end
    for doc in pairs(docs) do return doc end
end

local function find_sync_file(subs)
    local dir = aegisub.decode_path("?script")
    if not dir or dir:sub(1, 1) == "?" then return nil, "请先保存并打开字幕文件，再执行拉取。" end
    local doc, doc_err = current_document(subs)
    if doc_err then return nil, doc_err end
    local candidate = dir .. path_sep() .. "_sekaitext." .. doc .. ".sekaisync.txt"
    local fh = io.open(candidate, "r")
    if not fh then return nil, "没有当前文档对应的同步文件：" .. candidate end
    fh:close()
    return candidate, nil, doc
end

local function ass_time_to_ms(s)
    local h, m, sec, cs = s:match("^(%d+):(%d+):(%d+)%.(%d+)$")
    if not h then return 0 end
    return ((tonumber(h) * 60 + tonumber(m)) * 60 + tonumber(sec)) * 1000 + tonumber(cs) * 10
end

local function parse_event(kind, rest)
    local fields, pos = {}, 1
    for i = 1, 9 do
        local c = rest:find(",", pos, true)
        if not c then return nil end
        fields[i], pos = rest:sub(pos, c - 1), c + 1
    end
    return {
        class = "dialogue", comment = (kind == "Comment"),
        layer = tonumber(fields[1]) or 0,
        start_time = ass_time_to_ms(fields[2]), end_time = ass_time_to_ms(fields[3]),
        style = fields[4], actor = fields[5],
        margin_l = tonumber(fields[6]) or 0, margin_r = tonumber(fields[7]) or 0,
        margin_t = tonumber(fields[8]) or 0, margin_b = tonumber(fields[8]) or 0,
        effect = fields[9], text = rest:sub(pos), section = "[Events]", extra = {},
    }
end

local function add_group(groups, order, parsed, current_doc)
    if not parsed or not parsed.effect:match("^st:" .. current_doc .. ":%d+$") then
        return false
    end
    local tag = parsed.effect
    if not groups[tag] then
        groups[tag] = {}
        if order then order[#order + 1] = tag end
    end
    table.insert(groups[tag], parsed)
    return true
end

local function same_event(a, b)
    return a.class == "dialogue" and b.class == "dialogue"
        and (not not a.comment) == (not not b.comment)
        and (a.layer or 0) == (b.layer or 0)
        and (a.start_time or 0) == (b.start_time or 0)
        and (a.end_time or 0) == (b.end_time or 0)
        and (a.style or "") == (b.style or "")
        and (a.actor or "") == (b.actor or "")
        and (a.margin_l or 0) == (b.margin_l or 0)
        and (a.margin_r or 0) == (b.margin_r or 0)
        and (a.margin_t or 0) == (b.margin_t or 0)
        and (a.margin_b or 0) == (b.margin_b or 0)
        and (a.effect or "") == (b.effect or "")
        and (a.text or "") == (b.text or "")
end

local function current_group_indexes(subs, tag)
    local indexes = {}
    for i = 1, #subs do
        local line = subs[i]
        if line.class == "dialogue" and line.effect == tag then
            indexes[#indexes + 1] = i
        end
    end
    return indexes
end

local function metadata(subs)
    local values, indices = {}, {}
    for i = 1, #subs do
        local line = subs[i]
        if line.class == "info" then
            if line.key == META_DOCUMENT or line.key == META_REVISION or line.key == META_HASH then
                values[line.key], indices[line.key] = line.value, i
            end
        end
    end
    return values, indices
end

local function update_metadata(subs, indices, revision, content_hash)
    local revision_line = subs[indices[META_REVISION]]
    revision_line.value = tostring(revision)
    subs[indices[META_REVISION]] = revision_line
    local hash_line = subs[indices[META_HASH]]
    hash_line.value = content_hash
    subs[indices[META_HASH]] = hash_line
end

local function pull(subs, _sel)
    local sync_file, err, current_doc = find_sync_file(subs)
    if not sync_file then aegisub.log(0, (err or "未知错误") .. "\n"); aegisub.cancel() end

    local fh = io.open(sync_file, "r")
    if not fh then aegisub.log(0, "无法读取同步文件：" .. sync_file .. "\n"); aegisub.cancel() end
    local payload = { expected = {}, replacement = {}, order = {} }
    local section = nil
    for line in fh:lines() do
        line = line:gsub("\r$", "")
        local value
        value = line:match("^; document%-id: ([%w_-]+)$")
        if value then payload.document = value end
        value = line:match("^; expected%-revision: (%d+)$")
        if value then payload.expected_revision = tonumber(value) end
        value = line:match("^; expected%-content%-hash: ([0-9a-f]+)$")
        if value then payload.expected_hash = value end
        value = line:match("^; replacement%-revision: (%d+)$")
        if value then payload.replacement_revision = tonumber(value) end
        value = line:match("^; replacement%-content%-hash: ([0-9a-f]+)$")
        if value then payload.replacement_hash = value end
        if line == "; begin-expected" then section = "expected"
        elseif line == "; end-expected" then section = nil
        elseif line == "; begin-replacement" then section = "replacement"
        elseif line == "; end-replacement" then section = nil
        elseif section then
            local kind, rest = line:match("^(Dialogue): (.*)$")
            if not kind then kind, rest = line:match("^(Comment): (.*)$") end
            if kind then
                local order = section == "replacement" and payload.order or nil
                if not add_group(payload[section], order, parse_event(kind, rest), current_doc) then
                    fh:close(); aegisub.log(0, "同步文件含有无效或跨文档事件，已拒绝拉取。\n"); aegisub.cancel()
                end
            end
        end
    end
    fh:close()

    if payload.document ~= current_doc or not payload.expected_revision or not payload.replacement_revision
        or not payload.expected_hash or #payload.expected_hash ~= 64
        or not payload.replacement_hash or #payload.replacement_hash ~= 64 or #payload.order == 0 then
        aegisub.log(0, "同步文件缺少有效的文档、revision、hash 或事件，已拒绝拉取。\n")
        aegisub.cancel()
    end

    local meta, meta_indices = metadata(subs)
    if meta[META_DOCUMENT] ~= current_doc
        or tonumber(meta[META_REVISION]) ~= payload.expected_revision
        or meta[META_HASH] ~= payload.expected_hash
        or not meta_indices[META_REVISION] or not meta_indices[META_HASH] then
        aegisub.log(0, "当前 ASS 的同步 revision/hash 已变化；请丢弃旧 sidecar 并重新推送。\n")
        aegisub.cancel()
    end

    local existing = {}
    for i = 1, #subs do
        local line = subs[i]
        if line.class == "dialogue" and line.effect
            and line.effect:match("^st:" .. current_doc .. ":%d+$") then
            if not existing[line.effect] then existing[line.effect] = {} end
            table.insert(existing[line.effect], i)
        end
    end

    -- Every selected group must still exactly equal the disk content observed by
    -- the backend when it produced this sidecar. This catches unsaved Aegisub edits
    -- which a whole-file on-disk hash cannot see.
    for _, tag in ipairs(payload.order) do
        local expected, idxs = payload.expected[tag], existing[tag]
        if not expected or not idxs or #expected ~= #idxs then
            aegisub.log(0, "当前 ASS 的组 " .. tag .. " 已变化，未应用任何内容。\n")
            aegisub.cancel()
        end
        for i = 1, #expected do
            if not same_event(expected[i], subs[idxs[i]]) then
                aegisub.log(0, "当前 ASS 的组 " .. tag .. " 已被编辑，未应用任何内容。\n")
                aegisub.cancel()
            end
        end
    end

    -- Every group has now passed its precondition before any mutation. Re-scan
    -- indexes immediately before each replacement: selected groups can interleave,
    -- so a size change in one group may shift rows belonging to every later group.
    local replacements = {}
    for _, tag in ipairs(payload.order) do
        replacements[#replacements + 1] = {
            tag = tag, new_lines = payload.replacement[tag],
        }
    end

    local replaced = 0
    for _, replacement in ipairs(replacements) do
        local idxs = current_group_indexes(subs, replacement.tag)
        local new_lines = replacement.new_lines
        local n = math.min(#idxs, #new_lines)
        for i = 1, n do subs[idxs[i]] = new_lines[i] end
        if #new_lines > #idxs then
            for i = #idxs + 1, #new_lines do subs.insert(idxs[#idxs] + (i - #idxs), new_lines[i]) end
        elseif #idxs > #new_lines then
            for i = #idxs, #new_lines + 1, -1 do subs.delete(idxs[i]) end
        end
        replaced = replaced + 1
    end
    local _, updated_meta_indices = metadata(subs)
    update_metadata(subs, updated_meta_indices, payload.replacement_revision, payload.replacement_hash)
    aegisub.set_undo_point("SekaiText 拉取")

    -- A sidecar is a one-shot compare-and-swap request. Remove it only after all
    -- groups and metadata were applied; truncate as a fail-safe if removal fails.
    local removed = os.remove(sync_file)
    if not removed then
        local consumed = io.open(sync_file, "w")
        if consumed then consumed:write("; SekaiText sync consumed\n"); consumed:close()
        else aegisub.log(0, "警告：同步已应用，但 sidecar 无法删除；其 revision/hash 已失效。\n") end
    end
    aegisub.log(1, ("SekaiText 拉取完成：更新 %d 组"):format(replaced) .. "\n")
end

aegisub.register_macro(script_name .. "/从轴机拉取", "仅在组内容、revision 和 hash 均匹配时应用", pull)
`

// WriteAegisubSyncScript 把同步宏写到目录下（幂等覆盖），返回完整路径。
func WriteAegisubSyncScript(dir string) (string, error) {
	p := filepath.Join(dir, aegisubSyncScriptName)
	if err := WriteFileAtomic(p, []byte(aegisubSyncScript), 0644); err != nil {
		return "", err
	}
	return p, nil
}

// AegisubSyncPath returns the deterministic sidecar for one document. Modern
// files are discoverable from Effect alone, including when LuaFileSystem is not
// available. Legacy files remain tied to the ASS filename and require a unique
// legacy ASS in that directory before either side may use them.
func AegisubSyncPath(assPath, documentID string) string {
	if assPath == "" {
		return ""
	}
	if documentID == "" {
		return assPath + ".sekaisync.txt"
	}
	return filepath.Join(filepath.Dir(assPath), "_sekaitext."+documentID+".sekaisync.txt")
}

const (
	aegisubMetaDocument = "SekaiText Document ID"
	aegisubMetaRevision = "SekaiText Revision"
	aegisubMetaHash     = "SekaiText Content Hash"
)

type AegisubSyncMetadata struct {
	DocumentID  string
	Revision    uint64
	ContentHash string
}

// EmbedAegisubSyncMetadata puts the compare-and-swap token in [Script Info].
// The hash describes the logical engine export before these metadata lines are
// inserted, avoiding a self-referential whole-file hash.
func EmbedAegisubSyncMetadata(content string, metadata AegisubSyncMetadata) (string, error) {
	if !syncDocumentIDRe.MatchString(metadata.DocumentID) {
		return "", fmt.Errorf("无效的同步 document ID %q", metadata.DocumentID)
	}
	if !validSyncHash(metadata.ContentHash) {
		return "", fmt.Errorf("无效的同步 content hash")
	}

	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	hadTrailingNewline := strings.HasSuffix(normalized, "\n")
	lines := strings.Split(strings.TrimSuffix(normalized, "\n"), "\n")
	scriptInfo := -1
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "[Script Info]") {
			scriptInfo = i
			break
		}
	}
	if scriptInfo < 0 {
		return "", fmt.Errorf("ASS 内容缺少 [Script Info] 小节")
	}

	filtered := make([]string, 0, len(lines)+3)
	filtered = append(filtered, lines[:scriptInfo+1]...)
	filtered = append(filtered,
		aegisubMetaDocument+": "+metadata.DocumentID,
		aegisubMetaRevision+": "+strconv.FormatUint(metadata.Revision, 10),
		aegisubMetaHash+": "+metadata.ContentHash,
	)
	for _, line := range lines[scriptInfo+1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, aegisubMetaDocument+":") ||
			strings.HasPrefix(trimmed, aegisubMetaRevision+":") ||
			strings.HasPrefix(trimmed, aegisubMetaHash+":") {
			continue
		}
		filtered = append(filtered, line)
	}
	out := strings.Join(filtered, newline)
	if hadTrailingNewline {
		out += newline
	}
	return out, nil
}

func ParseAegisubSyncMetadata(content string) (AegisubSyncMetadata, error) {
	var metadata AegisubSyncMetadata
	inScriptInfo := false
	revisionSet := false
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inScriptInfo = strings.EqualFold(trimmed, "[Script Info]")
			continue
		}
		if !inScriptInfo {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case aegisubMetaDocument:
			metadata.DocumentID = strings.TrimSpace(value)
		case aegisubMetaRevision:
			revision, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return AegisubSyncMetadata{}, fmt.Errorf("无效的 SekaiText revision: %w", err)
			}
			metadata.Revision = revision
			revisionSet = true
		case aegisubMetaHash:
			metadata.ContentHash = strings.TrimSpace(value)
		}
	}
	if !revisionSet || !syncDocumentIDRe.MatchString(metadata.DocumentID) || !validSyncHash(metadata.ContentHash) {
		return AegisubSyncMetadata{}, fmt.Errorf("ASS 缺少有效的 SekaiText 同步元数据")
	}
	return metadata, nil
}

type AegisubSyncPayload struct {
	DocumentID             string
	ExpectedRevision       uint64
	ExpectedContentHash    string
	ReplacementRevision    uint64
	ReplacementContentHash string
	ExpectedLines          []string
	ReplacementLines       []string
}

// FormatAegisubSyncPayload builds a one-shot compare-and-swap request. The macro
// verifies metadata and every expected event before replacing any group.
func FormatAegisubSyncPayload(payload AegisubSyncPayload) (string, error) {
	if !syncDocumentIDRe.MatchString(payload.DocumentID) {
		return "", fmt.Errorf("无效的同步 document ID %q", payload.DocumentID)
	}
	if !validSyncHash(payload.ExpectedContentHash) || !validSyncHash(payload.ReplacementContentHash) {
		return "", fmt.Errorf("无效的同步 content hash")
	}
	if len(payload.ExpectedLines) == 0 || len(payload.ReplacementLines) == 0 {
		return "", fmt.Errorf("同步 payload 缺少 expected/replacement 事件")
	}
	header := "; SekaiText sync v3\n; document-id: " + payload.DocumentID +
		"\n; expected-revision: " + strconv.FormatUint(payload.ExpectedRevision, 10) +
		"\n; expected-content-hash: " + payload.ExpectedContentHash +
		"\n; replacement-revision: " + strconv.FormatUint(payload.ReplacementRevision, 10) +
		"\n; replacement-content-hash: " + payload.ReplacementContentHash + "\n"
	return header + "; begin-expected\n" + strings.Join(payload.ExpectedLines, "\n") +
		"\n; end-expected\n; begin-replacement\n" + strings.Join(payload.ReplacementLines, "\n") +
		"\n; end-replacement\n", nil
}

func validSyncHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, r := range hash {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// aegisubAutoloadDir 返回本机 Aegisub 的 automation/autoload 目录；探测不到
// Aegisub 配置根目录（说明没装或从未运行过）时返回空串，不凭空创建别家应用的
// 配置树。arch1t3cht 分支与官方版共用同一配置目录。
func aegisubAutoloadDir() string {
	var root string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, "Library", "Application Support", "Aegisub")
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return ""
		}
		root = filepath.Join(appdata, "Aegisub")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".aegisub")
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return ""
	}
	return filepath.Join(root, "automation", "autoload")
}

// InstallAegisubSyncMacro 尽力把同步宏直接装进 Aegisub 的 autoload 目录，
// 让「自动化 → SekaiText → 从轴机拉取」开箱即用（重启 Aegisub 后生效）。
// overrideDir 非空时优先（便携版/自定义安装位置探测不到，用户在插件里指一次
// automation/autoload 目录即可）；为空则自动探测。返回安装路径；探测不到且
// 未指定时返回空串且不算错误。
func InstallAegisubSyncMacro(overrideDir string) (string, error) {
	dir := strings.TrimSpace(overrideDir)
	if dir == "" {
		dir = aegisubAutoloadDir()
	}
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return WriteAegisubSyncScript(dir)
}
