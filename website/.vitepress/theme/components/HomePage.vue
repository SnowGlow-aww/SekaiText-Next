<script setup lang="ts">
import { withBase } from 'vitepress'
import DownloadButtons from './DownloadButtons.vue'
import DemoEditor from './DemoEditor.vue'

declare const __APP_VERSION__: string
const version = __APP_VERSION__

const workflow = [
  {
    number: '01',
    phase: '译准',
    title: '在语境里完成翻译',
    desc: '活动、卡面与主线剧情直接载入。逐句听语音、看演出，在翻译、校对与合意模式之间把译稿做准。',
    points: ['剧情索引', '逐句语音', 'Live2D 对照'],
    link: '/guide/editor.html',
    accent: '#39c5bb',
  },
  {
    number: '02',
    phase: '打稳',
    title: '让时间轴跟着台词走',
    desc: '识别录屏中的对话画面，自动生成逐句时间轴；长句可按语气和停顿分轴，并保留 Aegisub 精修空间。',
    points: ['自动识别', '分句微调', 'Aegisub 双向同步'],
    link: '/guide/autotiming.html',
    accent: '#6fa8ef',
  },
  {
    number: '03',
    phase: '交付',
    title: '从 ASS 一路做到成片',
    desc: '团队样式、地点横幅与 staff 信息随导出自动应用；需要成片时，直接调用内置 FFmpeg 完成压制。',
    points: ['团队样式', 'ASS 导出', '一键压制'],
    link: '/guide/autotiming.html',
    accent: '#ff69b4',
  },
]

const continuities = [
  {
    label: 'Aegisub',
    title: '精修习惯不用丢',
    desc: '双向同步译文与分句时间；在 Aegisub 里改完，轴机能继续接着做。',
    link: '/guide/autotiming.html',
  },
  {
    label: '本地文稿',
    title: '每一次编辑都有落点',
    desc: '文稿自动建档、自动保存；打轴微调另有 autosave ASS，意外退出也不必从头来。',
    link: '/guide/editor.html',
  },
  {
    label: '团队协作',
    title: '用词有依据，也能复核',
    desc: '术语与人称表集中管理，支持云端提案、审核和本地备份。',
    link: '/guide/glossary.html',
  },
  {
    label: '插件系统',
    title: '需要什么，再安装什么',
    desc: '自动打轴、压制与 Live2D 播放按需启用；插件可在应用内安装和更新。',
    link: '/plugins.html',
  },
]
</script>

