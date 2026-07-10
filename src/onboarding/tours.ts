import type { TourDef, TourStep } from './useTour'
import { LINKS } from '../utils/openExternal'

/** 主应用首次启动导览（设置→关于 可随时重看）。 */
export function appWelcomeTour(): TourDef {
  return {
    id: 'app-welcome',
    steps: [
      {
        route: '/',
        title: '欢迎使用 SekaiText Next！',
        body: '这是一款面向 Project SEKAI 剧情翻译的一站式工具。\n我是雪莹ちゃん，就让我花一分钟带你了解下主要功能吧！',
      },
      {
        route: '/',
        selector: '[data-tour="story-nav"]',
        title: '选择剧情',
        body: '在这里选择剧情类型（活动 / 主线 / 卡面 / 区域对话…）、具体活动与章节，然后加载原文。首次使用会自动下载剧情索引。',
      },
      {
        route: '/',
        selector: '[data-tour="modes"]',
        title: '工作模式',
        body: '翻译、校对、合意三种模式随时切换。校对/合意模式支持导入他人稿件并逐行对比改动。',
      },
      {
        route: '/',
        selector: '[data-tour="toolbar"]',
        title: '工具栏',
        body: '打开 / 保存译文稿；闪回·术语·同步·搜索等视图开关；还有说话人检查、全文检查与字数统计。',
      },
      {
        route: '/',
        selector: '[data-tour="workspace"]',
        title: '编辑区',
        body: '原文与译文逐行对照，点击行内文本直接输入。行首头像自动标识说话人，语音行可点击试听。',
      },
      {
        route: '/',
        selector: '[data-tour="nav-glossary"]',
        title: '术语库',
        body: '团队术语随翻随查：编辑时悬停命中词条即可看到译法，支持云端同步协作。',
      },
      {
        route: '/',
        selector: '[data-tour="nav-market"]',
        title: '插件市场',
        body: 'Live2D 剧情播放器、自动轴机 / 压制等扩展功能都在这里安装，装完即用、独立更新。',
      },
      {
        route: '/',
        selector: '[data-tour="nav-settings"]',
        title: '设置',
        body: '所有偏好都集中在设置页——下一步带你进去逛一圈。',
      },
      {
        route: '/settings',
        selector: '[data-tour="set-appearance"]',
        title: '外观',
        body: '主题配色随心换；角色头像材质也能整套替换成团队自己的（选择包含 chr_1~chr_31.png 的文件夹即可）。',
      },
      {
        route: '/settings',
        selector: '[data-tour="set-editor"]',
        title: '编辑器偏好',
        body: '字号、撤销深度、索引排序、切换模式时是否保留剧情……按个人习惯调整。',
      },
      {
        route: '/settings',
        selector: '[data-tour="set-network"]',
        title: '网络与调试',
        body: '更新与插件下载源可选「国内 CDN 加速」或「GitHub 直连」，所选源优先、另一侧自动兜底。',
      },
      {
        route: '/settings',
        selector: '[data-tour="set-shortcuts"]',
        title: '快捷键',
        body: '常用操作全部可以自定义按键，点击对应条目直接录制。',
      },
      {
        route: '/settings',
        selector: '[data-tour="set-about"]',
        title: '关于',
        body: '检查更新、官网与 GitHub 入口都在这里；想重看本导览，随时点「新手导览」。',
      },
      {
        title: '导览结束！',
        body: '更完整的图文教程见官网指南。\n以后想重看本导览，请前往 设置 → 关于 → 新手导览。',
        link: { label: '打开官网指南', url: LINKS.guide },
      },
    ],
  }
}

/**
 * 术语库首次打开导览——按角色分层：
 *  - base（所有人）：检索 / 称呼查询 / 自动同步与编辑器联动，未登录时先引导去账号中心登录
 *  - 翻译（登录后）：添加词条、解锁编辑、提案跟踪——都是提案送审语义
 *  - 校对：审核提案（通过 / 驳回）与呼吸灯提醒
 *  - 管理员：整库上传、账号中心的团队账号管理
 * 只拼「当前角色可见且没看过」的层：首次打开一次连播；之后被 promote 时
 * 只单独补播新增权限那一层。返回 null = 全部看过，无事可做。
 * 带 selector 的步骤在元素不存在时由导览引擎自动跳过，权限门禁天然生效。
 */
export interface GlossaryTourCtx {
  loggedIn: boolean
  isReviewer: boolean
  isAdmin: boolean
  /** useTour().seen —— 判层用，避免这里反向依赖 store。 */
  seen: (id: string) => boolean
}

