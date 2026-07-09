package service

import (
	"os"
	"path/filepath"
	"runtime"
)

// Aegisub 侧的同步宏：随导出写到 .ass 同目录。用户把它装进 Aegisub 的
// automation/autoload（或经 自动化管理器 手动加载）后，热键一按即可把轴机侧
// 推送的变更按 Effect 里的 st:N 标识原位替换进当前打开的字幕，带撤销点。
// 反方向（Aegisub→轴机）不需要脚本：用户 Ctrl+S 保存后，后端监测到文件变化
// 会自动回读分隔时间。
const aegisubSyncScriptName = "SekaiText同步.lua"

const aegisubSyncScript = `-- SekaiText 同步宏（由 SekaiText 自动生成，可整个文件复制进 Aegisub 的 automation/autoload）
-- 用法：在轴机里点「推送到 Aegisub」后，于 Aegisub 中运行 自动化 → SekaiText → 从轴机拉取。
script_name = "SekaiText"
script_description = "与 SekaiText 轴机同步（按 Effect 的 st:N 标识替换行）"
script_author = "SekaiText"
script_version = "1.1"

local function path_sep()
    return package.config:sub(1, 1)
end

-- 找当前字幕文件所在目录里最新的 *.sekaisync.txt
local function find_sync_file()
    local dir = aegisub.decode_path("?script")
    if not dir or dir:sub(1, 1) == "?" then return nil, "请先保存并打开字幕文件，再执行拉取。" end
    local ok, lfs = pcall(require, "lfs")
    if not ok then return nil, "找不到 lfs 模块（Aegisub 自带，一般不会发生）。" end
    local newest, newest_t
    for f in lfs.dir(dir) do
        if f:match("%.sekaisync%.txt$") then
            local full = dir .. path_sep() .. f
            local t = lfs.attributes(full, "modification")
            if t and (not newest_t or t > newest_t) then
                newest, newest_t = full, t
            end
        end
    end
    if not newest then
        return nil, "本目录没有 .sekaisync.txt——先在 SekaiText 轴机里点「推送到 Aegisub」。"
    end
    return newest
end

local function ass_time_to_ms(s)
    local h, m, sec, cs = s:match("^(%d+):(%d+):(%d+)%.(%d+)$")
    if not h then return 0 end
    return ((tonumber(h) * 60 + tonumber(m)) * 60 + tonumber(sec)) * 1000 + tonumber(cs) * 10
end

-- 把同步文件里一行 "Dialogue: rest" 解析成 Aegisub 行表
local function parse_event(kind, rest)
    local fields = {}
    local pos = 1
    for i = 1, 9 do
        local c = rest:find(",", pos, true)
        if not c then return nil end
        fields[i] = rest:sub(pos, c - 1)
        pos = c + 1
    end
    local text = rest:sub(pos)
    return {
        class = "dialogue",
        comment = (kind == "Comment"),
        layer = tonumber(fields[1]) or 0,
        start_time = ass_time_to_ms(fields[2]),
        end_time = ass_time_to_ms(fields[3]),
        style = fields[4],
        actor = fields[5],
        margin_l = tonumber(fields[6]) or 0,
        margin_r = tonumber(fields[7]) or 0,
        margin_t = tonumber(fields[8]) or 0,
        margin_b = tonumber(fields[8]) or 0,
        effect = fields[9],
        text = text,
        section = "[Events]",
        extra = {},
    }
end

local function pull(subs, _sel)
    local sync_file, err = find_sync_file()
    if not sync_file then
        aegisub.log(0, (err or "未知错误") .. "\n")
        aegisub.cancel()
    end

    -- 读同步文件按 st: 标识分组
    local groups, order = {}, {}
    for line in io.lines(sync_file) do
        line = line:gsub("\r$", "")
        local kind, rest = line:match("^(Dialogue): (.*)$")
        if not kind then kind, rest = line:match("^(Comment): (.*)$") end
        if kind then
            local parsed = parse_event(kind, rest)
            if parsed and parsed.effect:match("^st:%S+$") then
                local tag = parsed.effect
                if not groups[tag] then
                    groups[tag] = {}
                    order[#order + 1] = tag
                end
                table.insert(groups[tag], parsed)
            end
        end
    end
    if #order == 0 then
        aegisub.log(0, "同步文件里没有带 st: 标识的行：" .. sync_file .. "\n")
        aegisub.cancel()
    end

    -- 当前字幕里按标识收集行号
    local existing = {}
    for i = 1, #subs do
        local l = subs[i]
        if l.class == "dialogue" and l.effect and l.effect:match("^st:%S+$") then
            if not existing[l.effect] then existing[l.effect] = {} end
            table.insert(existing[l.effect], i)
        end
    end

    local replaced, appended = 0, 0
    -- 逆序处理，避免前面的插入/删除改变后面组的行号
    for gi = #order, 1, -1 do
        local tag = order[gi]
        local new_lines = groups[tag]
        local idxs = existing[tag]
        if idxs and #idxs > 0 then
            local n = math.min(#idxs, #new_lines)
            for k = 1, n do
                subs[idxs[k]] = new_lines[k]
            end
            if #new_lines > #idxs then
                for k = #idxs + 1, #new_lines do
                    subs.insert(idxs[#idxs] + (k - #idxs), new_lines[k])
                end
            elseif #idxs > #new_lines then
                for k = #idxs, #new_lines + 1, -1 do
                    subs.delete(idxs[k])
                end
            end
            replaced = replaced + 1
        else
            for _, l in ipairs(new_lines) do
                subs.append(l)
            end
            appended = appended + 1
        end
    end

    aegisub.set_undo_point("SekaiText 拉取")
    aegisub.log(1, ("SekaiText 拉取完成：更新 %d 组"):format(replaced)
        .. (appended > 0 and ("，另有 %d 组未找到已追加到末尾"):format(appended) or "") .. "\n")
end

aegisub.register_macro(script_name .. "/从轴机拉取", "读取同目录的 .sekaisync.txt 并按 st: 标识替换行", pull)
`

// WriteAegisubSyncScript 把同步宏写到目录下（幂等覆盖），返回完整路径。
func WriteAegisubSyncScript(dir string) (string, error) {
	p := filepath.Join(dir, aegisubSyncScriptName)
	if err := os.WriteFile(p, []byte(aegisubSyncScript), 0644); err != nil {
		return "", err
	}
	return p, nil
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

// InstallAegisubSyncMacro 尽力把同步宏直接装进本机 Aegisub 的 autoload 目录，
// 让「自动化 → SekaiText → 从轴机拉取」开箱即用（重启 Aegisub 后生效）。
// 返回安装路径；未检测到 Aegisub 时返回空串且不算错误。
func InstallAegisubSyncMacro() (string, error) {
	dir := aegisubAutoloadDir()
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return WriteAegisubSyncScript(dir)
}
