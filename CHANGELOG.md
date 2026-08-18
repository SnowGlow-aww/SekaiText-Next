# SekaiText Next v5.9.13 更新日志

> **地图对话拉取修复与文稿命名优化**：修复地图对话在 Haruki / Moesekai 源下的 404 与时间索引问题，优化输入章节译名时的默认保存文件名。

## 🗺️ 地图对话（初始 / 升级 / 追加）拉取与下载修复
- **CDN 存储节点更新**：Haruki 资产源切换为完整的官方存储节点（支持全部 actionset 地图对话剧情），并在下载安全白名单中放行，彻底解决地图对话拉取与下载报 404 的问题；
- **追加地图对话按时间索引优化**：修复下拉框全部显示占位符 `time` 的问题，现已准确解析并展示包含该追加对话的官方活动期数与标题（按最新活动倒序排列）；
- **三维联动筛选与下载**：完善地图对话按人物、按时间、按地点的三维联动筛选，在下载页选择特定筛选或全部章节时精准对应匹配的对话话数，不再报错 404。

## 📝 章节译名默认保存文件名优化
- **规范直接替换**：在编辑器右上角输入章节标题译名时，默认保存文件名直接采用 `【模式】<剧情前缀> <章节译名>.txt`（例如 `【翻译】3rd-group5-06 属于你的斗争方式.txt`），去除了冗余的原日文标题与 `【标题】` 标记；
- **向后兼容**：继续兼容解析历史已保存的带 `【标题】` 标记文稿。

---

# SekaiText Next v5.9.12 更新日志

> **保存模式与文稿归档管理更新**：设置页新增静默保存与确认保存切换滑块，并在文稿保存路径区域新增「导入」按钮，支持将外部文稿一键扫描并自动分级归档。

## 💾 保存方式自选滑块
- 设置页【文件保存】新增「保存方式」滑块：
  - **静默保存（默认）**：若文档已有绑定路径或自动分层归档路径，手动点击保存直接写入目标文件并提示已保存，不弹出系统文件选择框；
  - **确认保存**：每次点击保存均弹出系统文件保存框，方便手动重选路径或文件名。
  - 覆盖保护依然保留：无论哪种模式，若检测到目标已存在内容不同的文件，均会提示确认，防止误覆盖。

## 📥 文稿保存路径【导入】功能
- 设置页【文稿保存路径】新增「导入」按钮，支持选择任意包含文稿的文件夹。
- 自动递归扫描所有 `.txt` 文稿，并通过剧情标识反解引擎自动识别所属活动/主线/卡面，自动创建目录归类到 `<文稿保存路径>/故事类型/编号 活动名/【模式】文件名.txt`。
- 幂等与防覆盖保护：内容一致的文件自动跳过，内容不同的同名文件自动加 ` (2)` 后缀安全保存，未识别文稿归入 `未分类/`。

---

# SekaiText Next v5.9.11 更新日志

> **独立剧情 TXT 导出修复**：导出文件现在完全遵循编辑器的翻译稿格式，可直接重新导入，并完整保留原文标点。

## 🗂️ 独立下载页 TXT 导出
- 导出内容统一使用编辑器的 `【翻译】` 格式和默认文件命名规则：`【翻译】<SaveTitle> <ChapterTitle>.txt`。
- 对话续行正确写出字面量 `\\N`，导出的 TXT 可以重新导入编辑器。
- 原文导出改用日文原文模式，完整保留原始标点、符号和多行内容，不再被中文标点规范化逻辑改写。
- 补充多行、`\\N` 和标点保留的回归测试。

---

# SekaiText Next v5.9.10 更新日志

> **文稿与自动轴机工作流安全修复**：阻止翻译、校对与合意稿静默覆盖，修复剧情资源目录、压制命名、staff 导出、Aegisub 回读提示和三行分句复查。

## 💾 文稿保存安全
- 翻译、校对和合意稿只在用户点击“保存”后写入正式文稿；恢复快照继续自动保存，但不再静默覆盖用户选择的 TXT 文件。
- 每次保存均打开原生保存框，可自定义文件名和目录；已绑定的自定义路径会作为下一次保存的默认位置，未指定时使用规范默认名。
- 写入前逐字节比较现有文件：内容相同则直接视为已保存；内容不同时明确提示“此操作会覆盖原有文件”。
- 覆盖确认绑定现有文件的 SHA-256；如果文件在确认后又被其他程序修改，会要求重新确认，避免使用过期确认覆盖新内容。

