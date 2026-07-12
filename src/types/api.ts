export interface Settings {
  fontSize: number
  uiFontSize: number
  downloadSource: string
  saveN: boolean
  saveVoice: boolean
  disableSSL: boolean
  debugEnabled: boolean
  indexOrder: 'asc' | 'desc'
  voiceOutputDir?: string
  jsonDownloadDir?: string
  saveBaseDir?: string
  preserveStoryOnModeSwitch: boolean
  undoDepth: number
  keepHighlightWhenCompareOff: boolean
  shortcuts?: Record<string, string>
  hideAgreementImportHint?: boolean

  lastStoryType?: string
  lastStorySort?: string
  lastStoryIndex?: string
  lastChapter?: number
  lastDataSource?: string

  /** Where the Live2D dock sits relative to the editor: 'top' | 'right' | 'bottom' | 'window'. */
  live2dPosition?: string

  /** Override the plugin marketplace index URL. Empty = built-in default. */
  pluginMarketUrl?: string

  /** Override the app-release manifest URL. Empty = built-in default. */
  appUpdateUrl?: string

  /** 更新与插件市场下载渠道：'cdn'(默认,国内加速) | 'github'(直连)。 */
  downloadMirror?: string

  /** 已看过的导览 id（app-welcome / plugin:xxx / whatsnew:x.y），每个只弹一次。 */
  seenTours?: string[]

  /** 上次启动时的应用版本；升级后首启触发一次 what's new。 */
  lastSeenVersion?: string
}

export interface SaveMetadata {
  type: string
  sort?: string
  index: string
  chapter: number
  source: string
  scenarioId: string
  mode?: number
}

export interface MigrateSaveDirResult {
  oldDir: string
  newDir: string
  moved: number
  skipped: number
  /**
   * 因目标已存在同名文件而未搬走、仍留在旧目录的文件相对路径（相对旧/新根同一
   * 形式，正斜杠）。前端据此把这些文档的绑定保留在旧目录，绝不改写到新根那个
   * 内容不同的同名陌生文件（否则下次自动保存会覆盖它、丢掉原稿）。
   */
  skippedPaths: string[]
}
