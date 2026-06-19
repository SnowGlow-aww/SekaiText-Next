<script setup lang="ts">
import { ref, shallowRef, onMounted, onBeforeUnmount } from 'vue'
import * as PIXI from 'pixi.js'
import { Live2DModel, config } from '@sekai-world/pixi-live2d-display-mulmotion/cubism4'
import { Live2DController, type DialogLine } from '../../utils/live2d/Live2DController'
import { fetchScenario } from '../../utils/live2d/scenarioLoader'

// pixi-live2d-display needs PIXI global + ticker registered.
;(window as any).PIXI = PIXI
Live2DModel.registerTicker(PIXI.Ticker)
config.logLevel = config.LOG_LEVEL_ERROR

const STAGE_W = 1920
const STAGE_H = 1080

const props = defineProps<{
  source: string
  autoPlay: boolean
  voiceVolume: number
  bgmVolume: number
}>()
const emit = defineEmits<{
  loaded: []
  error: [msg: string]
  progress: [current: number, total: number]
  ended: []
}>()

const canvasRef = ref<HTMLCanvasElement | null>(null)
const wrapRef = ref<HTMLDivElement | null>(null)
const dialog = ref<DialogLine | null>(null)
const telop = ref<string | null>(null)
const fullScreenText = ref<string | null>(null)
const loading = ref(false)
const errorMsg = ref('')
const playing = ref(false)

const app = shallowRef<PIXI.Application | null>(null)
const controller = shallowRef<Live2DController | null>(null)
const root = shallowRef<PIXI.Container | null>(null)
let checkpoint = 0

// --- playback state machine ---
// `stepping` is true while a single forward step (stepUntilCheckpoint) runs;
// `busy` serialises whole operations (a forward step OR a backward seek) so
// clicks can never overlap and corrupt `checkpoint`/`history`.
let stepping = false
let busy = false
const history: number[] = [] // checkpoints we departed FROM, for 上一步

function initApp() {
  if (app.value || !canvasRef.value) return
  const a = new PIXI.Application({
    view: canvasRef.value,
    backgroundAlpha: 0,
    antialias: true,
    autoStart: true,
  })
  app.value = a
  const r = new PIXI.Container()
  a.stage.addChild(r)
  root.value = r
  fitStage()
}

function fitStage() {
  if (!app.value || !root.value || !wrapRef.value) return
  const w = wrapRef.value.clientWidth
  const h = wrapRef.value.clientHeight
  app.value.renderer.resize(w, h)
  const scale = Math.min(w / STAGE_W, h / STAGE_H)
  root.value.scale.set(scale)
  root.value.x = (w - STAGE_W * scale) / 2
  root.value.y = (h - STAGE_H * scale) / 2
}

async function play(type: string, sort: string, index: string, chapter: number) {
  if (!app.value || !root.value) return
  loading.value = true
  errorMsg.value = ''
  dialog.value = null
  telop.value = null
  fullScreenText.value = null
  history.length = 0
  try {
    controller.value?.destroy()
    root.value.removeChildren()

    const bgLayer = new PIXI.Container()
    const modelLayer = new PIXI.Container()
    root.value.addChild(bgLayer, modelLayer)

    const { scenario } = await fetchScenario(type, sort, index, chapter, props.source)
    const ctrl = new Live2DController(scenario, app.value, bgLayer, modelLayer, props.source, {
      onDialog: (l) => { dialog.value = l; if (l) telop.value = null },
      onProgress: (c, t) => emit('progress', c, t),
      onTelop: (t) => { telop.value = t },
      onFullScreenText: (t) => { fullScreenText.value = t },
    })
    ctrl.setVoiceVolume(props.voiceVolume)
    ctrl.setBgmVolume(props.bgmVolume)
    controller.value = ctrl
    await ctrl.init()
    // Start before the first snippet so the opening step lands ON snippet 0
    // (e.g. the first location banner) instead of skipping past it.
    checkpoint = -1
    playing.value = true
    emit('loaded')
    // Take the first step to the opening checkpoint; chain autoplay if enabled.
    busy = true
    try { await runStep() } finally { busy = false }
    if (props.autoPlay) void autoLoop()
  } catch (e: any) {
    errorMsg.value = e?.message || String(e)
    emit('error', errorMsg.value)
  } finally {
    loading.value = false
  }
}

async function runStep() {
  if (!controller.value) return
  stepping = true
  try {
    if (checkpoint >= 0) history.push(checkpoint) // don't record the pre-start sentinel (-1)
    const next = await controller.value.stepUntilCheckpoint(checkpoint)
    if (next === -1) {
      playing.value = false
      emit('ended')
    } else {
      checkpoint = next
    }
  } finally {
    stepping = false
  }
}

/** Stage click: keep the familiar feel — a click during a running step skips to
 *  the end of the current line; otherwise it advances one line. */
