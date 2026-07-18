<script setup lang="ts">
import { withBase } from 'vitepress'

const plugins = [
  {
    number: '01',
    label: '轴机与压制',
    title: '自动轴机 + 压制',
    desc: '从录屏识别、长句分轴到 ASS 导出与成片压制，集中处理字幕制作里最耗时间的环节。',
    points: ['逐句时间轴', 'Aegisub 同步', 'FFmpeg 压制'],
    link: '/guide/autotiming.html',
    accent: '#6fa8ef',
  },
  {
    number: '02',
    label: '演出对照',
    title: 'Live2D 剧情播放器',
    desc: '在应用里还原角色动作、表情、语音与背景；翻到哪一句，就能从哪一句开始看。',
    points: ['逐句跳转', '编辑器联动', '独立窗口'],
    link: '/guide/live2d.html',
    accent: '#ff69b4',
  },
]
</script>

<template>
  <div class="plugins-wrap">
    <div class="plugins-intro">
      <p>
        插件都从应用内「插件市场」安装，更新也在应用内完成。
        主程序保持轻量，需要打轴、压制或演出对照时再启用对应能力。
      </p>
      <span>安装包与市场索引均走国内 CDN</span>
    </div>

    <div class="plugin-grid">
      <a
        v-for="plugin in plugins"
        :key="plugin.title"
        :href="withBase(plugin.link)"
        class="plugin-card"
        :style="{ '--plugin-accent': plugin.accent }"
      >
        <div class="plugin-meta">
          <span>{{ plugin.number }}</span>
          <span>{{ plugin.label }}</span>
        </div>
        <h2>{{ plugin.title }}</h2>
        <p>{{ plugin.desc }}</p>
        <ul>
          <li v-for="point in plugin.points" :key="point">{{ point }}</li>
        </ul>
        <span class="plugin-more">查看使用指南 <span aria-hidden="true">→</span></span>
      </a>
    </div>
  </div>
</template>

<style scoped>
.plugins-wrap {
  margin-top: 28px;
}

.plugins-intro {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 32px;
  margin-bottom: 34px;
  padding-bottom: 24px;
  border-bottom: 1px solid var(--vp-c-divider);
}

.plugins-intro p {
  max-width: 680px;
  margin: 0;
  color: var(--vp-c-text-2);
  line-height: 1.8;
}

.plugins-intro > span {
  color: var(--vp-c-text-3);
  font-size: 12px;
  white-space: nowrap;
}

.plugin-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  border-top: 1px solid var(--vp-c-divider);
  border-bottom: 1px solid var(--vp-c-divider);
}

.plugin-card {
  position: relative;
  display: flex;
  min-width: 0;
  flex-direction: column;
  padding: 32px 34px 36px;
  color: inherit;
  text-decoration: none;
}

.plugin-card + .plugin-card {
  border-left: 1px solid var(--vp-c-divider);
}

.plugin-card::before {
  content: '';
  position: absolute;
  top: -1px;
  left: 0;
  width: 0;
  height: 2px;
  background: var(--plugin-accent);
  transition: width 0.25s ease;
}

.plugin-card:hover {
  background: color-mix(in srgb, var(--plugin-accent) 4%, transparent);
}

.plugin-card:hover::before {
  width: 100%;
}

.plugin-meta {
  display: flex;
  justify-content: space-between;
  margin-bottom: 34px;
  color: var(--vp-c-text-3);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.1em;
}

.plugin-meta span:last-child {
  color: var(--plugin-accent);
}

.plugin-card h2 {
  margin: 0 0 14px;
  border: 0;
  padding: 0;
  color: var(--vp-c-text-1);
  font-size: 21px;
  letter-spacing: -0.02em;
}

.plugin-card > p {
  margin: 0 0 24px;
  color: var(--vp-c-text-2);
  font-size: 14px;
  line-height: 1.8;
}

.plugin-card ul {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  margin: auto 0 28px;
  padding: 0;
  color: var(--vp-c-text-3);
  font-size: 12px;
  list-style: none;
}

.plugin-card li {
  position: relative;
}

.plugin-card li:not(:last-child)::after {
  content: '·';
  position: absolute;
  right: -11px;
  color: var(--vp-c-divider);
}

.plugin-more {
  color: var(--plugin-accent);
  font-size: 13px;
  font-weight: 650;
}

@media (max-width: 680px) {
  .plugins-intro {
    grid-template-columns: 1fr;
    gap: 10px;
  }

  .plugins-intro > span {
    white-space: normal;
  }

  .plugin-grid {
    grid-template-columns: 1fr;
  }

  .plugin-card {
    padding: 28px 20px 32px;
  }

  .plugin-card + .plugin-card {
    border-top: 1px solid var(--vp-c-divider);
    border-left: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .plugin-card::before {
    transition: none;
  }
}
</style>
