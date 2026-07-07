<script setup lang="ts">
import { computed } from 'vue'
import { Download, RefreshCw, X, CheckCircle2, AlertTriangle, Sparkles } from 'lucide-vue-next'
import { useAppUpdateStore } from '../../stores/appUpdate'
import { useToast } from '../../composables/useToast'

const u = useAppUpdateStore()
const toast = useToast()

const mb = (n: number) => (n / 1048576).toFixed(1)
const sizeText = computed(() => (u.total > 0 ? `${mb(u.read)} / ${mb(u.total)} MB` : ''))

const headIcon = computed(() =>
  u.phase === 'error' ? AlertTriangle : u.phase === 'ready' ? CheckCircle2 : Sparkles,
)

async function onInstall() {
  const ok = await u.install()
  if (ok) {
    toast.show('安装器已打开，SekaiText 将自动退出，请在其中完成更新（macOS 拖入「应用程序」）', 'info', 6000)
  }
}
</script>

<template>
  <Transition name="update-banner">
    <div
      v-if="u.show"
      class="fixed top-3 left-1/2 -translate-x-1/2 z-[200] w-[min(92vw,560px)] rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-lg)] px-4 py-3"
    >
      <div class="flex items-start gap-3">
        <div
          class="grid place-items-center w-8 h-8 rounded-full shrink-0 bg-[color-mix(in_oklch,var(--accent)_15%,transparent)] text-[var(--accent)]"
        >
          <component :is="headIcon" :size="18" />
        </div>
        <div class="min-w-0 flex-1">
          <template v-if="u.phase === 'available'">
            <div class="text-sm font-semibold text-[var(--color-text)]">
              发现新版本 v{{ u.info?.latest }}
              <span class="app-help font-normal">（当前 v{{ u.info?.current }}）</span>
            </div>
            <div v-if="u.info?.notes" class="app-help mt-0.5 line-clamp-3 whitespace-pre-line">{{ u.info.notes }}</div>
          </template>
          <template v-else-if="u.phase === 'downloading'">
            <div class="text-sm font-semibold text-[var(--color-text)]">正在下载更新…</div>
            <div class="mt-1.5 h-1.5 rounded-full overflow-hidden bg-[var(--color-bg)]">
              <div class="h-full rounded-full bg-[var(--accent)] transition-[width] duration-300" :style="{ width: Math.max(4, u.percent) + '%' }" />
            </div>
            <div class="app-help mt-1">{{ u.percent ? u.percent + '%' : '' }} {{ sizeText }}</div>
          </template>
          <template v-else-if="u.phase === 'ready'">
            <div class="text-sm font-semibold text-[var(--color-text)]">新版本已下载完成</div>
            <div class="app-help mt-0.5">点击「立即安装」打开安装器后，SekaiText 会自动退出以便替换，随后完成安装即可。</div>
          </template>
          <template v-else-if="u.phase === 'error'">
            <div class="text-sm font-semibold text-[var(--color-error,#e5484d)]">更新失败</div>
            <div class="app-help mt-0.5 line-clamp-2">{{ u.errorMsg }}</div>
          </template>
        </div>

        <button
          v-if="u.phase !== 'downloading'"
          class="grid place-items-center w-6 h-6 rounded-full shrink-0 text-[var(--color-text-tertiary)] hover:text-[var(--color-text)] hover:bg-[var(--color-border)] transition-colors"
          title="稍后"
          @click="u.dismiss()"
        >
          <X :size="14" />
        </button>
      </div>

      <div v-if="u.phase !== 'downloading'" class="flex justify-end gap-2 mt-2.5">
        <template v-if="u.phase === 'available'">
          <button class="btn btn-sm btn-ghost" @click="u.dismiss()">稍后</button>
          <button class="btn btn-sm btn-primary gap-1.5" @click="u.download()">
            <Download :size="14" /> 下载并安装
          </button>
        </template>
        <template v-else-if="u.phase === 'ready'">
          <button class="btn btn-sm btn-primary gap-1.5" @click="onInstall()">
            <CheckCircle2 :size="14" /> 立即安装
          </button>
        </template>
        <template v-else-if="u.phase === 'error'">
          <button class="btn btn-sm btn-ghost" @click="u.dismiss()">关闭</button>
          <button class="btn btn-sm btn-primary gap-1.5" @click="u.download()">
            <RefreshCw :size="14" /> 重试
          </button>
        </template>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.update-banner-enter-active,
.update-banner-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}
.update-banner-enter-from,
.update-banner-leave-to {
  opacity: 0;
  transform: translate(-50%, -12px);
}
</style>