## 🗂️ 剧情 JSON 与原文导出
- JSON 下载页直接以持久化设置作为当前输出目录，选择或输入目录后不再被页面旧状态覆盖回默认目录。
- 开始 JSON 下载或原文 TXT 导出前强制完成设置写入，并为整批任务锁定同一个目录，避免多章节被分散到不同位置。

## 🎬 AutoTiming 3.2.9
- 压制输出名在选择视频后立即按源视频派生，例如 `video.mp4` → `video_subbed.mp4`；用户手动指定输出后保持手动路径。
- staff 每个字段增加独立勾选，未勾选不输出、勾选留空输出默认职位、填写后输出自定义内容；轴校与压制人员拆分，并补充制作组与内容标题示例。
- staff 导出替换已有模板行，修复重复 Dialogue 和“字幕制作 by 字幕制作 by …”；全部未勾选时也会移除模板示例。
- Aegisub 自动回读改为静默单飞与指数退避；遇到缺少当前任务 `revision/hash` 的 ASS 时暂停该文件的自动回读，直到文件变化、重新导出或切换任务，不再反复弹错或发送无意义请求。
- 三行分句复查可在关闭后通过“复查三行分句”重新打开；已修正、不再属于过长行的对话仍保留在复查列表。

## ✅ 验证
- Host 31 个 Vitest 文件共 182 项测试、56 项 Node 发布/平台测试全部通过，`vue-tsc` 与 Vite 生产构建通过。
- Go 后端全量测试、重点包 race 测试与 `go vet` 通过；SekaiCoreEngine Release 构建 0 警告、0 错误。
- AutoTiming 18 项测试、生产构建与确定性插件打包通过；最终补丁通过 `git diff --check`。

## English Summary
- Restored explicit Save-picker semantics for translation, proofreading, and consensus documents; recovery snapshots no longer overwrite formal TXT files.
- Added byte comparison, SHA-256-bound overwrite confirmation, and stale-confirmation detection while preserving custom filenames and locations.
- Persisted and snapshotted the JSON/TXT output directory for complete multi-chapter batches.
- Released AutoTiming 3.2.9 with source-video-derived output names, per-field staff inclusion, separate timing-QC/encoding roles, idempotent staff export, silent and permanently blocked invalid ASS sync retries, and reopenable three-line split review.
- Passed the complete Host, Go, AutoTiming, Engine, type-check, production-build, race, vet, and release-regression gates.

---

# SekaiText Next v5.9.9 更新日志

> **投入使用前稳定性修复**：将文件打开、剧情恢复和合意稿导入统一改成候选事务，并集中修复桌面 Live2D 黑屏/无声、剧情跳转、恢复快照和自动轴机生命周期问题。

## 📝 文档与剧情事务
- 打开翻译文件时，文件解析、剧情身份反解、原文加载、行对齐和文本比较全部在本地候选状态完成；所有步骤成功前不再清空当前文档、原文、路径或撤销状态。
- 任何失败、空译文、空原文、缺失 `scenarioId`、空对齐结果或过期异步请求都会 fail-closed，不再显示虚假的“已打开”。
- 编辑器内容、原文、剧情身份、标题、路径、metadata、撤销栈和恢复状态改为一次性原子提交。
- 合意稿导入和自动恢复使用同一套候选校验；内嵌 metadata 与当前章节或场景不一致时直接拒绝，清理恢复快照失败时保留旧文档并等待重试。

## 🧭 通用剧情身份反解
- 覆盖普通活动、World Link、主线、活动/联动/生日/Festival/初始/升级卡面、三类地图对话和特殊剧情，不再针对单个文件名写特判。
- `event211-airi 前篇` 可精确复用当前已载入的活动卡面剧情，也可在冷启动时反解到活动卡面 211 的真实章节。
- 当前剧情复用要求完整 canonical identity 和 `scenarioId` 一致；天然有歧义的旧文件名、Festival 卡面碰撞和虚拟歌手升级卡隐藏坐标全部 fail-closed，避免静默绑定到错误原文。
- 主线导航值和加载坐标统一，区域对话初始/升级/追加边界按真实 catalog 分类。

