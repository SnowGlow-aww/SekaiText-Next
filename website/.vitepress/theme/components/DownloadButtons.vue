<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

const CDN = 'https://sakimizuki.accr.cc'
const RELEASES = 'https://github.com/SnowGlow-aww/SekaiText-Next/releases'

function cdnUrl(v: string, suffix: string) {
  return `${CDN}/sekaitext-releases/v${v}/SekaiText.Next_${v}_${suffix}`
}

// 构建期兜底（读主仓库 package.json），运行时再从 CDN manifest 刷新
const version = ref(__APP_VERSION__)
const downloads = ref({
  mac: cdnUrl(__APP_VERSION__, 'aarch64.dmg'),
  win: cdnUrl(__APP_VERSION__, 'x64-setup.exe'),
})
const os = ref<'mac' | 'win' | 'other'>('other')

// a 是否比 b 更新(简易 semver 比较)
function newer(a: string, b: string) {
  const pa = a.split('.').map(Number)
  const pb = b.split('.').map(Number)
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] || 0) - (pb[i] || 0)
    if (d) return d > 0
  }
  return false
}

onMounted(async () => {
  const ua = navigator.userAgent
  os.value = /Macintosh|Mac OS X/i.test(ua) ? 'mac' : /Windows/i.test(ua) ? 'win' : 'other'
  try {
    const r = await fetch(`${CDN}/sekaitext-plugins/app-release.json`, { cache: 'no-store' })
    if (r.ok) {
      const j = await r.json()
      // 边缘缓存可能滞后:manifest 比构建期版本还旧就忽略,
      // 否则会指向已被清理的历史版本安装包(404)
      if (j?.version && !newer(version.value, j.version)) {
        version.value = j.version
        downloads.value = {
          mac: j.downloads?.['darwin-aarch64'] || cdnUrl(j.version, 'aarch64.dmg'),
          win: j.downloads?.['windows-amd64'] || cdnUrl(j.version, 'x64-setup.exe'),
        }
      }
    }
  } catch {
    // CORS / 网络失败 → 静默使用构建期兜底链接
  }
})

const buttons = computed(() => {
  const mac = {
    key: 'mac',
    label: 'macOS 下载',
    sub: 'Apple Silicon · .dmg',
    href: downloads.value.mac,
  }
  const win = {
    key: 'win',
    label: 'Windows 下载',
    sub: 'x64 · 安装程序',
    href: downloads.value.win,
  }
  // 识别到的系统排前并高亮
  return os.value === 'win' ? [win, mac] : [mac, win]
})
</script>

<template>
  <div class="dl">
    <div class="dl-buttons">
      <!-- download 属性:①同域直接触发下载 ②VitePress 前端路由不拦截带 download 的链接
           (官网和安装包同域,普通点击否则会被当成站内路由导航→404 页;Cmd+点击不经过路由所以正常) -->
      <a v-for="b in buttons" :key="b.key" :href="b.href" class="dl-btn" download>
        <svg class="dl-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <polyline points="7 10 12 15 17 10" />
          <line x1="12" y1="15" x2="12" y2="3" />
        </svg>
        <span class="dl-text">
          <span class="dl-os">{{ b.label }}</span>
          <span class="dl-sub">{{ b.sub }}</span>
        </span>
      </a>
    </div>
    <p class="dl-meta">
      当前版本 <code class="dl-ver">v{{ version }}</code>
      <span class="dl-dot">·</span>
      <a :href="RELEASES" target="_blank" rel="noreferrer">全部版本与更新日志 ↗</a>
      <span class="dl-dot">·</span>
      国内 CDN 加速直链
    </p>
  </div>
</template>

<style scoped>
.dl {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
}
.dl-buttons {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 14px;
}
/* 两个平台平权：同款渐变、同宽，识别系统只决定排序 */
.dl-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  min-width: 250px;
  padding: 12px 26px;
  border-radius: 14px;
  text-decoration: none;
  background: var(--st-gradient);
  color: #fff;
  box-shadow: 0 8px 24px rgba(57, 197, 187, 0.35);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}
.dl-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 12px 32px rgba(255, 105, 180, 0.35);
}
.dl-icon {
  width: 22px;
  height: 22px;
  flex: none;
}
.dl-text {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  line-height: 1.25;
}
.dl-os {
  font-weight: 700;
  font-size: 15px;
}
.dl-sub {
  font-size: 12px;
  opacity: 0.75;
}
.dl-meta {
  font-size: 13px;
  color: var(--vp-c-text-2);
  text-align: center;
  margin: 0;
}
.dl-meta a {
  color: var(--vp-c-brand-1);
  text-decoration: none;
}
.dl-meta a:hover {
  text-decoration: underline;
}
.dl-ver {
  font-weight: 600;
  color: var(--vp-c-brand-1);
  background: var(--vp-c-brand-soft);
  border-radius: 6px;
  padding: 1px 7px;
}
.dl-dot {
  margin: 0 6px;
  opacity: 0.5;
}
</style>
