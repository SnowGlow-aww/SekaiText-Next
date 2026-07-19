<script setup lang="ts">
import { onMounted, onUnmounted, ref, type Component } from 'vue'
import {
  BookMarked,
  BookOpenText,
  Bug,
  ChevronLeft,
  Download,
  FilePenLine,
  Puzzle,
  Settings,
  Store,
  UserRound,
  icons,
} from 'lucide-vue-next'
import { useSettingsStore } from '../../stores/settings'
import { useTeamStore } from '../../stores/team'
import { useGlossaryNotifyStore } from '../../stores/glossaryNotify'
import { usePluginRegistry } from '../../plugin-host/registry'

const COMPACT_BREAKPOINT = 1080
const settings = useSettingsStore()
const team = useTeamStore()
const glossaryNotify = useGlossaryNotifyStore()
const pluginRegistry = usePluginRegistry()

const sidebarOpen = ref(typeof window === 'undefined' || window.innerWidth >= COMPACT_BREAKPOINT)
let wasNarrow = typeof window !== 'undefined' && window.innerWidth < COMPACT_BREAKPOINT

function handleResize() {
  const narrow = window.innerWidth < COMPACT_BREAKPOINT
  if (narrow && !wasNarrow) sidebarOpen.value = false
  wasNarrow = narrow
}

// PluginSidebarItem.icon is part of the public plugin contract: plugins pass a
// lucide-vue-next export name and the host resolves it. Keep the complete icon
// namespace here instead of a hand-maintained allow-list, otherwise every new
// valid plugin icon silently turns into Puzzle until the host is rebuilt.
const pluginIcons = icons as unknown as Record<string, Component>

function pluginIcon(name: string) {
  const icon = pluginIcons[name]
  return icon && (typeof icon === 'object' || typeof icon === 'function') ? icon : Puzzle
}

onMounted(() => {
  window.addEventListener('resize', handleResize)
  // The app shell must still mount when the local backend is starting or
  // temporarily unavailable. Start the notification loop either way, but
  // consume the status failure so it cannot surface as unhandledrejection.
  void team.refreshStatus().catch(() => {}).then(() => glossaryNotify.start())
})
onUnmounted(() => window.removeEventListener('resize', handleResize))
</script>

