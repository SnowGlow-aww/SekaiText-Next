<script setup lang="ts">
import { withBase } from 'vitepress'
import DownloadButtons from './DownloadButtons.vue'
import DemoEditor from './DemoEditor.vue'

declare const __APP_VERSION__: string
const version = __APP_VERSION__

// 每张功能卡一个主题色：图标底、悬停描边与阴影都跟着走
const features: {
  icon: string; title: string; desc: string; accent: string
  tag?: string; link?: string; more?: string
}[] = [
  {
    icon: '📝',
    title: '剧情翻译编辑器',
    desc: '原文 / 译文双栏对照，活动·卡面·主线剧情自动载入，支持语音播放、\\N 换行控制与多格式导出。',
    accent: '#39c5bb',
  },
  {
    icon: '⚡',
    title: '自动打轴',
    tag: '插件',
    desc: '内置 SekaiCoreEngine 识别录屏画面，自动对齐每句台词生成字幕时间轴，可逐行微调并与 Aegisub 双向同步。',
    accent: '#6fa8ef',
  },
  {
    icon: '🎬',
    title: '一键压制',
    tag: '插件',
    desc: '集成 FFmpeg + libass，内置团队字幕样式，从翻译稿到成片视频一条龙。',
    accent: '#f5a623',
  },
  {
    icon: '📚',
    title: '术语库与协作',
    desc: '术语 / 人称表云端同步，支持提案与审核，保障用词一致性。',
    accent: '#8c79ff',
  },
  {
    icon: '🧩',
    title: '插件市场',
    desc: '热加载插件系统，功能按需扩展；插件市场一键安装、自动更新，全程 CDN 加速。',
    accent: '#4ade80',
    // 卡片整体可点，跳到全部插件一览（含主页未展示的 Live2D 等）
    link: '/plugins.html',
    more: '浏览全部插件 →',
  },
]

const steps = [
  { n: '01', color: '#39c5bb', title: '下载安装', desc: '支持 macOS (Apple Silicon) 与 Windows (x64)，开箱即用，无需配置环境。' },
  { n: '02', color: '#6fa8ef', title: '选择剧情', desc: '内置剧情索引，选择活动 / 卡面 / 主线，原文与语音自动拉取。' },
  { n: '03', color: '#ff69b4', title: '翻译到出片', desc: '翻译 → 校对 → 自动打轴 → 一键压制，一条流水线全部在应用内完成。' },
]
</script>

