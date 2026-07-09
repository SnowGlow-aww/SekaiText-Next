<script setup lang="ts">
// 导览渲染引擎：聚光灯高亮 + 提示卡片。步骤有 selector 时挖洞高亮目标元素，
// 否则显示居中卡片。目标找不到（如对应侧栏项被隐藏）会自动跳过该步。
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ExternalLink } from 'lucide-vue-next'
import { useTour } from './useTour'
import { openExternal } from '../utils/openExternal'

const tour = useTour()
const router = useRouter()
const route = useRoute()

const stepIndex = ref(0)
const rect = ref<{ x: number; y: number; w: number; h: number } | null>(null)
const cardEl = ref<HTMLElement | null>(null)
const cardPos = ref<{ top: number; left: number } | null>(null)
let targetEl: HTMLElement | null = null

const steps = computed(() => tour.active.value?.steps ?? [])
const step = computed(() => steps.value[stepIndex.value] ?? null)
const isLast = computed(() => stepIndex.value === steps.value.length - 1)

const PAD = 6 // 高亮框相对目标的外扩
const GAP = 12 // 卡片与高亮框的间距

async function waitFor(selector: string, timeout = 2000): Promise<HTMLElement | null> {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) {
    const el = document.querySelector<HTMLElement>(selector)
    if (el) return el
    await new Promise((r) => setTimeout(r, 50))
  }
  return null
}

function measure() {
  if (!targetEl) {
    rect.value = null
  } else {
    const r = targetEl.getBoundingClientRect()
    // 收边到视口：目标比视口还高时（如设置页整个区块），框住它的可见部分
    const top = Math.max(r.top - PAD, 6)
    const bottom = Math.min(r.bottom + PAD, window.innerHeight - 6)
    rect.value = { x: r.left - PAD, y: top, w: r.width + PAD * 2, h: Math.max(bottom - top, 0) }
  }
  placeCard()
}

function placeCard() {
  if (!rect.value) {
    cardPos.value = null // 居中卡片走 CSS
    return
  }
  const vw = window.innerWidth
  const vh = window.innerHeight
  const cw = cardEl.value?.offsetWidth ?? 340
  const ch = cardEl.value?.offsetHeight ?? 180
  const r = rect.value
  // 垂直：优先放在目标下方，放不下改上方，再不行贴目标右侧垂直居中。
  let top = r.y + r.h + GAP
  if (top + ch > vh - 8) top = r.y - ch - GAP
  let left = r.x + r.w / 2 - cw / 2
  if (top < 8) {
    top = Math.min(Math.max(r.y + r.h / 2 - ch / 2, 8), vh - ch - 8)
    left = r.x + r.w + GAP
    if (left + cw > vw - 8) left = r.x - cw - GAP
  }
  cardPos.value = {
    top: Math.min(Math.max(top, 8), Math.max(vh - ch - 8, 8)),
    left: Math.min(Math.max(left, 8), Math.max(vw - cw - 8, 8)),
  }
}

async function activateStep() {
  const st = step.value
  if (!st) return
  targetEl = null
  if (st.route && route.path !== st.route) {
    await router.push(st.route).catch(() => {})
  }
  if (st.selector) {
    const el = await waitFor(st.selector)
    if (!el) {
      // 目标不存在：无声跳过（最后一步找不到就直接结束）。
      if (isLast.value) tour.finish()
      else stepIndex.value++
      return
    }
    el.scrollIntoView({ block: el.getBoundingClientRect().height > window.innerHeight * 0.75 ? 'start' : 'nearest' })
    targetEl = el
  }
  await nextTick()
  measure()
  // 卡片内容变化后尺寸才稳定，再排一次版。
  await nextTick()
  placeCard()
}

watch(
  () => tour.active.value,
  (v) => {
    if (v) {
      stepIndex.value = 0
      activateStep()
    } else {
      targetEl = null
      rect.value = null
    }
  },
)
watch(stepIndex, () => activateStep())

function next() {
  if (isLast.value) tour.finish()
  else stepIndex.value++
}
function prev() {
  if (stepIndex.value > 0) stepIndex.value--
}

function onResize() {
  if (tour.active.value) measure()
}
// 兜底：程序性滚动(scrollIntoView/路由恢复)后重新量取，高亮跟着目标走
function onScroll() {
  if (tour.active.value && targetEl) measure()
}
onMounted(() => {
  window.addEventListener('resize', onResize)
  window.addEventListener('scroll', onScroll, true)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  window.removeEventListener('scroll', onScroll, true)
})
</script>

<template>
  <teleport to="body">
    <div v-if="tour.active.value && step" class="fixed inset-0 z-[10000]" @wheel.prevent @touchmove.prevent>
      <!-- 点击护罩：无高亮时兼作暗幕 -->
      <div class="absolute inset-0" :class="rect ? '' : 'bg-black/55'" @click.stop />
      <!-- 聚光灯：透明中心 + 超大 box-shadow 压暗四周 -->
      <div
        v-if="rect"
        class="absolute rounded-lg pointer-events-none transition-all duration-300 ring-2 ring-[var(--color-primary)]"
        :style="{
          top: rect.y + 'px',
          left: rect.x + 'px',
          width: rect.w + 'px',
          height: rect.h + 'px',
          boxShadow: '0 0 0 200vmax rgba(0,0,0,.55)',
        }"
      />
      <!-- 提示卡片 -->
      <div
        ref="cardEl"
        class="absolute w-[340px] max-w-[92vw] rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] shadow-2xl p-4"
        :style="cardPos
          ? { top: cardPos.top + 'px', left: cardPos.left + 'px' }
          : { top: '50%', left: '50%', transform: 'translate(-50%,-50%)' }"
      >
        <div class="text-sm font-semibold mb-1.5">{{ step.title }}</div>
        <div class="text-sm text-[var(--color-text-secondary)] leading-relaxed whitespace-pre-line">{{ step.body }}</div>
        <button
          v-if="step.link"
          @click="openExternal(step.link.url)"
          class="btn btn-xs gap-1 mt-3 border-0 text-[var(--color-primary)] bg-[var(--color-primary)]/10 hover:bg-[var(--color-primary)]/20"
        >
          <ExternalLink :size="12" /> {{ step.link.label }}
        </button>
        <div class="flex items-center justify-between mt-4">
          <div class="flex items-center gap-1">
            <span
              v-for="(_, i) in steps"
              :key="i"
              class="h-1.5 rounded-full transition-all duration-200"
              :class="i === stepIndex
                ? 'w-4 bg-[var(--color-primary)]'
                : 'w-1.5 bg-[var(--color-text-secondary)]/35'"
            />
          </div>
          <div class="flex items-center gap-2">
            <button v-if="!isLast" @click="tour.finish()" class="btn btn-xs btn-ghost text-[var(--color-text-secondary)]">跳过</button>
            <button v-if="stepIndex > 0" @click="prev" class="btn btn-xs btn-ghost border border-[var(--color-border)]">上一步</button>
            <button @click="next" class="btn btn-xs btn-brand">{{ isLast ? '完成' : (stepIndex === 0 && !step.selector ? '开始导览' : '下一步') }}</button>
          </div>
        </div>
      </div>
    </div>
  </teleport>
</template>