## 🎭 Live2D 桌面播放器
- 修复 WebP 背景和 MP3 语音/BGM 通过桌面代理加载时可能黑屏或无声的问题，并为无扩展名代理音频显式指定格式和超时清理。
- 剧情源、章节和跳转请求纳入完整播放 identity；切换来源、快速连续跳转或重复点击时，不再沿用旧剧情或把过期异步结果写回新舞台。
- 加强 PIXI/Cubism 舞台尺寸、keep-alive、暂停/恢复、自动播放、模型加载、BGM/语音和纹理生命周期清理，避免旧 ticker、模型或循环音频泄漏。
- 编辑器行跳转在剧情或 `scenarioId` 缺失时给出明确错误，不再静默无响应。
- 内置 Live2D 插件同步至 `1.3.2`，最低主程序版本为 `5.9.9`。

## 🎬 自动轴机与恢复可靠性
- 自动轴机轮询、任务切换、页面离开和完成回调加入 generation 防护，旧任务响应不能覆盖新任务或重复弹出完成提示。
- 修复长行分轴帧未正确落地以及 TimingSelfTest 假绿；真实 780 帧视频自测验证快速连续台词起始帧和分轴边界，误差不超过 1 帧。
- Android 在文档事务繁忙期间产生的首次脏修改不再丢失恢复快照；IndexedDB 清理失败会保留待重试状态，而不是误报成功。

## ✅ 验证
- 主程序 178 项 Vitest 与 55 项发布/平台脚本测试通过，`vue-tsc`、Vite 生产构建、Go race 测试和 `go vet` 全部通过。
- generation 21 生产 catalog 完整矩阵通过；1915 项独立 identity round-trip 为 0 mismatch，33 个 Festival 碰撞和 18 个虚拟歌手升级卡碰撞全部 fail-closed。
- Live2D 13 项测试、类型检查、生产构建和桌面实机模型/BGM/语音验证通过；AutoTiming 13 项测试、类型检查、生产构建和 TimingSelfTest 通过。

## English Summary
- Reworked file opening, recovery, and baseline import into fail-closed candidate transactions that publish editor state only after translation, source rows, story identity, alignment, comparison, and recovery cleanup all succeed.
- Added production-wide story identity resolution for every supported story type while refusing ambiguous legacy, Festival, area-talk, and virtual-singer upgrade-card identities.
- Fixed desktop Live2D background/audio proxying, source-aware jumps, rapid story switching, keep-alive behavior, and PIXI/Cubism/Howler lifecycle cleanup; the bundled plugin is now 1.3.2 and requires host 5.9.9.
- Hardened auto-timing task generations and separator estimation, recovery scheduling, and IndexedDB cleanup failure handling.
- Passed the full frontend, backend race/vet, production catalog matrix, Live2D, auto-timing, and frame-level timing self-test gates.

---

# SekaiText Next v5.9.8 更新日志

> **P0 团队模式热修复**：修复 5.9.7 无法登录使用 Caddy 内部 CA 的官方团队术语服务器的问题。

## 👥 团队模式
- 修复连接 `https://8.140.254.217:8443` 时出现 `Caddy Local Authority ... certificate is not trusted`，导致登录与只读连接在 TLS 握手阶段被阻断的问题。
- 随应用内置官方服务器的**公开根证书**，并只对官方服务器的精确来源地址启用；Caddy 的短期叶证书和中间证书正常轮换后仍可继续验证。
- 失败期间账号密码并未发送到远程服务器：证书校验发生在 HTTP 登录请求之前。

## 🔒 安全边界
- 没有恢复 `InsecureSkipVerify`，也没有全局跳过证书校验。
- 继续验证 TLS 证书链、服务器 IP SAN、有效期和主机名，并继续禁止携带凭据的请求跨来源重定向。
- 内置 CA 不会用于其他端口、相似域名或第三方团队服务器；自建私有 CA 仍需由操作系统或用户显式信任。
- 安装包只包含公开的 `root.crt`，不包含 Caddy 根私钥或任何服务器密钥。

## ✅ 验证
- 使用内置根证书通过真实 Go `TeamService` 连接官方服务器，并成功读取当前术语库版本。
- 官方根证书 SHA-256 指纹固定为 `D2:E9:B3:6C:33:7E:72:03:D3:0B:87:41:AB:52:38:22:FE:35:B0:D3:1E:6D:C2:51:9D:5F:99:8C:0F:19:09:25`。
- Go race 测试、`go vet`、后端构建、前端与发布脚本测试、生产构建和 npm 安全审计全部通过。

