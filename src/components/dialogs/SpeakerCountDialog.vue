<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Users } from 'lucide-vue-next'
import { api } from '../../api/client'
import { useEditorStore } from '../../stores/editor'

const editor = useEditorStore()
const speakers = ref<{ japanese: string; chinese: string; count: number }[]>([])
const loading = ref(false)

const emit = defineEmits<{
  close: []
}>()

onMounted(async () => {
  if (editor.talks.length === 0) return
  loading.value = true
  try {
    const result = await api.speakerCount({
      talks: editor.talks,
      sourceTalks: editor.sourceTalks,
    })
    speakers.value = result.speakers
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <Transition appear name="dialog-fade">
    <div class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]">
      <!-- scrim -->
      <div class="absolute inset-0 bg-black/45 backdrop-blur-[2px]" @click="emit('close')" />

      <!-- panel -->
      <div class="app-card relative w-full max-w-md p-5" style="box-shadow: var(--shadow-lg)">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><Users :size="15" /></span>
          <div class="section-title">说话人统计</div>
        </div>

        <div v-if="loading" class="flex items-center justify-center gap-2 py-8 text-sm text-[var(--color-text-secondary)]">
          <span class="loading loading-spinner loading-sm" /> 加载中…
        </div>

        <div v-else class="max-h-80 overflow-y-auto rounded-[var(--radius-control)] border border-[var(--color-border)]">
          <table class="table table-sm">
            <thead>
              <tr>
                <th class="text-left font-medium text-[var(--color-text-secondary)]">说话人</th>
                <th class="text-right font-medium text-[var(--color-text-secondary)]">台词数</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in speakers" :key="s.japanese">
                <td>{{ s.japanese }}</td>
                <td class="text-right tabular-nums">{{ s.count }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="flex justify-end mt-5">
          <button
            class="btn btn-sm btn-ghost border border-[var(--color-border)]"
            @click="emit('close')"
          >
            关闭
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.dialog-fade-enter-active,
.dialog-fade-leave-active {
  transition: opacity var(--dur) var(--ease-out);
}
.dialog-fade-enter-from,
.dialog-fade-leave-to {
  opacity: 0;
}
.dialog-fade-enter-active .app-card,
.dialog-fade-leave-active .app-card {
  transition: transform var(--dur) var(--ease-out);
}
.dialog-fade-enter-from .app-card {
  transform: translateY(8px) scale(0.97);
}
</style>
