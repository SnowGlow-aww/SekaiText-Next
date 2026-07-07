<script setup lang="ts">
// 桌面端 EditorWorkspace/EditorPage 的忠实迷你复刻(布局/配色/交互均对照 src/ 源码):
// 双栏对照 + 校对对比(黄基线/红删除/绿新增) + 模式侧栏 + 工具栏开关 + 术语悬停 + \N 分行
import { ref, computed } from 'vue'
import { withBase } from 'vitepress'

// —— 术语库(演示数据) ——
interface Term {
  t: string
  to: string
  note: string
  category: string
}
const TERMS: Term[] = [
  { t: 'ナイトコード', to: 'Nightcord', note: '组内统一译法，保留英文', category: '专有名词' },
  { t: 'セカイ', to: 'SEKAI', note: '组内约定：保留不译', category: '世界观' },
  { t: 'SekaiText', to: 'SekaiText', note: '就是你正在看的这个工具（笑）', category: '彩蛋' },
]
const termRe = new RegExp(`(${TERMS.map((x) => x.t).join('|')})`, 'g')
function segments(jp: string) {
  return jp
    .split(termRe)
    .filter((s) => s !== '')
    .map((s) => ({ text: s, term: TERMS.find((x) => x.t === s) || null }))
}

// —— 示例剧情(宣传用创作，非游戏原文) ——
type Part = { t: string; k: 'same' | 'add' | 'remove' }
interface Item {
  id: number
  text: string
  touched: boolean
  parts?: Part[] // 编辑行的差异高亮(绿=新增)
}
interface Group {
  speaker: string
  avatar: string
  jp: string
  voice: boolean
  baseParts?: Part[] // 校对基线行(红=删除)
  items: Item[]
}
let nextId = 100
const groups = ref<Group[]>([
  {
    speaker: '宵崎奏',
    avatar: '奏',
    jp: '……新曲、もうすぐ完成しそう。今夜もナイトコードで集まれる？',
    voice: true,
    items: [{ id: 1, text: '……新歌快要完成了。今晚也能在Nightcord集合吗？', touched: false }],
  },
  {
    speaker: '朝比奈まふゆ',
    avatar: 'ま',
    jp: 'うん、大丈夫。セカイの方も、少し気になるけど。',
    voice: true,
    baseParts: [
      { t: '嗯，可以。虽然SEKAI那边也', k: 'same' },
      { t: '让人', k: 'remove' },
      { t: '有点在意。', k: 'same' },
    ],
    items: [
      {
        id: 2,
        text: '嗯，可以。虽然SEKAI那边也有点在意呢。',
        touched: false,
        parts: [
          { t: '嗯，可以。虽然SEKAI那边也有点在意', k: 'same' },
          { t: '呢', k: 'add' },
          { t: '。', k: 'same' },
        ],
      },
    ],
  },
  {
    speaker: '東雲絵名',
    avatar: '絵',
    jp: 'ねえ、この歌詞……海外のファンにも伝わるのかな。',
    voice: true,
    items: [{ id: 3, text: '', touched: false }],
  },
  {
    speaker: '暁山瑞希',
    avatar: '瑞',
    jp: '字幕をつければいいんじゃない？いいツール知ってるよ☆',
    voice: true,
    items: [{ id: 4, text: '', touched: false }],
  },
  {
    speaker: '宵崎奏',
    avatar: '奏',
    jp: '……SekaiText。翻訳も、字幕も、これひとつでできる。',
    voice: true,
    items: [{ id: 5, text: '', touched: false }],
  },
  {
    speaker: '東雲絵名',
    avatar: '絵',
    jp: '宣伝じゃん！（笑）',
    voice: false,
    items: [{ id: 6, text: '', touched: false }],
  },
])

// —— 模式 / 工具栏开关(对照桌面端:校对默认开对比) ——
const mode = ref(1) // 0=翻译 1=校对
const showCompare = ref(true)
const showGlossary = ref(true)

