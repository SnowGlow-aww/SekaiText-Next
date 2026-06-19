// howler-based audio loader for the Live2D player: voices and BGM.
// Voice URLs come from the existing /api/v1/voice/url endpoint; both voice and
// BGM are routed through the live2d proxy so they load in every environment
// (the preview/webview sandbox blocks some CDNs on direct fetch).

import { Howl } from 'howler'
import { api } from '../../api/client'
import { proxied, getBgmUrl, EXMEANING_BASE } from '../../constants/live2d'

export type AudioKind = 'voice' | 'bgm' | 'se'

export interface LoadedAudio {
  id: string
  kind: AudioKind
  howl: Howl
}

// Route any voice URL onto the exmeaning full mirror.
function voiceToExmeaning(url: string): string {
  let path = url.replace(/^https?:\/\/[^/]+\//, '')
  path = path.replace(/^sekai-(jp|cn)-assets\//, '').replace(/^jp-assets\//, '')
  return `${EXMEANING_BASE}/${path}`
}

function makeHowl(src: string, loop = false): Promise<Howl> {
  return new Promise((resolve, reject) => {
    // The proxy URL has no file extension, so howler can't infer the format and
    // would otherwise never fire load/loaderror (hanging the caller). Force mp3.
    const h = new Howl({ src: [src], format: ['mp3'], html5: false, loop, preload: true })
    let settled = false
    const done = (fn: () => void) => { if (!settled) { settled = true; fn() } }
    h.once('load', () => done(() => resolve(h)))
    h.once('loaderror', (_id, err) => done(() => reject(new Error(`audio load failed: ${err}`))))
    // Safety net: never hang the timeline if neither event fires.
    setTimeout(() => done(() => reject(new Error('audio load timeout'))), 10000)
  })
}

/** Load one voice clip by voiceId (via the backend voice/url + exmeaning proxy). */
export async function loadVoice(
  scenarioId: string,
  voiceId: string,
  source: string,
): Promise<LoadedAudio | null> {
  try {
    const { url } = await api.voiceUrl(scenarioId, voiceId, source)
    if (!url) return null
    const howl = await makeHowl(proxied(voiceToExmeaning(url)))
    return { id: voiceId, kind: 'voice', howl }
  } catch {
    return null
  }
}

/** Load one BGM track by name. */
export async function loadBgm(bgm: string): Promise<LoadedAudio | null> {
  try {
    const howl = await makeHowl(getBgmUrl(bgm), true)
    return { id: bgm, kind: 'bgm', howl }
  } catch {
    return null
  }
}