<template>
  <div class="home">
    <section class="hero">
      <div class="hero-atmosphere" aria-hidden="true" />
      <div class="container hero-grid">
        <div class="hero-copy">
          <div class="hero-brand">
            <img :src="withBase('/app-icon.png')" alt="" />
            <span>SekaiText Next</span>
          </div>

          <a
            class="release-link"
            href="https://github.com/SnowGlow-aww/SekaiText-Next/releases"
            target="_blank"
            rel="noreferrer"
          >
            <span class="release-dot" />
            v{{ version }}
            <span class="release-separator">·</span>
            查看更新内容
            <span aria-hidden="true">↗</span>
          </a>

          <h1>
            从日文原文，<br />
            <span class="st-gradient-text">到字幕成片</span>
          </h1>
          <p class="hero-lede">
            把剧情载入、翻译校对、自动打轴和压制串成一条工作流。
            少搬几次文件，把时间留给译文和节奏。
          </p>
          <ul class="hero-facts" aria-label="产品信息">
            <li>活动 / 卡面 / 主线剧情</li>
            <li>macOS / Windows</li>
            <li>MIT 开源</li>
          </ul>
          <DownloadButtons align="start" />
        </div>

        <div class="flow-board" aria-label="从剧情原文到成片的工作流程">
          <div class="flow-board-head">
            <span>一篇剧情的完整路径</span>
            <span class="flow-board-status">同一项目内完成</span>
          </div>
          <ol>
            <li>
              <span class="flow-number">01</span>
              <span>
                <strong>载入剧情</strong>
                <small>原文 · 语音 · 演出</small>
              </span>
            </li>
            <li>
              <span class="flow-number">02</span>
              <span>
                <strong>完成译稿</strong>
                <small>翻译 · 校对 · 合意</small>
              </span>
            </li>
            <li>
              <span class="flow-number">03</span>
              <span>
                <strong>生成时间轴</strong>
                <small>自动识别 · 分句 · 精修</small>
              </span>
            </li>
            <li>
              <span class="flow-number">04</span>
              <span>
                <strong>导出与压制</strong>
                <small>ASS · Aegisub · 成片</small>
              </span>
            </li>
          </ol>
          <p>每一步都保留可编辑文件，不把工作锁在应用里。</p>
        </div>
      </div>
    </section>

    <section class="demo-section">
      <div class="container">
        <div class="section-heading demo-heading">
          <div>
            <p class="section-kicker">可交互体验</p>
            <h2>先动手试一行</h2>
          </div>
          <p>
            下面不是静态截图。切换翻译模式、编辑译文、查看校对差异，
            直接感受 SekaiText 的工作方式。
          </p>
        </div>

        <div class="shot-frame">
          <div class="shot-chrome">
            <span class="window-dots" aria-hidden="true">
              <i /><i /><i />
            </span>
            <span class="shot-title">SekaiText Next · 编辑器体验</span>
            <span class="shot-live"><span />可以操作</span>
          </div>
          <DemoEditor />
        </div>
      </div>
    </section>

    <section class="workflow-section">
      <div class="container">
        <div class="section-heading">
          <div>
            <p class="section-kicker">从原文到成片</p>
            <h2>一条工作流，接住每一次交接</h2>
          </div>
          <p>
            原文、译稿、时间轴和成片不再散落在不同工具里；
            需要逐帧精修时，也随时接得上 Aegisub。
          </p>
        </div>

        <div class="workflow-grid">
          <a
            v-for="item in workflow"
            :key="item.number"
            :href="withBase(item.link)"
            class="workflow-item"
            :style="{ '--item-accent': item.accent }"
          >
            <div class="workflow-meta">
              <span>{{ item.number }}</span>
              <span>{{ item.phase }}</span>
            </div>
            <h3>{{ item.title }}</h3>
            <p>{{ item.desc }}</p>
            <ul>
              <li v-for="point in item.points" :key="point">{{ point }}</li>
            </ul>
            <span class="workflow-more">查看使用方式 <span aria-hidden="true">→</span></span>
          </a>
        </div>
      </div>
    </section>

    <section class="continuity-section">
      <div class="container">
        <div class="section-heading">
          <div>
            <p class="section-kicker">融入现有习惯</p>
            <h2>保留熟悉的工具，只减少重复劳动</h2>
          </div>
          <p>
            SekaiText 不要求团队换掉所有做法。它负责整理上下游，
            让已有工具、文件与协作方式更顺畅地接在一起。
          </p>
        </div>

        <div class="continuity-list">
          <a
            v-for="(item, index) in continuities"
            :key="item.title"
            :href="withBase(item.link)"
            class="continuity-row"
          >
            <span class="continuity-index">{{ String(index + 1).padStart(2, '0') }}</span>
            <span class="continuity-body">
              <small>{{ item.label }}</small>
              <strong>{{ item.title }}</strong>
              <span>{{ item.desc }}</span>
            </span>
            <span class="continuity-arrow" aria-hidden="true">↗</span>
          </a>
        </div>
      </div>
    </section>

    <section class="cta-section">
      <div class="container">
        <div class="cta-card">
          <p class="section-kicker">准备好了</p>
          <h2>下一篇剧情，从这里开始</h2>
          <p class="cta-copy">
            SekaiText Next 免费开源。安装主程序后，再按项目需要添加自动打轴、压制或 Live2D 插件。
          </p>
          <DownloadButtons compact />
          <div class="cta-links">
            <a :href="withBase('/guide/getting-started.html')">查看安装说明 <span aria-hidden="true">→</span></a>
            <a href="https://github.com/SnowGlow-aww/SekaiText-Next" target="_blank" rel="noreferrer">
              在 GitHub 查看源码 <span aria-hidden="true">↗</span>
            </a>
          </div>
        </div>
      </div>
    </section>

    <footer class="credits">
      <div class="container">
        <p>
          SekaiText Next 延续
          <a href="https://github.com/Is14w/SekaiText" target="_blank" rel="noreferrer">Is14w/SekaiText</a>
          的创意，由
          <a href="https://github.com/SnowGlow-aww" target="_blank" rel="noreferrer">雪莹ちゃん</a>
          重制并维护。
        </p>
        <p>
          感谢
          <a href="https://github.com/Cinea4678/" target="_blank" rel="noreferrer">Cinea</a>
          提供技术与基础设施支持，也感谢
          <a href="https://github.com/MejiroRina" target="_blank" rel="noreferrer">星雲希凪</a>
          参与界面优化。
        </p>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.home {
  overflow: clip;
}

