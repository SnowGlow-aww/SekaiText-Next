import { api } from '../api/client'

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

/** 常用外链集中定义，避免散落硬编码。 */
export const LINKS = {
  website: 'https://sakimizuki.accr.cc/web/index.html',
  guide: 'https://sakimizuki.accr.cc/web/guide/index.html',
  guideLive2d: 'https://sakimizuki.accr.cc/web/guide/live2d.html',
  guideAutotiming: 'https://sakimizuki.accr.cc/web/guide/autotiming.html',
  github: 'https://github.com/SnowGlow-aww/SekaiText-Next',
} as const

/**
 * 在系统浏览器中打开外部链接。Tauri webview 不处理 target=_blank /
 * window.open：桌面经后端打开，Android 经受限的原生 ACTION_VIEW 命令；
 * 浏览器开发模式直接新标签。
 */
export function openExternal(url: string): void {
  if (isTauri) {
    api.openUrl(url).catch((e) => console.error('[openExternal]', e))
  } else {
    window.open(url, '_blank', 'noopener')
  }
}
