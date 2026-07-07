<script setup lang="ts">
import { ref, computed } from 'vue'
import { withBase } from 'vitepress'

// —— 术语库（演示数据）——
interface Term {
  t: string
  to: string
  note: string
}
const TERMS: Term[] = [
  { t: 'ナイトコード', to: 'Nightcord', note: '组内统一译法 · 不音译' },
  { t: 'セカイ', to: 'SEKAI', note: '组内约定 · 保留不译' },
]

// —— 示例剧情（宣传用创作，非游戏原文）—— 颜色为角色官方形象色
const KANADE = { speaker: '宵崎奏', avatar: '奏', color: '#BB6588' }
const MAFUYU = { speaker: '朝比奈まふゆ', avatar: 'ま', color: '#8888CC' }
const ENA = { speaker: '東雲絵名', avatar: '絵', color: '#CCAA88' }
const MIZUKI = { speaker: '暁山瑞希', avatar: '瑞', color: '#DDAACC' }

const lines = ref([
  {
    ...KANADE,
    jp: '……新曲、もうすぐ完成しそう。今夜もナイトコードで集まれる？',
    cn: '……新歌快要完成了。今晚也能在Nightcord集合吗？',
  },
  {
    ...MAFUYU,
    jp: 'うん、大丈夫。セカイの方も、少し気になるけど。',
    cn: '嗯，可以。虽然SEKAI那边也有点在意。',
  },
  {
    ...ENA,
    jp: 'ねえ、この歌詞……海外のファンにも伝わるのかな。',
    cn: '',
  },
  {
    ...MIZUKI,
    jp: '字幕をつければいいんじゃない？ミズキ、いいツール知ってるよ☆',
    cn: '',
  },
  {
    ...KANADE,
    jp: '……SekaiText。翻訳も、字幕も、これひとつでできる。',
    cn: '',
  },
  {
    ...ENA,
    jp: '宣伝じゃん！（笑）',
    cn: '',
  },
])

// 原文按术语切分成片段，命中的片段渲染成可悬停术语
const termRe = new RegExp(`(${TERMS.map((x) => x.t).join('|')})`, 'g')
function segments(jp: string) {
  return jp
    .split(termRe)
    .filter((s) => s !== '')
    .map((s) => ({ text: s, term: TERMS.find((x) => x.t === s) || null }))
}

const done = computed(() => lines.value.filter((l) => l.cn.trim()).length)
const allDone = computed(() => done.value === lines.value.length)

const inputs = ref<HTMLInputElement[]>([])
function onEnter(e: KeyboardEvent, i: number) {
  // 中文输入法里回车是「确认候选词」，不能当成跳行
  if (e.isComposing) return
  e.preventDefault()
  inputs.value[i + 1]?.focus()
}

const toast = ref('')
let toastTimer: ReturnType<typeof setTimeout> | undefined
function locked(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (toast.value = ''), 2200)
}
</script>

<template>
  <div class="demo">
    <!-- 工具栏 -->
    <div class="demo-toolbar">
      <span class="demo-story">25時、ナイトコードで。 · 示例剧情</span>
      <span class="demo-progress" :class="{ 'is-done': allDone }">
        {{ allDone ? '✓ 全部完成！' : `已翻译 ${done}/${lines.length}` }}
      </span>
      <button class="demo-tool" @click="locked('语音试听在桌面版中开放')">▶ 试听</button>
      <button class="demo-tool" @click="locked('导出 .ass / 一键压制在桌面版中开放')">导出</button>
    </div>

    <!-- 对话行 -->
    <div class="demo-lines">
      <div v-for="(l, i) in lines" :key="i" class="demo-line">
        <span
          class="demo-avatar"
          :style="{ color: l.color, background: l.color + '2e', borderColor: l.color + '66' }"
        >
          {{ l.avatar }}
        </span>
        <div class="demo-texts">
          <div class="demo-meta">{{ l.speaker }}</div>
          <div class="demo-jp">
            <template v-for="(s, j) in segments(l.jp)" :key="j">
              <span v-if="s.term" class="term">
                {{ s.text }}
                <span class="term-tip">
                  <b>{{ s.term.t }}</b> → {{ s.term.to }}<i>{{ s.term.note }}</i>
                </span>
              </span>
              <template v-else>{{ s.text }}</template>
            </template>
          </div>
          <input
            :ref="(el) => (inputs[i] = el as HTMLInputElement)"
            v-model="l.cn"
            class="demo-cn"
            type="text"
            placeholder="输入译文，回车跳到下一句…"
            @keydown.enter="onEnter($event, i)"
          />
        </div>
      </div>
    </div>

    <!-- 底栏 -->
    <div class="demo-foot">
      <template v-if="allDone">
        🎉 这就是 SekaiText 的手感 —— <a :href="withBase('/download.html')">下载桌面版</a>，打轴、压制、Live2D 全都有。
      </template>
      <template v-else>
        ↑ 这是可交互体验版：试着翻译几句，悬停<span class="foot-term">虚线词</span>查看术语库统一译法。示例文本为宣传用创作，非游戏原文。
      </template>
    </div>

    <Transition name="toast">
      <div v-if="toast" class="demo-toast">{{ toast }}</div>
    </Transition>
  </div>