.container {
  width: min(1160px, calc(100% - 48px));
  margin: 0 auto;
}

.hero {
  position: relative;
  isolation: isolate;
  padding: 96px 0 104px;
}

.hero-atmosphere {
  position: absolute;
  z-index: -1;
  inset: 0;
  overflow: hidden;
  background:
    radial-gradient(circle at 8% 8%, rgba(57, 197, 187, 0.17), transparent 34%),
    radial-gradient(circle at 92% 20%, rgba(255, 105, 180, 0.12), transparent 30%);
}

.hero-atmosphere::after {
  content: '';
  position: absolute;
  width: 560px;
  height: 560px;
  right: 7%;
  top: 4%;
  border: 1px solid color-mix(in srgb, var(--st-teal) 18%, transparent);
  border-radius: 50%;
  box-shadow:
    0 0 0 86px color-mix(in srgb, var(--st-teal) 4%, transparent),
    0 0 0 172px color-mix(in srgb, var(--st-pink) 3%, transparent);
  opacity: 0.8;
}

.hero-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 420px;
  align-items: center;
  gap: clamp(56px, 8vw, 110px);
}

.hero-copy {
  min-width: 0;
}

.hero-brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 24px;
  font-size: 14px;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.hero-brand img {
  width: 42px;
  height: 42px;
  border-radius: 11px;
  box-shadow: 0 10px 26px rgba(57, 197, 187, 0.22);
}

.release-link {
  display: flex;
  width: fit-content;
  align-items: center;
  gap: 7px;
  margin-bottom: 18px;
  color: var(--vp-c-text-2);
  font-size: 13px;
  font-weight: 550;
  text-decoration: none;
}

.release-link:hover {
  color: var(--vp-c-brand-1);
}

.release-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--st-teal);
  box-shadow: 0 0 0 4px rgba(57, 197, 187, 0.13);
}

.release-separator {
  color: var(--vp-c-divider);
}

.hero h1 {
  max-width: 720px;
  margin: 0;
  border: 0;
  padding: 0;
  font-size: clamp(50px, 6.3vw, 80px);
  font-weight: 780;
  letter-spacing: -0.045em;
  line-height: 1.04;
  text-wrap: balance;
}

.hero-lede {
  max-width: 620px;
  margin: 28px 0 20px;
  color: var(--vp-c-text-2);
  font-size: clamp(17px, 2vw, 20px);
  line-height: 1.8;
  text-wrap: pretty;
}

.hero-facts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 24px;
  margin: 0 0 30px;
  padding: 0;
  list-style: none;
  color: var(--vp-c-text-3);
  font-size: 13px;
}

.hero-facts li {
  position: relative;
}

.hero-facts li:not(:last-child)::after {
  content: '';
  position: absolute;
  top: 50%;
  right: -13px;
  width: 3px;
  height: 3px;
  border-radius: 50%;
  background: var(--vp-c-divider);
}

.flow-board {
  position: relative;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--st-teal) 24%, var(--vp-c-divider));
  border-radius: 28px;
  background:
    linear-gradient(150deg, color-mix(in srgb, var(--st-teal) 8%, var(--vp-c-bg)) 0%, var(--vp-c-bg-soft) 42%),
    var(--vp-c-bg-soft);
  box-shadow: 0 28px 80px rgba(11, 18, 29, 0.14);
}

.dark .flow-board {
  box-shadow: 0 32px 90px rgba(0, 0, 0, 0.42);
}