<template>
  <div class="home">
    <!-- Hero -->
    <section class="hero">
      <div class="hero-bg" aria-hidden="true">
        <div class="blob blob-teal" />
        <div class="blob blob-pink" />
        <div class="blob blob-blue" />
      </div>
      <div class="container hero-inner">
        <img class="hero-icon" :src="withBase('/app-icon.png')" alt="SekaiText Next" />
        <div class="hero-badge-row">
          <a
            class="hero-badge"
            href="https://github.com/SnowGlow-aww/SekaiText-Next/releases"
            target="_blank"
            rel="noreferrer"
          >
            <span class="hero-badge-dot" />v{{ version }} 现已发布<span class="hero-badge-link">更新日志 →</span>
          </a>
        </div>
        <h1 class="hero-title st-gradient-text">SekaiText Next</h1>
        <p class="hero-tagline">「プロジェクトセカイ」剧情翻译一站式工作台</p>
        <p class="hero-sub">翻译 · 校对 · 自动打轴 · 一键压制 · Live2D 剧情播放 · 术语库协作</p>
        <DownloadButtons />

        <!-- 在线体验编辑器 -->
        <div class="shot-glow">
          <div class="shot-frame">
            <div class="shot-chrome">
              <span class="dot dot-r" /><span class="dot dot-y" /><span class="dot dot-g" />
              <span class="shot-title">SekaiText Next — 在线体验</span>
              <span class="shot-live"><span class="shot-live-dot" />可交互</span>
            </div>
            <DemoEditor />
          </div>
        </div>
      </div>
    </section>

    <!-- Features -->
    <section class="section">
      <div class="container">
        <h2 class="section-title">全新重构，由 <span class="accent-text">Tauri</span> 驱动</h2>
        <p class="section-sub">为剧情翻译工作流打造的一站式解决方案</p>
        <div class="feature-grid">
          <component
            :is="f.link ? 'a' : 'div'"
            v-for="f in features"
            :key="f.title"
            :href="f.link ? withBase(f.link) : undefined"
            class="feature-card"
            :style="{ '--fc': f.accent }"
          >
            <div class="feature-icon">{{ f.icon }}</div>
            <h3 class="feature-title">
              {{ f.title }}<span v-if="f.tag" class="feature-tag">{{ f.tag }}</span>
            </h3>
            <p class="feature-desc">{{ f.desc }}</p>
            <span v-if="f.more" class="feature-more">{{ f.more }}</span>
          </component>
        </div>
      </div>
    </section>

    <!-- Steps -->
    <section class="section section-alt">
      <div class="container">
        <h2 class="section-title">三步上手</h2>
        <div class="steps">
          <div v-for="s in steps" :key="s.n" class="step">
            <div class="step-n" :style="{ color: s.color }">{{ s.n }}</div>
            <h3 class="step-title">{{ s.title }}</h3>
            <p class="step-desc">{{ s.desc }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- CTA -->
    <section class="section cta">
      <div class="container">
        <div class="cta-card">
          <h2 class="section-title">现在开始</h2>
          <p class="section-sub">免费、开源（MIT），内置自动更新，国内下载全程 CDN 加速。</p>
          <DownloadButtons />
          <p class="cta-links">
            <a :href="withBase('/guide/index.html')">📖 阅读使用指南</a>
            <a href="https://github.com/SnowGlow-aww/SekaiText-Next" target="_blank" rel="noreferrer">⭐ GitHub 仓库</a>
          </p>
        </div>
      </div>
    </section>

    <!-- Credits -->
    <section class="credits">
      <div class="container">
        <p>
          创意来源 <a href="https://github.com/Is14w/SekaiText" target="_blank" rel="noreferrer">Is14w/SekaiText</a> ·
          由 <a href="https://github.com/SnowGlow-aww" target="_blank" rel="noreferrer">雪莹ちゃん</a> 重制维护
        </p>
        <p>
          感谢 <a href="https://github.com/Cinea4678/" target="_blank" rel="noreferrer">Cinea</a> 提供的技术/账户/服务器支持 ·
          感谢 <a href="https://github.com/MejiroRina" target="_blank" rel="noreferrer">星雲希凪</a> 提供的 UI 优化
        </p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.container {
  max-width: 1080px;
  margin: 0 auto;
  padding: 0 24px;
}

/* ---------- Hero ---------- */
.hero {
  position: relative;
  overflow: hidden;
  padding: 80px 0 72px;
  text-align: center;
}
.hero-bg {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(90px);
  opacity: 0.3;
}
.dark .blob {
  opacity: 0.2;
}
.blob-teal {
  width: 480px;
  height: 480px;
  background: #39c5bb;
  top: -180px;
  left: -120px;
}
.blob-pink {
  width: 420px;
  height: 420px;
  background: #ff69b4;
  top: -120px;
  right: -100px;
}
.blob-blue {
  width: 380px;
  height: 380px;
  background: #6fa8ef;
  bottom: -220px;
  left: 40%;
}
.hero-inner {
  position: relative;
}
.hero-icon {
  width: 108px;
  height: 108px;
  border-radius: 25px;
  box-shadow: 0 16px 40px rgba(57, 197, 187, 0.35);
  margin: 0 auto 20px;
  animation: float 5s ease-in-out infinite;
}
@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-8px); }
}
/* 版本徽章：独占一行居中（hero-title 是 inline-block，直接相邻会挤到同一行） */
.hero-badge-row {
  margin-bottom: 14px;
}
.hero-badge {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 5px 14px;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, var(--st-teal) 35%, var(--vp-c-divider));
  background: color-mix(in srgb, var(--st-teal) 7%, var(--vp-c-bg));
  font-size: 12.5px;
  font-weight: 500;
  color: var(--vp-c-text-2);
  text-decoration: none;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}
.hero-badge:hover {
  border-color: var(--st-teal);
  box-shadow: 0 4px 16px rgba(57, 197, 187, 0.2);
}
.hero-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--st-teal);
  box-shadow: 0 0 0 3px rgba(57, 197, 187, 0.2);
}
.hero-badge-link {
  color: var(--vp-c-brand-1);
  font-weight: 600;
}
.hero-title {
  font-size: clamp(40px, 7vw, 64px);
  font-weight: 800;
  letter-spacing: -0.02em;
  line-height: 1.15;
  margin: 0 0 10px;
}
.hero-tagline {
  font-size: clamp(18px, 3vw, 24px);
  font-weight: 600;
  color: var(--vp-c-text-1);
  margin: 0 0 8px;
}
.hero-sub {
  font-size: 15px;
  color: var(--vp-c-text-2);
  margin: 0 0 32px;
}