function isChanged(g: Group, it: Item) {
  return mode.value >= 1 && !!it.parts && !it.touched
}
function showBaseline(g: Group) {
  return mode.value >= 1 && showCompare.value && !!g.baseParts && !g.items[0]?.touched
}

// —— 编辑 ——
const workspaceRef = ref<HTMLElement | null>(null)
function touch(g: Group, it: Item) {
  if (!it.touched) it.touched = true
}
function onEnter(e: KeyboardEvent) {
  if (e.isComposing) return
  e.preventDefault()
  const inputs = workspaceRef.value?.querySelectorAll<HTMLInputElement>('input.ed-input')
  if (!inputs) return
  const i = Array.from(inputs).indexOf(e.target as HTMLInputElement)
  inputs[i + 1]?.focus()
}
const MAX_LINES = 4
function addLine(g: Group) {
  if (g.items.length >= MAX_LINES) {
    toastMsg(`体验版每句最多 ${MAX_LINES} 行`)
    return
  }
  g.items.push({ id: nextId++, text: '', touched: true })
}
function removeLine(g: Group, idx: number) {
  g.items.splice(idx, 1)
}

const done = computed(() => groups.value.filter((g) => g.items.some((it) => it.text.trim())).length)
const allDone = computed(() => done.value === groups.value.length)

// —— 术语悬停提示(fixed 定位，不受滚动容器裁剪) ——
const tip = ref<{ term: Term; x: number; y: number } | null>(null)
function showTip(e: MouseEvent, term: Term) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  tip.value = { term, x: r.left + r.width / 2, y: r.bottom + 8 }
}
function hideTip() {
  tip.value = null
}

// —— Toast ——
const toast = ref('')
let toastTimer: ReturnType<typeof setTimeout> | undefined
function toastMsg(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (toast.value = ''), 2200)
}
const desktopOnly = (name: string) => toastMsg(`「${name}」在桌面版中开放`)
</script>