## English Summary
- Fixed the 5.9.7 team-mode regression that rejected the official glossary server's Caddy internal CA before login.
- Bundled only the official server's public root certificate and scoped it to the exact `https://8.140.254.217:8443` origin.
- Kept full certificate-chain, hostname, validity, and redirect verification; TLS verification is never disabled globally.
- Confirmed with a live Go `TeamService` connection that the official server validates successfully without sending credentials through an untrusted channel.

---

# SekaiText Next v5.9.7 更新日志

> 自动轴机兼容性与稳定性修复版本：取消翻译文本对角色命名、冒号格式和行数的强制匹配，并确保打轴模板、字体与阈值资源全程使用安装包内置文件。

## 🎬 自动轴机兼容性
- **取消翻译硬校验**：翻译角色名不需要与日文 scenario 一致；是否使用全角冒号、对话/效果行类型和总行数也不再作为启动打轴的门槛。
- **按现有内容尽力套用**：有角色名的翻译会更新角色名与正文；无角色名的行只更新正文并保留日文原名；缺失行保留原文，多余行忽略，类型不一致时也不会崩溃。
- **修正错误分类**：翻译格式或内容差异不再被包装成“模板/字体资源缺失且无法联网下载”，实际异常会按原原因返回。

## 📦 内置资源与内核
- 自动轴机的字体、模板、菜单图与阈值清单继续随 **SekaiCoreEngine** 内置发布；启动时只进行本地文件大小与 MD5 校验，**不会联网下载资源**。
- 若内置资源确实缺失或损坏，应用会明确提示重新安装或更新内核，而不是要求联网补下载。
- 内置 **SekaiCoreEngine 2.3.11**，主程序发布工作流固定到经过验证的引擎提交，避免重新运行工作流时意外打入不同版本。

## 🛠️ 稳定性、安全与平台维护
- 加强编辑器聚焦草稿、自动保存、页面离开和 IME 组合输入的状态协调，避免未失焦内容丢失、重复提交或旧异步结果覆盖新文档。
- 加固团队模式、术语库同步、应用更新、内置 Live2D 资源和本地 IPC 的并发、权限、输入边界与资源生命周期处理。
- 补齐 Android 移动端运行时、原生桥接及 APK 构建/签名/验证工具链；本次公开桌面安装包仍为 macOS Apple Silicon 与 Windows x64。

## 📦 发布与更新
- 主程序、Tauri、Cargo 与后端版本统一更新至 `5.9.7`。
- macOS Apple Silicon 与 Windows x64 安装包发布到 GitHub Release 和 OSS/CDN。
- `app-release.json` 使用 Ed25519 签名，并记录安装包 SHA-256 与文件大小；官网与下载页同步到 `5.9.7`。

## ✅ 验证
- 主程序前端与发布脚本测试全部通过（93 项 Vitest、55 项 Node 测试），生产构建、官网 12 页面校验及 npm 安全审计通过。
- Go 后端与术语服务器通过 race 测试和 `go vet`；Tauri 通过锁定依赖 Rust 测试与 Release 检查。
- Android arm64 Release APK 实际构建成功，并通过签名状态、ABI、应用标识、版本、权限、网络安全、DEX 类与内嵌配置校验。
- SekaiCoreEngine 2.3.11 Release 构建和 LineEditSelfTest 通过，Core/Engine/App 的 NuGet 漏洞检查均为 0。
- AutoTiming、Live2D、插件市场及两份 Moe 镜像的测试、生产构建与依赖审计通过。

## English Summary
- Removed strict auto-timing translation matching. Speaker names, full-width colons, line types, and line counts no longer need to mirror the Japanese scenario.
- Translation lines are applied on a best-effort basis: missing lines keep the source text, extra lines are ignored, unnamed lines preserve the original speaker, and type mismatches no longer crash the run.
- VideoProcess fonts, templates, menu assets, and thresholds are validated exclusively from the bundled installation. Auto-timing never downloads these resources at startup.
- Bundled **SekaiCoreEngine 2.3.11** and pinned the release workflow to its verified commit.
- Included editor, team/glossary, update, Live2D, IPC, and Android runtime reliability and security improvements.
- Verified frontend and release tests, production and website builds, race-enabled Go suites, locked Rust checks, a real Android release APK, engine self-tests, and npm/NuGet advisory scans.

---

# SekaiText Next v5.9.0 更新日志