<template>
  <div class="app-shell page-bg" :class="{ 'is-compact': !sidebarOpen }">
    <aside class="app-shell-sidebar">
      <div class="app-shell-brand">
        <span class="app-shell-brand-mark" aria-hidden="true" />
        <div class="app-shell-brand-copy min-w-0">
          <div class="app-shell-brand-name">SekaiText</div>
          <div class="app-shell-brand-sub">セカイテキスト NEXT</div>
        </div>
        <button
          class="app-shell-collapse"
          :title="sidebarOpen ? '收起侧栏' : '展开侧栏'"
          :aria-label="sidebarOpen ? '收起侧栏' : '展开侧栏'"
          @click="sidebarOpen = !sidebarOpen"
        >
          <ChevronLeft :size="15" />
        </button>
      </div>

      <div class="app-shell-kicker">WORKSPACE</div>
      <nav class="app-shell-nav" aria-label="工作区导航">
        <router-link to="/" exact-active-class="is-route-active" class="app-shell-nav-item" title="编辑器">
          <FilePenLine class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">编辑器</span>
        </router-link>
        <router-link to="/download" exact-active-class="is-route-active" class="app-shell-nav-item" title="剧情下载">
          <Download class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">剧情下载</span>
        </router-link>
        <router-link
          to="/glossary"
          exact-active-class="is-route-active"
          class="app-shell-nav-item"
          data-tour="nav-glossary"
          :class="{ 'notify-breathe': glossaryNotify.active }"
          :title="glossaryNotify.tooltip || '术语库'"
        >
          <BookMarked class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">术语库</span>
        </router-link>
        <router-link to="/grammar" exact-active-class="is-route-active" class="app-shell-nav-item" title="语法用例">
          <BookOpenText class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">语法用例</span>
        </router-link>
        <router-link to="/market" exact-active-class="is-route-active" class="app-shell-nav-item" data-tour="nav-market" title="插件市场">
          <Store class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">插件市场</span>
        </router-link>
        <router-link
          v-for="item in pluginRegistry.sidebarItems"
          :key="`${item.pluginId}:${item.id}`"
          :to="item.to"
          exact-active-class="is-route-active"
          class="app-shell-nav-item"
          :title="item.label"
        >
          <component :is="pluginIcon(item.icon)" class="app-shell-nav-icon" :size="17" />
          <span class="app-shell-nav-label">{{ item.label }}</span>
        </router-link>
        <router-link v-if="settings.settings.debugEnabled" to="/debug" exact-active-class="is-route-active" class="app-shell-nav-item" title="调试日志">
          <Bug class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">调试日志</span>
        </router-link>
      </nav>

      <nav class="app-shell-nav app-shell-nav-bottom" aria-label="账户与设置">
        <router-link to="/account" exact-active-class="is-route-active" class="app-shell-nav-item" title="账号中心">
          <UserRound class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">账号中心</span>
        </router-link>
        <router-link to="/settings" exact-active-class="is-route-active" class="app-shell-nav-item" data-tour="nav-settings" title="设置">
          <Settings class="app-shell-nav-icon" :size="17" /><span class="app-shell-nav-label">设置</span>
        </router-link>
      </nav>
    </aside>

    <div class="app-shell-content">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.app-shell {
  --shell-sidebar-width: 11.5rem;
  display: grid;
  grid-template-columns: var(--shell-sidebar-width) minmax(0, 1fr);
  width: 100vw;
  height: 100vh;
  overflow: hidden;
  transition: grid-template-columns 220ms var(--ease-out);
  animation: shell-enter 280ms var(--ease-out) both;
}
.app-shell.is-compact { --shell-sidebar-width: 3.75rem; }
@keyframes shell-enter {
  from { opacity: 0; translate: 0 0.35rem; }
  to { opacity: 1; translate: 0 0; }
}
.app-shell-sidebar {
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 0 0.75rem 0.875rem;
  overflow: hidden;
  background: color-mix(in oklch, var(--color-base-200) 94%, var(--color-base-100));
  border-right: 1px solid var(--color-border);
  transition: padding 220ms var(--ease-out), background-color var(--dur) var(--ease-out);
}
.app-shell-brand {
  position: relative;
  display: flex;
  align-items: center;
  gap: 0.625rem;
  height: 4.25rem;
  padding: 0 0.5rem;
  border-bottom: 1px solid var(--color-border);
  white-space: nowrap;
}
.app-shell-brand-mark {
  width: 0.55rem;
  height: 0.55rem;
  flex: 0 0 0.55rem;
  rotate: 45deg;
  border-radius: 2px;
  background: var(--accent, var(--color-primary));
  box-shadow: 0 0 0 3px color-mix(in oklch, var(--accent, var(--color-primary)) 9%, transparent);
}
.app-shell-brand-name { font-size: 1rem; font-weight: 800; line-height: 1; }
.app-shell-brand-sub { margin-top: 0.2rem; color: var(--color-text-tertiary); font-size: 0.48rem; letter-spacing: 0.15em; }
.app-shell-brand-copy,
.app-shell-nav-label {
  max-width: 8rem;
  opacity: 1;
  transform: translateX(0);
  transition: opacity 130ms var(--ease-out), max-width 220ms var(--ease-out), transform 220ms var(--ease-out);
}
.app-shell-collapse {
  display: grid;
  place-items: center;
  width: 1.75rem;
  height: 1.75rem;
  margin-left: auto;
  border: 0;
  border-radius: 0.45rem;
  color: var(--color-text-tertiary);
  background: transparent;
  transition: color var(--dur-fast), background-color var(--dur-fast), scale 80ms;
}
.app-shell-collapse:hover { color: var(--color-text); background: color-mix(in oklch, var(--color-base-content) 5%, transparent); }
.app-shell-collapse:active { scale: 0.96; }
.app-shell-collapse svg { transition: transform 220ms var(--ease-out); }
.is-compact .app-shell-collapse svg { transform: rotate(180deg); }
.is-compact .app-shell-brand { justify-content: center; padding: 0; }
.is-compact .app-shell-brand-copy,
.is-compact .app-shell-nav-label {
  max-width: 0;
  opacity: 0;
  transform: translateX(-0.3rem);
  overflow: hidden;
  pointer-events: none;
}
.is-compact .app-shell-collapse { position: absolute; right: -0.1rem; bottom: 0.25rem; width: 1.35rem; height: 1.35rem; }
.app-shell-kicker {
  height: 0.75rem;
  margin: 1.2rem 0.625rem 0.45rem;
  color: var(--color-text-tertiary);
  font-size: 0.55rem;
  letter-spacing: 0.16em;
  transition: height 180ms var(--ease-out), margin 220ms var(--ease-out), opacity 100ms ease;
}
.is-compact .app-shell-kicker { height: 0; margin: 0; opacity: 0; overflow: hidden; }
.app-shell-nav { display: flex; flex-direction: column; gap: 0.125rem; margin-top: 0.75rem; }
.app-shell-kicker + .app-shell-nav {
  flex: 1 1 auto;
  min-height: 0;
  margin-top: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.app-shell-nav-bottom {
  flex: 0 0 auto;
  margin-top: 0;
  padding-top: 0.625rem;
  border-top: 1px solid var(--color-border);
}
.app-shell-nav-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: 0.7rem;
  width: 100%;
  height: 2.45rem;
  padding: 0 0.625rem;
  overflow: hidden;
  border-radius: 0.45rem;
  color: var(--color-text-secondary);
  font-size: 0.76rem;
  white-space: nowrap;
  transition: color 150ms, background-color 150ms, transform 160ms var(--ease-out), scale 80ms;
}
.app-shell-nav-item:hover {
  color: var(--color-text);
  background: color-mix(in oklch, var(--color-base-content) 3.5%, transparent);
  transform: translateX(0.12rem);
}
.app-shell-nav-item:active { scale: 0.985; }
.app-shell-nav-item.is-route-active,
.app-shell-nav-item[aria-current='page'] {
  color: var(--color-text);
  background: color-mix(in oklch, var(--accent, var(--color-primary)) 7%, transparent);
  font-weight: 700;
}
.app-shell-nav-item.is-route-active::before,
.app-shell-nav-item[aria-current='page']::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0.65rem;
  bottom: 0.65rem;
  width: 2px;
  border-radius: 2px;
  background: var(--accent, var(--color-primary));
  animation: nav-marker-in 200ms var(--ease-out) both;
}
.app-shell-nav-icon {
  width: 1.15rem;
  height: 1.15rem;
  flex: 0 0 1.15rem;
  color: var(--color-text-tertiary);
  stroke-width: 1.8;
  transition: color 150ms ease, transform 180ms var(--ease-out);
}
.app-shell-nav-item:hover .app-shell-nav-icon { transform: scale(1.08); }
.app-shell-nav-item.is-route-active .app-shell-nav-icon,
.app-shell-nav-item[aria-current='page'] .app-shell-nav-icon {
  color: var(--accent, var(--color-primary));
  transform: scale(1.06);
  stroke-width: 2.15;
}
.is-compact .app-shell-nav-item { justify-content: center; padding: 0; }
.is-compact .app-shell-nav-item:hover { transform: translateY(-1px); }
.app-shell-content {
  min-width: 0;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
}
@keyframes nav-marker-in {
  from { opacity: 0; transform: scaleY(0.35); }
  to { opacity: 1; transform: scaleY(1); }
}
@keyframes notify-breathe {
  0%, 100% { color: var(--color-text-secondary); }
  50% { color: var(--accent, var(--color-primary)); }
}
.notify-breathe { animation: notify-breathe 2.2s ease-in-out infinite; }
@media (prefers-reduced-motion: reduce) {
  .app-shell { animation: none; transition: none; }
  .app-shell-sidebar,
  .app-shell-brand-copy,
  .app-shell-nav-label,
  .app-shell-kicker,
  .app-shell-collapse svg,
  .app-shell-nav-item,
  .app-shell-nav-icon { transition: none; }
  .app-shell-nav-item.is-route-active::before,
  .app-shell-nav-item[aria-current='page']::before { animation: none; }
  .notify-breathe { animation: none; color: var(--accent, var(--color-primary)); }
}
</style>
