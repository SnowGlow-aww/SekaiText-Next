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

  /** Override the plugin marketplace index URL. Empty = built-in default. */
  pluginMarketUrl?: string

  /** Override the app-release manifest URL. Empty = built-in default. */
  appUpdateUrl?: string
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