> 全应用工作台焕新，并集中修复编辑器文档切换、官网部署和正式发布链路中的数据安全与可靠性问题。

## 界面与体验
- 引入统一的应用壳层、侧栏导航、页面标题和内容层级，编辑器、设置、账号、术语库、插件市场等页面使用一致的工作台视觉语言。
- 编辑器原文/译文工作区改为更紧凑的连续表格布局；整份文档从完全透明快速渐显，退出动画更短，并兼容“减少动态效果”。
- 新手导览跟随新布局更新；聚光灯外区域不再误触导航，跨页面步骤会等待页面动画稳定后再定位。

## 编辑器稳定性
- 剧情原文和译文模板全部成功后才原子提交；任一请求失败时保留当前文档，不再出现“新原文配旧译文”的混合状态。
- 打开文件、载入剧情、切换模式和清空内容统一使用文档操作锁，避免异步响应覆盖用户正在编辑的内容。
- 行编辑请求绑定文档修订号，并在请求前后检查；旧文档的延迟响应不再写入新文档。
- 自动保存绑定文件路径时不再触发整块编辑区重建，避免尚未 blur 的 `contenteditable` 输入丢失。
- 所有模式中的未保存内容都会参与打开/载入确认。

## 官网与发布
- 官网生产路径固定为 `/web/`，部署按“资源先上传、HTML 后上传”执行，并校验页面、样式、动态 JS chunks 和全部构建产物。
- `verify:remote` 会先重建官网，错误的 `SITE_BASE` 会在上传前失败。
- GitHub Release 完成后自动生成并发布应用更新 manifest。
- macOS 内核、FFmpeg 与动态库在有 Developer ID 时使用正式身份签名；无证书构建继续使用经过完整校验的 ad-hoc 签名。
- npm、Tauri 与 Cargo 版本统一同步为 `5.9.0`。

---

# SekaiText Next v4.2.1 更新日志

> 紧急修复：自动轴机导出的字幕「只有样式、没有时间轴」（空 ass）。

## 🩹 修复
- **导出字幕为空**：内核构造打轴配置时，因 C# `default(struct)` 零初始化跳过了默认值，把所有导出开关置成了 false，导致每条字幕行在导出末尾被过滤掉——明明识别 162/162，导出的 ass 却空有样式、没有轴。已改为缺省即全部导出。
- 连带修复同一根因导致的：字幕**字体名为空**、**打字机动画时长为 0（失效）**。

---

# SekaiText Next v4.2.0 更新日志

> 自动轴机修复与增强：根治「连打多个视频时第二个无法导出 ass」、字幕按剧本名命名、可自定义字幕输出目录；并把全部「引擎」字样统一更名为「内核」(SekaiCoreEngine)。

## 🎬 自动轴机
- **根治「连打两个视频→第二个无法导出 ass」**：长·三行·有翻译的台词在导出时因分隔帧未初始化而崩溃(`Frames[..负数]`)，现已在导出前正确初始化分隔帧。
- **字幕按剧本名命名**：导出的字幕从 `timing-<随机串>.ass` 改为按剧本名，例如 `event_206_05.ass`。
- **可自定义字幕输出目录**：打轴区新增「字幕输出目录」，可选目录并记忆上次选择；留空则写入默认目录。
- **稳定性**：修复连打多个视频时「压制输出路径仍指向上一个视频」可能烧错文件；内核就绪状态在返回页面时自动复查(不再卡在禁用)；导出超时放宽至 3 分钟；连续打轴会释放上一段视频的句柄并清理跨运行匹配缓存。

## 🏷️ 「引擎」更名为「内核」
- 全部界面文案「引擎」统一改为「内核」；内置打轴/压制内核更名为 **SekaiCoreEngine**。

## 🗂️ JSON 下载
- 「输出目录」退出后会被记住，不必每次重填。

---

# SekaiText Next v4.1.4 更新日志

> 默认字体「荆南麦圆体」更新为新版字形（TTF）。并包含 v4.1.3 的端口自愈修复。

## 🔤 字体
- 内置默认字体「荆南麦圆体」更新为新版字形文件（TTF 替换旧 OTF）。仍可在设置里切换或导入自定义字体。

## 🩹 含 v4.1.3：自动修复端口冲突
- 新版启动自动清理遗留的旧版后端 + 端口绑定带重试，根治「装了新版却仍连到旧后端 / 检查更新 404」，一次装好后永久自愈。