.flow-board::before {
  content: '';
  position: absolute;
  top: -90px;
  right: -60px;
  width: 220px;
  height: 220px;
  border-radius: 50%;
  border: 42px solid color-mix(in srgb, var(--st-pink) 7%, transparent);
  pointer-events: none;
}

.flow-board-head {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 20px 24px;
  border-bottom: 1px solid var(--vp-c-divider);
  font-size: 12px;
  font-weight: 650;
}

.flow-board-status {
  color: var(--vp-c-brand-1);
  font-weight: 550;
}

.flow-board ol {
  position: relative;
  margin: 0;
  padding: 6px 24px;
  list-style: none;
}

.flow-board ol::before {
  content: '';
  position: absolute;
  top: 34px;
  bottom: 34px;
  left: 42px;
  width: 1px;
  background: var(--st-gradient);
  opacity: 0.65;
}

.flow-board li {
  position: relative;
  display: grid;
  grid-template-columns: 38px 1fr;
  align-items: center;
  gap: 14px;
  min-height: 76px;
  border-bottom: 1px solid color-mix(in srgb, var(--vp-c-divider) 72%, transparent);
}

.flow-board li:last-child {
  border-bottom: 0;
}

.flow-number {
  position: relative;
  z-index: 1;
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--st-teal) 25%, var(--vp-c-divider));
  border-radius: 50%;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-3);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.flow-board strong,
.flow-board small {
  display: block;
}

.flow-board strong {
  margin-bottom: 4px;
  font-size: 15px;
  font-weight: 650;
}

.flow-board small {
  color: var(--vp-c-text-3);
  font-size: 12px;
}

.flow-board > p {
  margin: 0;
  padding: 18px 24px 20px;
  border-top: 1px solid var(--vp-c-divider);
  color: var(--vp-c-text-3);
  font-size: 12px;
  line-height: 1.6;
}

.demo-section {
  padding: 92px 0 112px;
  background: var(--vp-c-bg-soft);
}

.section-heading {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(280px, 440px);
  align-items: end;
  gap: 48px;
  margin-bottom: 50px;
}

.section-kicker {
  margin: 0 0 12px;
  color: var(--vp-c-brand-1);
  font-size: 12px;
  font-weight: 750;
  letter-spacing: 0.16em;
}

.section-heading h2,
.cta-card h2 {
  margin: 0;
  border: 0;
  padding: 0;
  color: var(--vp-c-text-1);
  font-size: clamp(31px, 4vw, 46px);
  font-weight: 720;
  letter-spacing: -0.035em;
  line-height: 1.18;
  text-wrap: balance;
}

.section-heading > p {
  margin: 0;
  color: var(--vp-c-text-2);
  font-size: 15px;
  line-height: 1.85;
  text-wrap: pretty;
}

.shot-frame {
  position: relative;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--st-teal) 18%, var(--vp-c-divider));
  border-radius: 18px;
  background: var(--vp-c-bg);
  box-shadow: 0 30px 90px rgba(11, 18, 29, 0.17);
}

.dark .shot-frame {
  box-shadow: 0 32px 100px rgba(0, 0, 0, 0.48);
}

.shot-chrome {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  min-height: 48px;
  padding: 0 16px;
  border-bottom: 1px solid var(--vp-c-divider);
  background: color-mix(in srgb, var(--vp-c-bg-soft) 84%, transparent);
}

.window-dots {
  display: flex;
  gap: 7px;
}

.window-dots i {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--vp-c-divider);
}

.window-dots i:first-child {
  background: #ff605c;
}

.window-dots i:nth-child(2) {
  background: #ffbd44;
}

.window-dots i:nth-child(3) {
  background: #00ca4e;
}

.shot-title {
  color: var(--vp-c-text-3);
  font-size: 12px;
}

.shot-live {
  display: inline-flex;
  justify-self: end;
  align-items: center;
  gap: 6px;
  color: var(--vp-c-brand-1);
  font-size: 11px;
  font-weight: 650;
}

.shot-live > span {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--st-teal);
  box-shadow: 0 0 0 4px rgba(57, 197, 187, 0.12);
}

.workflow-section,
.continuity-section {
  padding: 112px 0;
}

