<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

declare const __APP_VERSION__: string

const CDN = 'https://sakimizuki.accr.cc'
const RELEASES = 'https://github.com/SnowGlow-aww/SekaiText-Next/releases'

withDefaults(defineProps<{
  compact?: boolean
  align?: 'start' | 'center'
}>(), {
  compact: false,
  align: 'center',
})

function cdnUrl(v: string, suffix: string) {
  return CDN + '/sekaitext-releases/v' + v + '/SekaiText.Next_' + v + '_' + suffix
}

function githubUrl(v: string, suffix: string) {
  return RELEASES + '/download/v' + v + '/SekaiText.Next_' + v + '_' + suffix
}

const version = ref(__APP_VERSION__)
const downloads = ref({
  // GitHub is the durable fallback. Hydration switches to CDN only after both
  // mirrored installers have been probed successfully.
  mac: githubUrl(__APP_VERSION__, 'aarch64.dmg'),
  win: githubUrl(__APP_VERSION__, 'x64-setup.exe'),
})
const os = ref<'mac' | 'win' | 'other'>('other')
const usingCdn = computed(() => downloads.value.mac.startsWith(CDN))

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
    const r = await fetch(CDN + '/sekaitext-plugins/app-release.json', { cache: 'no-store' })
    if (r.ok) {
      const j = await r.json()
      if (j?.version && !newer(version.value, j.version)) {
        const next = {
          mac: j.downloads?.['darwin-aarch64'] || cdnUrl(j.version, 'aarch64.dmg'),
          win: j.downloads?.['windows-amd64'] || cdnUrl(j.version, 'x64-setup.exe'),
        }
        const available = await Promise.all(
          Object.values(next).map(async (url) => (await fetch(url, { method: 'HEAD', cache: 'no-store' })).ok),
        )
        if (available.every(Boolean)) {
          version.value = j.version
          downloads.value = next
        }
      }
    }
  } catch {
    // CDN 暂不可用时继续使用构建期版本，下载入口不会消失。
  }
})

const buttons = computed(() => {
  const mac = {
    key: 'mac',
    label: '下载 macOS 版',
    sub: 'Apple 芯片 · macOS 12+',
    href: downloads.value.mac,
  }
  const win = {
    key: 'win',
    label: '下载 Windows 版',
    sub: '64 位 · Windows 10+',
    href: downloads.value.win,
  }
  return os.value === 'win' ? [win, mac] : [mac, win]
})
</script>

<template>
  <div :class="['dl', 'is-' + align, { 'is-compact': compact }]">
    <div class="dl-buttons">
      <!-- download 属性避免 VitePress 把同域安装包误当作站内路由。 -->
      <a
        v-for="b in buttons"
        :key="b.key"
        :href="b.href"
        :class="['dl-btn', { 'is-current': b.key === os }]"
        download
      >
        <span class="dl-icon-wrap">
          <svg class="dl-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" />
            <line x1="12" y1="15" x2="12" y2="3" />
          </svg>
        </span>
        <span class="dl-text">
          <span class="dl-os">
            {{ b.label }}
            <small v-if="b.key === os" class="dl-current">当前设备</small>
          </span>
          <span class="dl-sub">{{ b.sub }}</span>
        </span>
      </a>
    </div>
    <p class="dl-meta">
      最新版 <code class="dl-ver">v{{ version }}</code>
      <span class="dl-dot">·</span>
      {{ usingCdn ? '国内 CDN 直连' : 'GitHub 直连' }}
      <span class="dl-dot">·</span>
      <a :href="RELEASES" target="_blank" rel="noreferrer">更新日志与历史版本 ↗</a>
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

.dl.is-start {
  align-items: flex-start;
}

.dl-buttons {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 12px;
}

.is-start .dl-buttons {
  justify-content: flex-start;
}

.dl-btn {
  position: relative;
  overflow: hidden;
  display: flex;
  align-items: center;
  gap: 13px;
  min-width: 246px;
  padding: 13px 18px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 13px;
  background: var(--vp-c-bg);
  box-shadow: 0 8px 26px rgba(11, 18, 29, 0.07);
  color: var(--vp-c-text-1);
  text-decoration: none;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.dl-btn::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--st-gradient);
  opacity: 0.45;
}

.dl-btn.is-current {
  border-color: color-mix(in srgb, var(--st-teal) 40%, var(--vp-c-divider));
  background: color-mix(in srgb, var(--st-teal) 8%, var(--vp-c-bg));
  box-shadow: 0 10px 30px rgba(57, 197, 187, 0.13);
}

.dl-btn:hover {
  transform: translateY(-2px);
  border-color: color-mix(in srgb, var(--st-teal) 52%, var(--vp-c-divider));
  box-shadow: 0 14px 34px rgba(11, 18, 29, 0.12);
}

.dl-icon-wrap {
  display: grid;
  width: 38px;
  height: 38px;
  flex: none;
  place-items: center;
  border-radius: 10px;
  background: color-mix(in srgb, var(--st-teal) 11%, var(--vp-c-bg-soft));
  color: var(--vp-c-brand-1);
}

.dl-icon {
  width: 19px;
  height: 19px;
}

.dl-text {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  line-height: 1.25;
}

.dl-os {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 14px;
  font-weight: 700;
  white-space: nowrap;
}

.dl-current {
  padding: 2px 6px;
  border-radius: 999px;
  background: var(--vp-c-brand-soft);
  color: var(--vp-c-brand-1);
  font-size: 9px;
  font-weight: 700;
}

.dl-sub {
  margin-top: 2px;
  color: var(--vp-c-text-3);
  font-size: 11.5px;
}

.dl-meta {
  margin: 0;
  color: var(--vp-c-text-2);
  font-size: 13px;
  text-align: center;
}

.is-start .dl-meta {
  text-align: left;
}

.dl-meta a {
  color: var(--vp-c-brand-1);
  text-decoration: none;
}

.dl-meta a:hover {
  text-decoration: underline;
}

.dl-ver {
  padding: 1px 7px;
  border-radius: 6px;
  background: var(--vp-c-brand-soft);
  color: var(--vp-c-brand-1);
  font-weight: 650;
}

.dl-dot {
  margin: 0 6px;
  opacity: 0.45;
}

.is-compact .dl-btn {
  min-width: 220px;
  padding: 10px 14px;
}

.is-compact .dl-icon-wrap {
  width: 34px;
  height: 34px;
}

.is-compact .dl-meta {
  font-size: 12px;
}

@media (max-width: 900px) {
  .dl.is-start {
    align-items: center;
  }

  .is-start .dl-buttons {
    justify-content: center;
  }

  .is-start .dl-meta {
    text-align: center;
  }
}

@media (max-width: 560px) {
  .dl,
  .dl-buttons {
    width: 100%;
  }

  .dl-btn,
  .is-compact .dl-btn {
    width: 100%;
    min-width: 0;
  }

  .dl-meta {
    max-width: 310px;
    line-height: 1.75;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dl-btn {
    transition: none;
  }
}
</style>