---

# SekaiText Next v4.1.3 更新日志

> 彻底修复「装了新版却仍连到旧版后端、检查更新一直失败 / 日志里 `/app/update/check 404`」——根因是旧版没退干净、占着 9800 端口。现在新版启动会**自动接管端口**，无需任何手动操作。

## 🩹 自动修复端口冲突
- **启动自动清理遗留旧后端**：新版启动时先杀掉残留的旧版 sidecar，确保用的是自己（匹配的）后端；旧版没退干净也不会再让前端连到错误的旧后端。
- **后端绑定端口带重试**：平滑掉升级瞬间「旧进程刚被杀、端口还没释放」的竞态。
- 一次装好后永久自愈，再不用手动 `kill` 端口。

> 含 v4.1.2「检查更新」按钮 / 自动复查，以及 v4.1.1 全部安全加固与稳定性修复。

---

# SekaiText Next v4.1.2 更新日志

> 自动更新体验改进：设置内「检查更新」按钮 + 窗口回到前台自动复查，长期开着也能及时收到新版。

## 🔄 自动更新改进
- **设置 → 关于** 新增「检查更新」按钮：手动立即检查，结果即时提示（发现新版 / 已是最新 / 检查失败）。
- **窗口重新聚焦自动复查**（30 分钟节流）：长时间开着应用也能在切回时收到新版提示，不必重启。

> 含 v4.1.1 全部安全加固与稳定性修复（能力令牌、回环绑定、生命周期与并发修复等）。

---

# SekaiText Next v4.1.1 更新日志

> 安全加固与稳定性维护版本：能力令牌防护自更新链路、修复多处生命周期与并发隐患、清理死代码。

## 🔒 安全加固
- **能力令牌**：打包应用启动时由外壳生成一次性随机令牌，自更新等敏感操作必须携带，挡住网页跨源偷偷触发「改设置 → 下载 → 打开」链路；后端默认仅绑定本机回环（不再监听局域网）。
- **自更新更安全**：安装包下载源限定 GitHub 官方主机白名单；打开时仅允许安装包类型（.dmg/.pkg/.exe/.msi）并先解析软链接，杜绝被诱导打开任意文件。
- **术语库 / 插件**：远程同步加超时与重定向上限；本地导入加文件大小上限（防 xlsx 解压炸弹）；插件包安装加条目数与解压总量上限（防 zip 炸弹）。

## 🛠️ 稳定性修复
- 修复编辑器 / 调试页在 keep-alive 下的生命周期泄漏：离开编辑器后不再误触发快捷键与保存弹窗，自动保存与日志轮询不再后台空转。
- 修复启动时自更新 / 插件汇总为空导致的报错（返回空数组而非 null）。
- CDN 元数据刷新改为单飞，避免并发刷新导致的崩溃；下载改用唯一临时文件，避免同名并发下载互相污染缓存；下载收尾错误不再被吞。
- 外部启动器（打开文件夹 / 安装包）进程及时回收，避免僵尸进程堆积。

## 🧹 维护性
- 删除一批无引用的死代码（组件 / 类型 / 后端方法）。
- 插件启用状态写入加锁；镜像源可用 `SEKAITEXT_GH_PROXY` 环境变量切换或关闭。
- CI 增加版本号与发布 tag 一致性校验，移除清单生成中无效的构建分支。

---

**安装**：macOS 打开 `SekaiText Next_4.1.1_aarch64.dmg` 拖入「应用程序」；Windows 版由 GitHub Release 提供。本体内可直接「检查更新」一键升级。

# SekaiText Next v4.1.0 更新日志

> 自动更新上线（本体 + 插件）、GitHub 下载镜像加速、默认换装初音未来配色与内置荆南麦圆体，自动轴机插件完整可用。

## 🔄 自动检查更新
- **插件自动更新**：每次启动静默检查插件市场，把已安装插件升级到最新版（逐个失败不影响其它），更新到的会提示「重启后生效」。
- **本体自动更新**：启动自动检查新版本，发现后顶部弹出更新横幅 → 一键下载新安装包（带进度，存到「下载」文件夹）→ 一键打开安装。下载走镜像加速；安装包不会擅自下载，点了才下。

## 🚀 GitHub 下载镜像加速
- 插件市场的索引与插件包下载优先走 `ghfast.top` 镜像（短超时快速失败），不可用时自动回退 GitHub 官方源；自建源不受影响，插件包 sha256 校验照旧。