.workflow-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  border-top: 1px solid var(--vp-c-divider);
  border-bottom: 1px solid var(--vp-c-divider);
}

.workflow-item {
  position: relative;
  display: flex;
  min-width: 0;
  flex-direction: column;
  padding: 34px clamp(24px, 3vw, 38px) 38px;
  color: inherit;
  text-decoration: none;
}

.workflow-item + .workflow-item {
  border-left: 1px solid var(--vp-c-divider);
}

.workflow-item::before {
  content: '';
  position: absolute;
  top: -1px;
  left: 0;
  width: 0;
  height: 2px;
  background: var(--item-accent);
  transition: width 0.3s ease;
}

.workflow-item:hover {
  background: color-mix(in srgb, var(--item-accent) 4%, transparent);
}

.workflow-item:hover::before {
  width: 100%;
}

.workflow-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 42px;
  color: var(--vp-c-text-3);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.12em;
}

.workflow-meta span:last-child {
  color: var(--item-accent);
}

.workflow-item h3 {
  margin: 0 0 16px;
  color: var(--vp-c-text-1);
  font-size: 21px;
  font-weight: 680;
  letter-spacing: -0.015em;
  line-height: 1.35;
}

.workflow-item > p {
  margin: 0 0 24px;
  color: var(--vp-c-text-2);
  font-size: 14px;
  line-height: 1.8;
  text-wrap: pretty;
}

.workflow-item ul {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  margin: auto 0 28px;
  padding: 0;
  list-style: none;
  color: var(--vp-c-text-3);
  font-size: 12px;
}

.workflow-item li {
  position: relative;
}

.workflow-item li:not(:last-child)::after {
  content: '·';
  position: absolute;
  right: -10px;
  color: var(--vp-c-divider);
}

.workflow-more {
  color: var(--item-accent);
  font-size: 13px;
  font-weight: 650;
}

.continuity-section {
  background: var(--vp-c-bg-soft);
}

.continuity-list {
  border-top: 1px solid var(--vp-c-divider);
}

.continuity-row {
  display: grid;
  grid-template-columns: 70px minmax(0, 1fr) 32px;
  align-items: center;
  gap: 28px;
  min-height: 142px;
  padding: 24px 8px;
  border-bottom: 1px solid var(--vp-c-divider);
  color: inherit;
  text-decoration: none;
  transition: padding 0.25s ease, background 0.25s ease;
}

.continuity-row:hover {
  padding-right: 20px;
  padding-left: 20px;
  background: color-mix(in srgb, var(--st-teal) 5%, transparent);
}