</template>

<style scoped>
.demo {
  position: relative;
  text-align: left;
  background: var(--vp-c-bg);
}

/* 工具栏 */
.demo-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 16px;
  border-bottom: 1px solid var(--vp-c-divider);
  font-size: 12.5px;
}
.demo-story {
  font-weight: 600;
  color: var(--vp-c-text-1);
  margin-right: auto;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.demo-progress {
  color: var(--vp-c-text-2);
  background: var(--vp-c-default-soft);
  border-radius: 20px;
  padding: 2px 10px;
  white-space: nowrap;
}
.demo-progress.is-done {
  color: #fff;
  background: var(--st-gradient);
}
.demo-tool {
  border: 1px solid var(--vp-c-divider);
  border-radius: 7px;
  padding: 3px 10px;
  font-size: 12px;
  color: var(--vp-c-text-2);
  background: var(--vp-c-bg-soft);
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease;
  white-space: nowrap;
}
.demo-tool:hover {
  border-color: var(--st-teal);
  color: var(--vp-c-brand-1);
}

/* 对话行 */
.demo-lines {
  max-height: 400px;
  overflow-y: auto;
  padding: 14px 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.demo-line {
  display: flex;
  gap: 11px;
}
.demo-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid transparent;
  font-size: 13px;
  font-weight: 700;
  margin-top: 2px;
}
.demo-texts {
  flex: 1;
  min-width: 0;
}
.demo-meta {
  font-size: 11.5px;
  color: var(--vp-c-text-3);
  margin-bottom: 2px;
}
.demo-jp {
  font-size: 14px;
  line-height: 1.65;
  color: var(--vp-c-text-1);
  margin-bottom: 6px;
}
.demo-cn {
  width: 100%;
  font-size: 13.5px;
  padding: 6px 10px;
  border-radius: 8px;
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-brand-1);
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.demo-cn:focus {
  outline: none;
  border-color: var(--st-teal);
  box-shadow: 0 0 0 3px var(--vp-c-brand-soft);
}
.demo-cn::placeholder {
  color: var(--vp-c-text-3);
}

/* 术语高亮 */
.term {
  position: relative;
  border-bottom: 1.5px dashed var(--st-teal);
  cursor: help;
  color: var(--vp-c-brand-1);
}
.term-tip {
  /* 弹在词的下方：demo-lines 是滚动容器，往上弹第一行会被裁掉 */
  display: none;
  position: absolute;
  top: calc(100% + 7px);
  left: 50%;
  transform: translateX(-50%);
  z-index: 10;
  white-space: nowrap;
  background: var(--vp-c-bg-elv, var(--vp-c-bg));
  border: 1px solid var(--vp-c-divider);
  border-radius: 9px;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.18);
  padding: 7px 12px;
  font-size: 12px;
  color: var(--vp-c-text-1);
  line-height: 1.5;
}
.term-tip b {
  color: var(--vp-c-brand-1);
}
.term-tip i {
  display: block;
  font-style: normal;
  color: var(--vp-c-text-3);
  font-size: 11px;
}
.term:hover .term-tip {
  display: block;
}

/* 底栏 */
.demo-foot {
  border-top: 1px solid var(--vp-c-divider);
  padding: 9px 16px;
  font-size: 12px;
  color: var(--vp-c-text-2);
}
.demo-foot a {
  color: var(--vp-c-brand-1);
  font-weight: 600;
  text-decoration: none;
}
.demo-foot a:hover {
  text-decoration: underline;
}
.foot-term {
  border-bottom: 1.5px dashed var(--st-teal);
  color: var(--vp-c-brand-1);
}

/* Toast */
.demo-toast {
  position: absolute;
  left: 50%;
  bottom: 52px;
  transform: translateX(-50%);
  background: var(--vp-c-bg-elv, var(--vp-c-bg));
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.22);
  padding: 9px 16px;
  font-size: 12.5px;
  color: var(--vp-c-text-1);
  white-space: nowrap;
}
.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(6px);
}
</style>