## 🎨 默认主题色 → 初音未来
- 默认主题色改为**初音未来代表色**（teal `#33ccbb`），移除原「品牌紫」默认色块；老用户的旧默认色启动时自动迁移到新默认。

## 🔤 内置默认字体
- **荆南麦圆体**随应用打包并设为默认 UI 字体（仍可在设置里切换其它字体或导入自定义字体）。

## 🎬 自动轴机插件（打轴 + 压制）
- 侧栏更名「自动轴机」；接通内置 SekaiToolsEngine 引擎，打轴 / 压制真正可用。
- 文件选择器、可调阈值参数、压制硬件解码加速开关、退出 / 返回按钮齐全；可视性与设计系统统一。
- 后端经纪人异步化：启动不再卡住界面、随时可取消；非法阈值在进引擎前被过滤，杜绝启动报错。

## 🖼️ 编辑器与界面打磨
- 编辑器支持个性化壁纸背景；原文 / 译文吸顶标题圆角与衔接处统一，消除割裂感。
- 打包后所有文件选择经由原生对话框（修复旧版「当前环境不支持文件选择」）。
- 一批 UI 一致性与可视性修复。

---

**安装**：macOS 打开 `SekaiText Next_4.1.0_aarch64.dmg`，拖入「应用程序」。Windows 版由 GitHub Release 提供。

<br>

---

# SekaiText Next v4.0.0 更新日志

> 一次彻底的界面重做 —— 全新设计系统、可换肤的推しカラー主题、字体自定义，以及全应用页面的统一焕新。

## ✨ 全新设计系统
- **完整的设计令牌体系**：统一的间距 / 圆角 / 柔和多层阴影 / 层级 / 文字层次 / 状态色，全应用一致。
- **两套精心调校的主题** + 跟随系统：
  - `sekai-light` —— 柔和冷调的近白画布、悬浮白卡片。
  - `sekai-dark` —— 深邃靛蓝夜色（非纯黑），高亮更有质感。
- 所有页面、组件、弹窗统一到这套视觉语言。

## 🎨 推しカラー主题色（换肤）
- 在「设置 → 外观」里挑选你推し的**官方代表色**，整个界面的主色（按钮、链接、焦点环、渐变、选区）**实时**换上它的颜色，并持久保存。
- 内置全 6 团 30 名角色代表色，按组合分组；默认是 **PJSK 多彩渐变**。

## 🔤 字体自定义
- 「设置 → 外观」新增**字体**选择：默认 / 系统 UI / 苹方 / 微软雅黑 / 思源黑体 / 圆体 / 楷体 / 宋体 / 等宽，共 9 种，全局生效、持久化。

## 🧑‍🎤 头像自选颜色
- 「账号中心 → 我的资料」可为自己的头像挑选背景色（角色色板 + 自定义取色 + 一键「自动」）。
- 颜色**存到团队服务器**，队友也能看到、刷新不丢；未设置者沿用按 ID 生成的稳定配色。

## 💬 主题化对话框
- 全应用约 17 处原生 `confirm()/prompt()` 全部替换为可主题化的对话框（支持回车 / Esc、危险操作高亮、删除需输入用户名二次确认等）。

## 🛡️ 三级团队权限模型
- 服务器账号（@admin）为**唯一超级管理员**，拥有全部权限。
- 其余管理员为新的「管理员」层级：**只能提升、不能降级**，不能禁用 / 修改其他管理员，无法触碰超管。
- UI 严格按服务器权威用户列表判定权限（修复了越权显示、下拉回弹等问题）。

## 🧩 全页面焕新
- 账号中心、设置、术语库、语法、JSON 下载、插件市场、调试、编辑器，以及全部面板 / 弹窗 / 导航 / Toast / 下载浮窗等组件统一改造；图标全面换为 Lucide。

## 🩹 体验打磨
- 相邻同类框体（输入框 / 下拉 / 按钮 / 角色徽章）宽度统一对齐。
- 下拉框去除原生箭头、改用自定义 chevron，与输入框等高。
- 刷新按钮旋转动画可见。
- 清除全应用硬编码灰色 / 颜色，深浅色下均正确。

---

**安装**：macOS 打开 `SekaiText Next_4.0.0_aarch64.dmg`，拖入「应用程序」。Windows 版由 GitHub Release 提供。