<template>
  <div class="stx-demo">
    <div class="d-body">
      <!-- 模式侧栏 -->
      <aside class="d-side">
        <div class="d-side-label">模式</div>
        <button class="d-side-btn" :class="{ active: mode === 0 }" @click="mode = 0">翻译</button>
        <button class="d-side-btn" :class="{ active: mode === 1 }" @click="mode = 1">校对</button>
        <button class="d-side-btn" @click="desktopOnly('合意模式')">合意</button>
        <div class="d-side-sp" />
        <button class="d-side-btn dim" @click="desktopOnly('术语库')">术语库</button>
        <button class="d-side-btn dim" @click="desktopOnly('插件市场')">插件市场</button>
        <button class="d-side-btn dim" @click="desktopOnly('设置')">设置</button>
      </aside>

      <div class="d-main">
        <!-- 工具栏 -->
        <div class="d-toolbar">
          <button class="d-btn" @click="desktopOnly('打开文件')">打开</button>
          <button class="d-btn" @click="toastMsg('已保存（体验版不落盘）')">保存</button>
          <button class="d-btn" @click="desktopOnly('清空')">清空</button>
          <span class="d-sep" />
          <button class="d-chip" aria-pressed="false" @click="desktopOnly('闪回溯源')">闪回</button>
          <button class="d-chip" :aria-pressed="showGlossary" @click="showGlossary = !showGlossary">术语</button>
          <button class="d-chip" aria-pressed="false" @click="desktopOnly('同步滚动')">同步</button>
          <button class="d-chip" aria-pressed="false" @click="desktopOnly('搜索替换')">搜索</button>
          <span class="d-sep" />
          <button class="d-btn" @click="desktopOnly('说话人批量翻译')">说话人</button>
          <button class="d-btn" @click="desktopOnly('全文检查')">检查</button>
          <template v-if="mode >= 1">
            <span class="d-sep" />
            <button class="d-chip" :aria-pressed="showCompare" @click="showCompare = !showCompare">对比</button>
          </template>
          <span class="d-progress" :class="{ done: allDone }">{{ allDone ? '✓ 全部完成！' : `${done}/${groups.length}` }}</span>
        </div>

        <!-- 工作区(双栏对照) -->
        <div ref="workspaceRef" class="d-workspace">
          <div class="d-cols">
            <div class="d-col-h">
              <span>原文</span>
              <span class="d-scenario">event_143_07</span>
            </div>
            <div class="d-col-h d-col-h-r">
              <span>译文</span>
              <input class="d-title" type="text" placeholder="标题..." @click.stop />
            </div>
          </div>

          <div class="d-groups">
            <div v-for="(g, gi) in groups" :key="gi" class="d-group">
              <!-- 原文侧 -->
              <div class="d-src">
                <span class="d-avatar">{{ g.avatar }}</span>
                <span class="d-src-body">
                  <span class="d-speaker">{{ g.speaker }}</span>
                  <span class="d-jp">
                    <template v-for="(s, si) in segments(g.jp)" :key="si">
                      <span
                        v-if="s.term && showGlossary"
                        class="d-term"
                        @mouseenter="showTip($event, s.term)"
                        @mouseleave="hideTip"
                      >{{ s.text }}</span>
                      <template v-else>{{ s.text }}</template>
                    </template>
                  </span>
                </span>
                <span class="d-ctrl-stack">
                  <button v-if="g.voice" class="d-ctrl" @click="desktopOnly('语音试听')">▶ 语音</button>
                  <button class="d-ctrl" @click="desktopOnly('Live2D 播放')">🎭 Live2D</button>
                </span>
              </div>

              <!-- 译文侧 -->
              <div class="d-dst">
                <!-- 校对基线行(只读):删除字红底 -->
                <div v-if="showBaseline(g)" class="d-row d-row-base">
                  <span class="d-num" />
                  <span class="d-row-speaker">原</span>
                  <span class="d-row-text d-base-text">
                    <template v-for="(p, pi) in g.baseParts" :key="pi">
                      <span v-if="p.k === 'remove'" class="d-rm">{{ p.t }}</span>
                      <template v-else>{{ p.t }}</template>
                    </template>
                  </span>
                </div>

                <div
                  v-for="(it, ii) in g.items"
                  :key="it.id"
                  class="d-row"
                  :class="{ 'd-row-changed': showCompare && isChanged(g, it) }"
                >
                  <span class="d-num">{{ ii === 0 ? gi + 1 : '' }}</span>
                  <span class="d-row-speaker">{{ ii === 0 ? g.speaker : '' }}</span>

                  <!-- 有差异且未动过:展示绿色新增高亮,点击转输入 -->
                  <span
                    v-if="showCompare && isChanged(g, it)"
                    class="d-row-text d-editable"
                    @click="touch(g, it)"
                  >
                    <template v-for="(p, pi) in it.parts" :key="pi">
                      <span v-if="p.k === 'add'" class="d-add">{{ p.t }}</span>
                      <template v-else-if="p.k !== 'remove'">{{ p.t }}</template>
                    </template>
                  </span>
                  <input
                    v-else
                    v-model="it.text"
                    class="d-row-text ed-input"
                    type="text"
                    :placeholder="ii === 0 ? '点击输入译文…' : '续行(\\N)…'"
                    @focus="touch(g, it)"
                    @keydown.enter="onEnter($event)"
                  />

                  <span class="d-row-end">
                    <span v-if="ii < g.items.length - 1" class="d-nmark">\N</span>
                    <button v-if="ii === g.items.length - 1" class="d-mini" title="添加行(\N 分行)" @click="addLine(g)">+</button>
                    <button v-if="ii > 0" class="d-mini d-mini-del" title="删除行" @click="removeLine(g, ii)">−</button>
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 底栏 -->
        <div class="d-foot">
          <template v-if="allDone">
            🎉 这就是 SekaiText 的手感 —— <a :href="withBase('/download.html')">下载桌面版</a>，打轴、压制、Live2D 全都有。
          </template>
          <template v-else>
            可以真的动手试试：输入译文、悬停<span class="d-foot-term">虚线术语</span>、看「对比」的校对差异高亮、点 ＋ 给译文 \N 分行。示例文本为宣传用创作，非游戏原文。
          </template>
        </div>
      </div>
    </div>

    <!-- 术语悬浮提示(对照桌面端术语 tooltip 结构) -->
    <Transition name="d-fade">
      <div v-if="tip" class="d-tip" :style="{ left: tip.x + 'px', top: tip.y + 'px' }">
        <div class="d-tip-head">
          <b>{{ tip.term.t }}</b><span class="d-tip-arrow">→</span><span class="d-tip-to">{{ tip.term.to }}</span>
        </div>
        <div class="d-tip-note">{{ tip.term.note }}</div>
        <div class="d-tip-cat">{{ tip.term.category }}</div>
      </div>
    </Transition>

    <Transition name="d-fade">
      <div v-if="toast" class="d-toast">{{ toast }}</div>
    </Transition>
  </div>
