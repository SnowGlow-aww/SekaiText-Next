<script setup lang="ts">
// 插件一览：陈列当前市场里所有插件的功能方块（首页只保留主打卡，全量在这里）。
// 卡片沿用首页 feature-card 的视觉语言；整卡可点，跳到对应使用指南。
import { withBase } from 'vitepress'

const plugins: {
  icon: string; title: string; desc: string; accent: string; link: string
}[] = [
  {
    icon: '⚡',
    title: '自动打轴',
    desc: '内置 SekaiCoreEngine 识别录屏画面，自动对齐每句台词生成字幕时间轴，可逐行微调分句并与 Aegisub 双向同步。',
    accent: '#6fa8ef',
    link: '/guide/autotiming.html',
  },
  {
    icon: '🎬',
    title: '一键压制',
    desc: '集成 FFmpeg + libass，内置团队字幕样式，从翻译稿到成片视频一条龙（随自动轴机插件提供）。',
    accent: '#f5a623',
    link: '/guide/autotiming.html',
  },
  {
    icon: '🎭',
    title: 'Live2D 剧情播放器',
    desc: '内置播放器还原游戏演出——表情、动作、语音同步播放，准确把握人物情感；支持编辑器内嵌面板与独立窗口，可从任意台词一键跳转播放。',
    accent: '#ff69b4',
    link: '/guide/live2d.html',
  },
]
</script>

<template>
  <div class="plugins-wrap">
    <p class="plugins-hint">
      以下功能均通过应用内「插件市场」一键安装、自动更新，全程 CDN 加速；点击卡片查看对应使用指南。
    </p>
    <div class="plugin-grid">
      <a
        v-for="p in plugins"
        :key="p.title"
        :href="withBase(p.link)"
        class="plugin-card"
        :style="{ '--fc': p.accent }"
      >
        <div class="plugin-icon">{{ p.icon }}</div>
        <h3 class="plugin-title">
          {{ p.title }}<span class="plugin-tag">插件</span>
        </h3>
        <p class="plugin-desc">{{ p.desc }}</p>
        <span class="plugin-more">使用指南 →</span>
      </a>
    </div>
  </div>
</template>

<style scoped>
.plugins-wrap { margin-top: 8px; }
.plugins-hint {
  font-size: 14px;
  line-height: 1.8;
  color: var(--vp-c-text-2);
  margin: 0 0 20px;
}
/* flex + 居中：全部插件一排居中放；窄屏换行后每排也自动居中 */
.plugin-grid {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 20px;
  text-align: left;
}
.plugin-card { width: calc((100% - 40px) / 3); }
@media (max-width: 860px) {
  .plugin-card { width: calc((100% - 20px) / 2); }
}
@media (max-width: 560px) {
  .plugin-card { width: 100%; }
}
.plugin-card {
  display: block;
  background: var(--vp-c-bg-soft);
  border: 1px solid var(--vp-c-divider);
  border-radius: 16px;
  padding: 24px;
  text-decoration: none;
  color: inherit;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}
.plugin-card:hover {
  transform: translateY(-4px);
  border-color: color-mix(in srgb, var(--fc) 60%, transparent);
  box-shadow: 0 12px 32px color-mix(in srgb, var(--fc) 18%, transparent);
}
.plugin-icon {
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
.plugin-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 8px;
}
.plugin-tag {
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
.plugin-desc {
  font-size: 13.5px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
  margin: 0;
}
.plugin-more {
  display: inline-block;
  margin-top: 12px;
  font-size: 13px;
  font-weight: 500;
  color: var(--fc);
}
</style>
