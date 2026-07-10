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