export function glossaryTour(ctx: GlossaryTourCtx): TourDef | null {
  const tiers: { id: string; steps: TourStep[] }[] = []

  if (!ctx.seen('glossary-intro')) {
    const base: TourStep[] = []
    if (!ctx.loggedIn) {
      base.push(
        {
          route: '/glossary',
          title: '欢迎来到术语库！',
          body: '术语库是团队共享的译名词典——人名、称呼、专有名词的标准译法都在这里，翻译时自动高亮命中。\n检测到你还没有登录团队服务器，先带你完成登录。',
        },
        {
          route: '/account',
          selector: '[data-tour="team-panel"]',
          title: '登录团队服务器',
          body: '在这里填写团队服务器地址与账号密码登录（账号找团队管理员开通）；也可以「只读连接」，免登录浏览并自动同步团队术语库。\n可以现在就登录，登好后点「下一步」继续。',
        },
        {
          route: '/glossary',
          title: '回到术语库',
          body: '接下来看看术语库的基础功能。没登录也可以先了解，之后随时能在账号中心补登录——登录后会再介绍对应权限的功能。',
        },
      )
    } else {
      base.push({
        route: '/glossary',
        title: '欢迎来到术语库！',
        body: '术语库是团队共享的译名词典——人名、称呼、专有名词的标准译法都在这里，翻译时自动高亮命中。\n花一分钟带你过一遍功能。',
      })
    }
    base.push(
      {
        route: '/glossary',
        selector: '[data-tour="glo-search"]',
        title: '检索与浏览',
        body: '输入原文或译文即可日⇄中双向模糊检索；右侧分类下拉可按分类整库浏览。',
      },
      {
        route: '/glossary',
        selector: '[data-tour="glo-tabs"]',
        title: '称呼查询',
        body: '第二个标签页是人称表：查任意两位角色之间的称呼（谁怎么叫谁），翻译对话时最常用。',
      },
      {
        title: '自动同步 & 编辑器联动',
        body: '登录或只读连接后，术语库每分钟自动与服务器同步，无需手动操作；每次同步到更新还会在本地留存最近 10 份备份。\n回到编辑器：原文中命中的词条会自动加下划线，悬停即可查看译法与角色称呼。',
      },
    )
    tiers.push({ id: 'glossary-intro', steps: base })
  }

  if (ctx.loggedIn && !ctx.seen('glossary-tier:member')) {
    tiers.push({
      id: 'glossary-tier:member',
      steps: [
        {
          route: '/glossary',
          selector: '[data-tour="glo-search"]',
          title: '添加词条（翻译）',
          body: '你已登录为团队用户。点「添加」提交新词条——会作为提案送管理员 / 校对审核，通过后全员自动同步。',
        },
        {
          route: '/glossary',
          selector: '[data-tour="glo-lock"]',
          title: '解锁编辑',
          body: '词条默认锁定防误改，点这里解锁后才能修改 / 删除；这些改动同样先作为提案送审，不会直接动到团队数据。',
        },
        {
          route: '/glossary',
          selector: '[data-tour="glo-proposals"]',
          title: '我的提案',
          body: '在这里跟踪自己提案的进度（待审核 / 已通过 / 已驳回）；提案被通过时，编辑器侧栏的「术语库」会亮起呼吸灯提醒你。',
        },
      ],
    })
  }

  if (ctx.isReviewer && !ctx.seen('glossary-tier:reviewer')) {
    tiers.push({
      id: 'glossary-tier:reviewer',
      steps: [
        {
          route: '/glossary',
          selector: '[data-tour="glo-proposals"]',
          title: '审核提案（校对）',
          body: '你拥有校对权限：提案面板里多了「待审核」标签页，可逐条通过或驳回（驳回需填理由）。\n有提案等待审核时，编辑器侧栏的「术语库」会亮呼吸灯提醒。',
        },
      ],
    })
  }

  if (ctx.isAdmin && !ctx.seen('glossary-tier:admin')) {
    tiers.push({
      id: 'glossary-tier:admin',
      steps: [
        {
          route: '/glossary',
          selector: '[data-tour="glo-upload"]',
          title: '整库上传（管理员）',
          body: '把本地术语库一键上传并完全替换线上版本（线上多出的条目会被删除，上传前有二次确认），全员一分钟内自动同步。',
        },
        {
          route: '/account',
          selector: '[data-tour="acct-admin"]',
          title: '团队账号管理',
          body: '账号中心可以创建团队账号、调整角色（翻译 / 校对）、重置密码与禁用账号。管理员只能提升等级，降级与管理管理员需要超级管理员。',
        },
      ],
    })
  }

  if (!tiers.length) return null
  return {
    id: tiers[0].id,
    alsoMarks: tiers.slice(1).map((t) => t.id),
    steps: tiers.flatMap((t) => t.steps),
  }
}

/**
 * 插件功能介绍：安装后首次进入对应插件页面时弹出一次。
 * 由宿主维护文案，插件本体无需改动；插件也可通过 host.startTour 自带更细的分步导览。
 */
const PLUGIN_INTROS: Record<string, { title: string; body: string; link?: TourStep['link'] }> = {
  live2d: {
    title: 'Live2D 剧情播放器',
    body: '把当前选择的剧情用 Live2D 演出播放：\n· 步进 / 自动播放逐句观看，带语音、表情与动作\n· 模型等素材首次使用时自动下载\n· 需要整库离线可在 设置 → Live2D 素材 一键同步',
    link: { label: '查看完整指南', url: LINKS.guideLive2d },
  },
  'auto-timing': {
    title: '自动轴机 / 压制',
    body: '为录屏视频自动打轴并生成字幕：\n· 选择视频与对应剧情翻译稿，一键识别打轴\n· 识别完成后可逐行检查、分句微调（带画面预览）\n· 与 Aegisub 双向同步，改完一键回读\n· 导出 .ass 自带团队样式，可直接压制成品',
    link: { label: '查看完整指南', url: LINKS.guideAutotiming },
  },
}