.continuity-index {
  color: var(--vp-c-text-3);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.continuity-body {
  display: grid;
  grid-template-columns: minmax(190px, 0.75fr) minmax(0, 1.25fr);
  align-items: center;
  gap: 10px 36px;
}

.continuity-body small {
  grid-column: 1;
  color: var(--vp-c-brand-1);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.1em;
}

.continuity-body strong {
  grid-column: 1;
  color: var(--vp-c-text-1);
  font-size: 18px;
  font-weight: 650;
}

.continuity-body > span {
  grid-column: 2;
  grid-row: 1 / span 2;
  color: var(--vp-c-text-2);
  font-size: 14px;
  line-height: 1.8;
  text-wrap: pretty;
}

.continuity-arrow {
  color: var(--vp-c-text-3);
  font-size: 17px;
}

.cta-section {
  padding: 112px 0 60px;
}

.cta-card {
  position: relative;
  overflow: hidden;
  padding: 72px clamp(24px, 7vw, 84px);
  border: 1px solid color-mix(in srgb, var(--st-teal) 20%, var(--vp-c-divider));
  border-radius: 28px;
  background:
    radial-gradient(circle at 0% 0%, rgba(57, 197, 187, 0.11), transparent 36%),
    radial-gradient(circle at 100% 100%, rgba(255, 105, 180, 0.09), transparent 34%),
    var(--vp-c-bg-soft);
  text-align: center;
}

.cta-copy {
  max-width: 640px;
  margin: 18px auto 32px;
  color: var(--vp-c-text-2);
  font-size: 15px;
  line-height: 1.8;
}

.cta-links {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: 16px 28px;
  margin-top: 26px;
  font-size: 13px;
}

.cta-links a {
  color: var(--vp-c-text-2);
  font-weight: 600;
  text-decoration: none;
}

.cta-links a:hover {
  color: var(--vp-c-brand-1);
}

.credits {
  padding: 32px 0 24px;
  border-top: 1px solid var(--vp-c-divider);
  color: var(--vp-c-text-3);
  font-size: 12px;
  line-height: 1.7;
  text-align: center;
}

.credits p {
  margin: 3px 0;
}

.credits a {
  color: var(--vp-c-text-2);
}

.credits a:hover {
  color: var(--vp-c-brand-1);
}

@media (max-width: 900px) {
  .hero {
    padding: 72px 0 88px;
  }

  .hero-grid {
    grid-template-columns: 1fr;
    gap: 56px;
  }

  .hero-copy {
    text-align: center;
  }

  .hero-brand,
  .release-link {
    margin-right: auto;
    margin-left: auto;
  }

  .hero-lede {
    margin-right: auto;
    margin-left: auto;
  }

  .hero-facts {
    justify-content: center;
  }

  .flow-board {
    width: min(100%, 560px);
    margin: 0 auto;
    text-align: left;
  }

  .section-heading {
    grid-template-columns: 1fr;
    align-items: start;
    gap: 20px;
  }

  .section-heading > p {
    max-width: 660px;
  }

  .workflow-grid {
    grid-template-columns: 1fr;
  }

  .workflow-item + .workflow-item {
    border-top: 1px solid var(--vp-c-divider);
    border-left: 0;
  }

  .workflow-meta {
    margin-bottom: 24px;
  }
}

@media (max-width: 640px) {
  .container {
    width: min(100% - 32px, 1160px);
  }

  .hero {
    padding: 48px 0 64px;
  }

  .hero-atmosphere::after {
    width: 360px;
    height: 360px;
    right: -150px;
    top: 60px;
  }

  .hero-brand {
    margin-bottom: 20px;
  }

  .hero h1 {
    font-size: clamp(43px, 13vw, 58px);
  }

  .hero-lede {
    margin-top: 22px;
    font-size: 16px;
    line-height: 1.75;
  }

  .hero-facts {
    gap: 6px 18px;
    margin-bottom: 26px;
    font-size: 12px;
  }

  .hero-facts li:not(:last-child)::after {
    right: -10px;
  }

  .flow-board {
    border-radius: 20px;
  }

  .flow-board-head {
    padding: 16px 18px;
  }

  .flow-board ol {
    padding: 4px 18px;
  }

  .flow-board ol::before {
    left: 36px;
  }

  .flow-board > p {
    padding: 16px 18px 18px;
  }

  .demo-section,
  .workflow-section,
  .continuity-section,
  .cta-section {
    padding: 72px 0;
  }

  .section-heading {
    margin-bottom: 34px;
  }

  .section-heading h2,
  .cta-card h2 {
    font-size: 31px;
  }

  .shot-frame {
    border-radius: 14px;
  }

  .shot-chrome {
    grid-template-columns: auto 1fr;
    min-height: 44px;
    padding: 0 12px;
  }

  .shot-title {
    padding-left: 10px;
    text-align: right;
  }

  .shot-live {
    display: none;
  }

  .workflow-item {
    padding: 28px 20px 32px;
  }

  .continuity-row {
    grid-template-columns: 36px minmax(0, 1fr) 20px;
    gap: 14px;
    min-height: 0;
    padding: 24px 0;
  }

  .continuity-row:hover {
    padding-right: 8px;
    padding-left: 8px;
  }

  .continuity-body {
    grid-template-columns: 1fr;
    gap: 5px;
  }

  .continuity-body small,
  .continuity-body strong,
  .continuity-body > span {
    grid-column: 1;
    grid-row: auto;
  }

  .continuity-body > span {
    margin-top: 6px;
    font-size: 13px;
  }

  .cta-card {
    padding: 48px 20px;
    border-radius: 20px;
  }

  .credits {
    text-align: left;
  }
}

@media (prefers-reduced-motion: reduce) {
  .workflow-item::before,
  .continuity-row {
    transition: none;
  }
}
</style>