async function onClick() {
  if (!controller.value || !playing.value) return
  if (stepping) { controller.value.skip(); return }
  if (busy) return
  busy = true
  try {
    await runStep()
    if (props.autoPlay) void autoLoop()
  } finally { busy = false }
}

/** 下一步 button: a single press always moves to the next line — if a step is
 *  mid-flight it skips it to completion (no need to press twice). */
async function next() {
  if (!controller.value || !playing.value) return
  if (stepping) { controller.value.skip(); return }
  if (busy) return
  busy = true
  try {
    await runStep()
    if (props.autoPlay) void autoLoop()
  } finally { busy = false }
}

/** Autoplay: after a step settles, wait briefly then take the next, until the
 *  user turns autoplay off or the scenario ends. */
async function autoLoop() {
  while (props.autoPlay && playing.value && !busy) {
    busy = true
    try {
      await runStep()
      // Autoplay paces on the voice: wait for the just-started line's voice to
      // finish before advancing. Manual 下一步/click never waits (atomic step).
      if (playing.value) await controller.value?.waitForVoice()
    } finally { busy = false }
    if (!playing.value) break
    await new Promise(r => setTimeout(r, 200))
  }
}

/** 上一步: rebuild the scene up to the previous checkpoint. seekTo replays from
 *  the start (Live2D state can't be rewound in place), now WITH voice on the
 *  landed line. Serialised through `busy`; aborts any running step first. */
async function prev() {
  if (!controller.value || !playing.value) return
  if (history.length < 1) return
  if (stepping) controller.value.skip()
  while (busy) await new Promise(r => setTimeout(r, 30))
  busy = true
  try {
    const target = history.pop() ?? 0
    await controller.value.seekTo(target)
    checkpoint = target
  } finally {
    busy = false
  }
}

defineExpose({
  play: (t: string, s: string, i: string, c: number) => play(t, s, i, c),
  advance: next,
  next,
  prev,
  /** Jump to a 1-based dialog line: rebuild the scene up to that line's snippet
   *  (with voice on the landed line), and resync the history stack so 上一步
   *  keeps working from the jumped-to position. */
  seekToLine: async (line: number) => {
    if (!controller.value || !playing.value) return
    if (stepping) controller.value.skip()
    while (busy) await new Promise(r => setTimeout(r, 30))
    busy = true
    try {
      const target = controller.value.snippetForDialogLine(line)
      history.length = 0
      for (let n = 1; n < line; n++) history.push(controller.value.snippetForDialogLine(n))
      await controller.value.seekTo(target)
      checkpoint = target
    } finally {
      busy = false
    }
  },
  setVolumes: (v: number, b: number) => {
    controller.value?.setVoiceVolume(v)
    controller.value?.setBgmVolume(b)
  },
})

let ro: ResizeObserver | null = null
onMounted(() => {
  initApp()
  ro = new ResizeObserver(fitStage)
  if (wrapRef.value) ro.observe(wrapRef.value)
})
onBeforeUnmount(() => {
  ro?.disconnect()
  controller.value?.destroy()
  app.value?.destroy(false, { children: true })
  app.value = null
})
</script>

<template>
  <div ref="wrapRef" class="relative w-full h-full overflow-hidden bg-black" @click="onClick">
    <canvas ref="canvasRef" class="w-full h-full block" />

    <div v-if="telop" class="absolute inset-x-0 top-1/3 flex justify-center pointer-events-none px-6">
      <div class="bg-black/55 text-white text-lg md:text-2xl tracking-wide rounded px-6 py-3 text-center whitespace-pre-wrap">
        {{ telop }}
      </div>
    </div>

    <div v-if="fullScreenText" class="absolute inset-0 flex items-center justify-center bg-black/80 pointer-events-none px-10">
      <div class="text-white text-2xl md:text-4xl leading-relaxed text-center whitespace-pre-wrap">
        {{ fullScreenText }}
      </div>
    </div>

    <div
      v-if="dialog"
      class="absolute left-1/2 -translate-x-1/2 bottom-6 w-[80%] max-w-3xl pointer-events-none"
    >
      <div class="bg-base-100/85 backdrop-blur rounded-2xl border border-base-300 px-5 py-3 shadow-lg">
        <div v-if="dialog.name" class="text-primary font-semibold text-sm mb-1">{{ dialog.name }}</div>
        <div class="text-base-content text-base leading-relaxed whitespace-pre-wrap">{{ dialog.body }}</div>
      </div>
    </div>

    <div v-if="loading" class="absolute inset-0 flex items-center justify-center pointer-events-none">
      <span class="loading loading-spinner loading-lg text-primary" />
    </div>
    <div v-if="errorMsg" class="absolute top-2 left-2 right-2 text-xs text-error bg-base-200/80 rounded px-2 py-1">
      {{ errorMsg }}
    </div>
    <div v-if="playing && !autoPlay" class="absolute bottom-1 right-2 text-xs text-base-content/50 pointer-events-none">
      点击推进 ▸
    </div>
  </div>
</template>