export function pluginIntroTour(pluginId: string): TourDef | null {
  const intro = PLUGIN_INTROS[pluginId]
  if (!intro) return null
  return {
    id: `plugin:${pluginId}`,
    steps: [{ title: intro.title, body: intro.body, link: intro.link }],
  }
}

/**
 * 版本更新说明：老用户升级后首次启动弹一次。按 major.minor 维护，
 * 补丁版本不打扰。发版时若有值得展示的变化，在这里加一条。
 */
const WHATS_NEW: Record<string, { title?: string; body: string; link?: TourStep['link'] }> = {
  '5.7': {
    body:
      '· 启动元数据拉取全面加速：剧情目录更新走 CDN 镜像（此前大表在慢链路上会卡死超时），并保证与数据源实时一致\n' +
      '· 保存重做：编辑后自动保存到正式文档（按选中剧情自动建档命名）；「保存」直写当前文件不再弹窗\n' +
      '· 编辑器键盘流：Esc 退出编辑、↑/↓ 切换行、Enter 进入编辑，当前行自动滚动居中\n' +
      '· 术语库分级导览：按登录状态与角色（翻译 / 校对 / 管理员）分层教学，被提升权限后自动补播新增教程\n' +
      '· 轴机：横幅时间帧级精修（对人工基准误差 ≤2 帧）；staff 制作人员行随导出（人名自定义）；阈值 / staff 支持命名预设；适配 Aegisub 3.2.2～最新版\n' +
      '· 压制修复（Windows）：编码器按显卡自动判定（NVIDIA/Intel/AMD 硬编逐个验证后才列出），不再默认 macOS 专属编码器；需配合轴机插件 3.1.0\n' +
      '· 修复：Live2D 从编辑器跳转语音播两次；下载页可不选章节一键下全部；调试日志可暂停滚动；不再误触浏览器右键菜单',
  },
  '5.6': {
    body:
      '· 术语库上下行同步：管理员可在术语库页一键「上传至线上术语库」（完全替换线上内容，二次确认）\n' +
      '· 同步安全网：每次从服务器拉到更新，自动在本地滚动保留最近 10 份 JSON 备份，误覆盖可随时回滚\n' +
      '· 同步流量优化：服务器启用压缩后，全量同步流量降至原来的约 1/5\n' +
      '· 账号中心「上次同步」改为显示时间（不再显示内部版本号）',
  },
  '5.5': {
    body:
      '· 轴机并行任务：可同时打轴 / 压制多个视频（默认关闭，插件页开关开启；性能不高的电脑慎用）\n' +
      '· 轴机语音停顿对齐：分句微调可拉取该句语音检测说话停顿，一键把换行对齐到实际停顿处\n' +
      '· Aegisub 同步大修：在 Aegisub 里改的译文现在会自动回读（不再被导出覆盖）；新增手动「从 Aegisub 拉取」按钮；便携版可手动指定目录安装同步宏\n' +
      '· 导出修复：清理不再删掉 \\N 换行、三行文本正确命中「3行」样式；地点横幅自动套用团队「遮罩 / 地点名称」样式\n' +
      '· 修复 Windows 打轴时弹出黑色命令行窗口（误关会导致任务失败）的问题',
  },
  '5.4': {
    body:
      '· 逐行自动保存：每次编辑后自动把译文写到输出目录的 autosave.txt，崩溃断电不再丢稿\n' +
      '· 工具栏新增「撤销 / 重做」按钮；「清空」加确认弹窗\n' +
      '· 术语库呼吸灯：有提案待审核 / 你的提案被通过时，侧栏「术语库」按主题色发光提醒\n' +
      '· 下载与保存目录支持访达 / 资源管理器可视化选择\n' +
      '· 插件更新：轴机新增微调自动保存与原文对照；Live2D 修复切换卡帧 / 闪回黑屏 / 背景丢失，进度条可拖拽',
  },
  '5.3': {
    body:
      '· 新增新手导览与插件功能介绍（设置 → 关于 可重看）\n' +
      '· 编辑器头像全面增强：画外音 / 消息 / 剧中剧角色名也能正确显示头像，并支持自定义头像材质（设置 → 外观）\n' +
      '· 修复插件市场「主页」按钮无法打开的问题，关于页新增官网入口',
    link: { label: '查看官网', url: LINKS.website },
  },
}

export function whatsNewTour(version: string): TourDef | null {
  const mm = version.split('.').slice(0, 2).join('.')
  const wn = WHATS_NEW[mm]
  if (!wn) return null
  return {
    id: `whatsnew:${mm}`,
    steps: [{ title: wn.title ?? `SekaiText 已更新到 v${version}`, body: wn.body, link: wn.link }],
  }
}