/* 体验窗口：渐变光环 + 窗口框 */
.shot-glow {
  position: relative;
  margin: 56px auto 0;
  max-width: 980px;
}
.shot-glow::before {
  content: '';
  position: absolute;
  inset: -1px;
  border-radius: 15px;
  background: var(--st-gradient);
  opacity: 0.35;
  filter: blur(14px);
  z-index: 0;
}
.dark .shot-glow::before {
  opacity: 0.3;
}
.shot-frame {
  position: relative;
  z-index: 1;
  border-radius: 14px;
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.14);
  overflow: hidden;
}
.dark .shot-frame {
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.5);
}
.shot-chrome {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--vp-c-divider);
}
.dot {
  width: 11px;
  height: 11px;
  border-radius: 50%;
}
.dot-r { background: #ff5f57; }
.dot-y { background: #febc2e; }
.dot-g { background: #28c840; }
.shot-title {
  flex: 1;
  text-align: center;
  font-size: 12px;
  color: var(--vp-c-text-3);
}
.shot-live {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 500;
  color: #16a34a;
  background: rgba(40, 200, 64, 0.12);
}
.shot-live-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: #28c840;
  animation: live-pulse 1.8s ease-in-out infinite;
}
@keyframes live-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

/* ---------- Sections ---------- */
.section {
  padding: 80px 0;
  text-align: center;
}
.section-alt {
  background: var(--vp-c-bg-soft);
}
.section-title {
  font-size: clamp(26px, 4vw, 34px);
  font-weight: 700;
  letter-spacing: -0.01em;
  margin: 0 0 12px;
  border: none;
  padding: 0;
}
.section-sub {
  color: var(--vp-c-text-2);
  font-size: 15px;
  margin: 0 auto 44px;
  max-width: 560px;
}
.accent-text {
  color: var(--vp-c-brand-1);
}

/* Features：每卡自带主题色 --fc */
.feature-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
  text-align: left;
}
@media (max-width: 860px) {
  .feature-grid { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 560px) {
  .feature-grid { grid-template-columns: 1fr; }
}
.feature-card {
  background: var(--vp-c-bg-soft);
  border: 1px solid var(--vp-c-divider);
  border-radius: 16px;
  padding: 24px;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}
.feature-card:hover {
  transform: translateY(-4px);
  border-color: color-mix(in srgb, var(--fc) 60%, transparent);
  box-shadow: 0 12px 32px color-mix(in srgb, var(--fc) 18%, transparent);
}
.feature-icon {
  width: 46px;
  height: 46px;
  border-radius: 12px;
  background: color-mix(in srgb, var(--fc) 13%, transparent);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  margin-bottom: 14px;
}
.feature-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 8px;
}
.feature-tag {
  flex: none;
  font-size: 10.5px;
  font-weight: 500;
  line-height: 1;
  padding: 3px 7px;
  border-radius: 999px;
  color: var(--fc);
  background: color-mix(in srgb, var(--fc) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--fc) 28%, transparent);
}
.feature-desc {
  font-size: 13.5px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
  margin: 0;
}
/* 可点击的功能卡（如插件市场 → 插件一览）：去链接默认样式，加“更多”指引 */
a.feature-card {
  display: block;
  text-decoration: none;
  color: inherit;
}
.feature-more {
  display: inline-block;
  margin-top: 12px;
  font-size: 13px;
  font-weight: 500;
  color: var(--fc);
}

/* Steps：桌面端步骤间加箭头连线 */
.steps {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
}
@media (max-width: 700px) {
  .steps { grid-template-columns: 1fr; }
}
.step {
  position: relative;
  padding: 8px 16px;
}
@media (min-width: 701px) {
  .step:not(:last-child)::after {
    content: '→';
    position: absolute;
    right: -18px;
    top: 16px;
    font-size: 22px;
    color: var(--vp-c-text-3);
    opacity: 0.6;
  }
}
.step-n {
  font-size: 40px;
  font-weight: 800;
  margin-bottom: 6px;
}
.step-title {
  font-size: 17px;
  font-weight: 600;
  margin: 0 0 8px;
}
.step-desc {
  font-size: 13.5px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
  margin: 0;
}

/* CTA：柔和渐变卡片收束 */
.cta {
  padding-top: 40px;
}
.cta-card {
  position: relative;
  overflow: hidden;
  border-radius: 24px;
  border: 1px solid var(--vp-c-divider);
  padding: 56px 32px 48px;
  background:
    radial-gradient(600px 220px at 12% 0%, rgba(57, 197, 187, 0.1), transparent 70%),
    radial-gradient(600px 220px at 88% 100%, rgba(255, 105, 180, 0.1), transparent 70%),
    var(--vp-c-bg-soft);
}
.cta-links {
  margin-top: 26px;
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: 26px;
  font-size: 14px;
}
.cta-links a {
  color: var(--vp-c-brand-1);
  text-decoration: none;
  font-weight: 500;
}
.cta-links a:hover {
  text-decoration: underline;
}

/* Credits */
.credits {
  border-top: 1px solid var(--vp-c-divider);
  padding: 28px 0 12px;
  text-align: center;
  font-size: 12.5px;
  color: var(--vp-c-text-3);
}
.credits p {
  margin: 4px 0;
}
.credits a {
  color: var(--vp-c-text-2);
}
.credits a:hover {
  color: var(--vp-c-brand-1);
}
</style>