</template>

<style scoped>
/* 配色对照桌面端 src/style.css:主色 #6c4cff(暗色 #8c79ff),surface/border 随站点主题 */
.stx-demo {
  --dp: #6c4cff;
  --d-border: var(--vp-c-divider);
  --d-surface: var(--vp-c-bg-soft);
  --d-text2: var(--vp-c-text-2);
  --d-text3: var(--vp-c-text-3);
  position: relative;
  text-align: left;
  background: var(--vp-c-bg);
  font-size: 14px;
}

.d-body {
  display: flex;
  min-height: 0;
}

/* ---- 模式侧栏 ---- */
.d-side {
  width: 92px;
  flex: none;
  border-right: 1px solid var(--d-border);
  background: var(--d-surface);
  padding: 8px 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.d-side-label {
  font-size: 11px;
  color: var(--d-text3);
  padding: 2px 8px 6px;
}
.d-side-btn {
  text-align: left;
  padding: 6px 10px;
  border-radius: 8px;
  font-size: 13px;
  color: var(--d-text2);
  transition: color 0.15s ease, background-color 0.15s ease;
  cursor: pointer;
}
.d-side-btn:hover {
  color: var(--dp);
}
.d-side-btn.active {
  background: color-mix(in srgb, var(--dp) 10%, transparent);
  color: var(--dp);
  font-weight: 500;
}
.d-side-btn.dim {
  font-size: 12px;
}
.d-side-sp {
  flex: 1;
  min-height: 12px;
}

/* ---- 工具栏 ---- */
.d-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.d-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--d-border);
  background: var(--d-surface);
}
.d-btn,
.d-chip {
  display: inline-flex;
  align-items: center;
  height: 26px;
  padding: 0 9px;
  border-radius: 8px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--d-text2);
  white-space: nowrap;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}
.d-btn:hover,
.d-chip:hover {
  background: color-mix(in srgb, var(--vp-c-text-1) 8%, transparent);
  color: var(--vp-c-text-1);
}
.d-chip[aria-pressed='true'] {
  background: color-mix(in srgb, var(--dp) 14%, transparent);
  color: var(--dp);
}
.d-sep {
  width: 1px;
  height: 18px;
  background: var(--d-border);
  margin: 0 5px;
}
.d-progress {
  margin-left: auto;
  font-size: 11.5px;
  color: var(--d-text2);
  background: color-mix(in srgb, var(--vp-c-text-1) 7%, transparent);
  border-radius: 20px;
  padding: 2px 9px;
  white-space: nowrap;
}
.d-progress.done {
  color: #fff;
  background: var(--st-gradient);
}

