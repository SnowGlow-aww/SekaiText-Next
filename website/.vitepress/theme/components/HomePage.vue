<script setup lang="ts">
import { withBase } from 'vitepress'
import DownloadButtons from './DownloadButtons.vue'
import DemoEditor from './DemoEditor.vue'

const features = [
  {
    icon: '📝',
    title: '剧情翻译编辑器',
    desc: '原文 / 译文双栏对照，活动·卡面·主线剧情一键载入，支持语音试听、\\N 换行控制与多格式导出。',
  },
  {
    icon: '⚡',
    title: '自动打轴',
    desc: '内置 SekaiCoreEngine 识别录屏画面，自动对齐每句台词生成字幕时间轴，实测全量匹配。',
  },
  {
    icon: '🎬',
    title: '一键压制',
    desc: '集成 FFmpeg + libass，翻译打轴完成后直接输出成品视频，无需再开第二个工具。',
  },
  {
    icon: '🎭',
    title: 'Live2D 剧情播放器',
    desc: '内置播放器还原游戏演出——表情、动作、语音同步播放，边看边翻不出戏。',
  },
  {
    icon: '📚',
    title: '术语库与协作',
    desc: '术语 / 人称表云端同步，提案审核流程，数百人翻译团队协作实测可用。',
  },
  {
    icon: '🧩',
    title: '插件市场',
    desc: '热加载插件系统，功能按需扩展；插件市场一键安装、自动更新，全程 CDN 加速。',
  },
]

const steps = [
  { n: '01', title: '下载安装', desc: '支持 macOS (Apple Silicon) 与 Windows (x64)，开箱即用，无需配置环境。' },
  { n: '02', title: '选择剧情', desc: '内置剧情索引，选择活动 / 卡面 / 主线，原文与语音自动拉取。' },
  { n: '03', title: '翻译到出片', desc: '翻译 → 校对 → 自动打轴 → 一键压制，一条流水线全部在应用内完成。' },
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
        <h1 class="hero-title st-gradient-text">SekaiText Next</h1>
        <p class="hero-tagline">「プロジェクトセカイ」剧情翻译一站式工作台</p>
        <p class="hero-sub">翻译 · 校对 · 自动打轴 · 一键压制 · Live2D 剧情播放 · 术语库协作</p>
        <DownloadButtons />

        <!-- 在线体验编辑器 -->
        <div class="shot-frame">
          <div class="shot-chrome">
            <span class="dot dot-r" /><span class="dot dot-y" /><span class="dot dot-g" />
            <span class="shot-title">SekaiText Next — 在线体验</span>
          </div>
          <DemoEditor />
        </div>
      </div>
    </section>

    <!-- Features -->
    <section class="section">
      <div class="container">
        <h2 class="section-title">从原文到成品视频，<span class="st-gradient-text">一个应用搞定</span></h2>
        <p class="section-sub">为 PJSK 翻译组打造的完整工作流，不再需要在五六个工具之间来回切换。</p>
        <div class="feature-grid">
          <div v-for="f in features" :key="f.title" class="feature-card">
            <div class="feature-icon">{{ f.icon }}</div>
            <h3 class="feature-title">{{ f.title }}</h3>
            <p class="feature-desc">{{ f.desc }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- Steps -->
    <section class="section section-alt">
      <div class="container">
        <h2 class="section-title">三步上手</h2>
        <div class="steps">
          <div v-for="s in steps" :key="s.n" class="step">
            <div class="step-n st-gradient-text">{{ s.n }}</div>
            <h3 class="step-title">{{ s.title }}</h3>
            <p class="step-desc">{{ s.desc }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- CTA -->
    <section class="section cta">
      <div class="container">
        <h2 class="section-title">现在开始</h2>
        <p class="section-sub">免费、开源（MIT），内置自动更新，国内下载全程 CDN 加速。</p>
        <DownloadButtons />
        <p class="cta-links">
          <a :href="withBase('/guide/index.html')">📖 阅读使用指南</a>
          <a href="https://github.com/SnowGlow-aww/SekaiText-Next" target="_blank" rel="noreferrer">⭐ GitHub 仓库</a>
        </p>
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
          感谢 <a href="https://github.com/Cinea4678/" target="_blank" rel="noreferrer">Cinea</a> 提供的技术与账户支持 ·
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
  padding: 88px 0 72px;
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
  opacity: 0.35;
}
.dark .blob {
  opacity: 0.22;
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
  width: 112px;
  height: 112px;
  border-radius: 26px;
  box-shadow: 0 16px 40px rgba(57, 197, 187, 0.35);
  margin: 0 auto 22px;
  animation: float 5s ease-in-out infinite;
}
@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-8px); }
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

/* 截图窗口 */
.shot-frame {
  margin: 56px auto 0;
  max-width: 920px;
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
  margin-right: 40px;
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

/* Features */
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
  border-color: var(--st-teal);
  box-shadow: 0 12px 32px rgba(57, 197, 187, 0.16);
}
.feature-icon {
  width: 46px;
  height: 46px;
  border-radius: 12px;
  background: var(--vp-c-brand-soft);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  margin-bottom: 14px;
}
.feature-title {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 8px;
}
.feature-desc {
  font-size: 13.5px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
  margin: 0;
}

/* Steps */
.steps {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
}
@media (max-width: 700px) {
  .steps { grid-template-columns: 1fr; }
}
.step {
  padding: 8px 16px;
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

/* CTA */
.cta-links {
  margin-top: 26px;
  display: flex;
  justify-content: center;
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
