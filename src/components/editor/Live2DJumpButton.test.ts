// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

const toastMock = vi.hoisted(() => ({ show: vi.fn() }))
vi.mock('../../composables/useToast', () => ({ useToast: () => toastMock }))

import Live2DJumpButton from './Live2DJumpButton.vue'
import { usePluginRegistry } from '../../plugin-host/registry'
import { useLive2dDockStore } from '../../stores/live2dDock'
import { useStoryStore } from '../../stores/story'

async function mountButton(scenarioId = 'scenario-current') {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePluginRegistry().markLoaded('live2d')
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp(Live2DJumpButton, { scenarioId, talkIndex: 2, voiceId: 'voice-2' })
  app.use(pinia)
  app.mount(host)
  await nextTick()
  const button = host.querySelector('button') as HTMLButtonElement | null
  if (!button) throw new Error('Live2D button did not render')
  return { app, button }
}

describe('Live2DJumpButton feedback', () => {
  beforeEach(() => {
    toastMock.show.mockReset()
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('warns instead of silently returning when the host story is missing', async () => {
    const mounted = await mountButton()

    mounted.button.click()
    await nextTick()

    expect(toastMock.show).toHaveBeenCalledWith(
      '当前剧情原文未就绪，无法在 Live2D 中播放；请重新载入剧情',
      'warn',
    )
    mounted.app.unmount()
  })

  it('warns when the clicked row belongs to a different scenario', async () => {
    const mounted = await mountButton('scenario-old')
    const story = useStoryStore()
    story.scenarioId = 'scenario-current'
    story.sourceTalks = [{ speaker: '愛莉', text: '原文', charIndex: 0 }]

    mounted.button.click()
    await nextTick()

    expect(toastMock.show).toHaveBeenCalledWith(
      '当前剧情原文未就绪，无法在 Live2D 中播放；请重新载入剧情',
      'warn',
    )
    mounted.app.unmount()
  })

  it('reports a routed jump failure and does not silently discard it', async () => {
    const mounted = await mountButton()
    const story = useStoryStore()
    story.scenarioId = 'scenario-current'
    story.sourceTalks = [{ speaker: '愛莉', text: '原文', charIndex: 0 }]
    vi.spyOn(useLive2dDockStore(), 'requestJump').mockRejectedValueOnce(new Error('plugin stage missing'))

    mounted.button.click()
    await nextTick()
    await Promise.resolve()

    expect(toastMock.show).toHaveBeenCalledWith(
      'Live2D 播放请求失败，请检查插件和当前剧情',
      'error',
    )
    mounted.app.unmount()
  })
})