/* ---- 工作区 ---- */
.d-workspace {
  max-height: 400px;
  overflow-y: auto;
  position: relative;
}
.d-cols {
  display: grid;
  grid-template-columns: 1fr 1fr;
  position: sticky;
  top: 0;
  z-index: 5;
  background: var(--vp-c-bg);
  border-bottom: 1px solid var(--d-border);
}
.d-col-h {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 12px;
  font-size: 13px;
  font-weight: 600;
  color: var(--d-text2);
}
.d-col-h-r {
  border-left: 1px solid var(--d-border);
}
.d-scenario {
  font-size: 11px;
  font-weight: 400;
  color: var(--d-text3);
}
.d-title {
  flex: 1;
  min-width: 0;
  max-width: 150px;
  font-size: 12px;
  font-weight: 400;
  padding: 3px 8px;
  border-radius: 7px;
  border: 1px solid var(--d-border);
  background: var(--d-surface);
  color: var(--vp-c-text-1);
}
.d-title:focus {
  outline: none;
  border-color: var(--dp);
}

.d-groups {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px 8px 10px;
}
.d-group {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

/* 原文卡 */
.d-src {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--d-border);
  border-radius: 10px;
}
.d-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--vp-c-text-1) 72%, transparent);
  color: var(--vp-c-bg);
  font-size: 12px;
  font-weight: 500;
}
.d-src-body {
  flex: 1;
  min-width: 0;
}
.d-speaker {
  display: block;
  font-size: 11.5px;
  font-weight: 500;
  color: var(--d-text2);
  margin-bottom: 2px;
}
.d-jp {
  display: block;
  line-height: 1.65;
  color: var(--vp-c-text-1);
  word-break: break-word;
}
.d-ctrl-stack {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 4px;
  flex: none;
}
.d-ctrl {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 26px;
  padding: 0 9px;
  border: 1px solid var(--d-border);
  border-radius: 7px;
  font-size: 11.5px;
  line-height: 1;
  color: var(--d-text2);
  white-space: nowrap;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease;
}
.d-ctrl:hover {
  border-color: var(--dp);
  color: var(--dp);
}

/* 术语高亮(对照桌面端 .glossary-hit:主色点状下划线) */
.d-term {
  border-bottom: 1.5px dotted var(--dp);
  cursor: help;
}
.d-term:hover {
  background: color-mix(in srgb, var(--dp) 15%, transparent);
  border-radius: 2px;
}

/* 译文行 */
.d-dst {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.d-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid var(--d-border);
  border-radius: 10px;
  transition: background-color 0.15s ease;
}
.d-row:hover {
  background: color-mix(in srgb, var(--dp) 4%, transparent);
}
.d-row:first-child:last-child {
  height: 100%;
  align-items: center;
}
/* 校对基线行:黄左边+黄底,删除字红底(对照 border-l-yellow-400/bg-yellow-400/8) */
.d-row-base {
  border-left: 4px solid #facc15;
  background: rgba(250, 204, 21, 0.08);
  user-select: none;
}
.d-row-base:hover {
  background: rgba(250, 204, 21, 0.08);
}
.d-rm {
  background: rgba(248, 113, 113, 0.3);
}
/* 有差异的编辑行:绿左边+绿底,新增字绿底 */
.d-row-changed {
  border-left: 4px solid #4ade80;
  background: rgba(74, 222, 128, 0.08);
}
.d-add {
  background: rgba(74, 222, 128, 0.3);
}

.d-num {
  width: 20px;
  flex: none;
  font-size: 11px;
  font-family: var(--vp-font-family-mono);
  color: var(--d-text2);
  padding-top: 3px;
}
.d-row-speaker {
  min-width: 3rem;
  max-width: 6rem;
  flex: none;
  font-size: 11.5px;
  color: var(--d-text2);
  padding-top: 3px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.d-row-text {
  flex: 1;
  min-width: 0;
  line-height: 1.65;
  color: var(--vp-c-text-1);
  word-break: break-word;
}
.d-base-text {
  color: var(--d-text2);
}
.d-editable {
  cursor: text;
}
/* 输入框无框化:文字直接躺在行里,和桌面端 contenteditable 一致 */
input.ed-input {
  border: none;
  background: transparent;
  padding: 0;
  font-size: 14px;
  font-family: inherit;
}
input.ed-input:focus {
  outline: none;
}
input.ed-input::placeholder {
  color: var(--d-text3);
}

.d-row-end {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
  padding-top: 1px;
}
.d-nmark {
  font-size: 11px;
  font-family: var(--vp-font-family-mono);
  color: var(--d-text2);
}
.d-mini {
  width: 22px;
  height: 22px;
  border: 1px solid var(--d-border);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1;
  color: var(--d-text2);
  cursor: pointer;
  transition: color 0.15s ease, border-color 0.15s ease, background-color 0.15s ease;
}
.d-mini:hover {
  color: var(--dp);
  border-color: var(--dp);
}
.d-mini-del:hover {
  color: #ef4444;
  border-color: #ef4444;
  background: rgba(239, 68, 68, 0.08);
}

/* ---- 底栏 ---- */
.d-foot {
  border-top: 1px solid var(--d-border);
  padding: 8px 14px;
  font-size: 12px;
  color: var(--d-text2);
  background: var(--d-surface);
}
.d-foot a {
  color: var(--dp);
  font-weight: 600;
  text-decoration: none;
}
.d-foot a:hover {
  text-decoration: underline;
}
.d-foot-term {
  border-bottom: 1.5px dotted var(--dp);
}

/* ---- 术语悬浮 / Toast(fixed,不受滚动裁剪) ---- */
.d-tip {
  position: fixed;
  transform: translateX(-50%);
  z-index: 60;
  max-width: min(260px, 86vw);
  background: var(--vp-c-bg-elv, var(--vp-c-bg));
  border: 1px solid var(--d-border);
  border-radius: 10px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.2);
  padding: 9px 12px;
  font-size: 12px;
  line-height: 1.55;
  color: var(--vp-c-text-1);
  pointer-events: none;
}
.d-tip-head b {
  font-weight: 600;
}
.d-tip-arrow {
  margin: 0 6px;
  color: var(--d-text2);
}
.d-tip-to {
  color: var(--dp);
  font-weight: 500;
}
.d-tip-note {
  color: var(--d-text2);
  margin-top: 3px;
}
.d-tip-cat {
  font-size: 10px;
  color: var(--d-text3);
  margin-top: 4px;
  opacity: 0.8;
}

.d-toast {
  position: absolute;
  left: 50%;
  bottom: 46px;
  transform: translateX(-50%);
  z-index: 50;
  background: var(--vp-c-bg-elv, var(--vp-c-bg));
  border: 1px solid var(--d-border);
  border-radius: 10px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.22);
  padding: 8px 15px;
  font-size: 12.5px;
  color: var(--vp-c-text-1);
  white-space: nowrap;
}
.d-fade-enter-active,
.d-fade-leave-active {
  transition: opacity 0.18s ease;
}
.d-fade-enter-from,
.d-fade-leave-to {
  opacity: 0;
}

/* ---- 响应式:窄屏收掉侧栏,双栏改上下 ---- */
@media (max-width: 760px) {
  .d-side {
    display: none;
  }
  .d-group {
    grid-template-columns: 1fr;
    gap: 4px;
  }
  .d-cols {
    grid-template-columns: 1fr;
  }
  .d-col-h-r {
    display: none;
  }
}
</style>

<style>
/* 暗色主题跟随桌面端:主色换 #8c79ff(非 scoped,选择器带唯一类不外泄) */
.dark .stx-demo {
  --dp: #8c79ff;
}
</style>
