<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Users, Check } from 'lucide-vue-next'
import { api } from '../../api/client'
import { useEditorStore } from '../../stores/editor'

const editor = useEditorStore()
const speakers = ref<{ japanese: string; chinese: string }[]>([])
const loading = ref(false)

const emit = defineEmits<{
  close: []
  save: [speakers: { japanese: string; chinese: string }[]]
}>()

// Load character name dictionary and build lookup map
let nameMap: Map<string, string> = new Map()

async function loadCharDict() {
  try {
    const res = await fetch('/characterDict.json')
    const chars: { name_j: string; name_c: string }[] = await res.json()
    for (const c of chars) {
      nameMap.set(c.name_j, c.name_c)
    }
  } catch { /* dict load fail, leave names as-is */ }
}

// Translate a Japanese speaker name to Chinese using the dictionary.
// Handles compound names (split by ・) and variant suffixes (_LeoN, etc.).
function translateSpeaker(jp: string): string {
  if (!jp) return jp
  // Try exact match first
  if (nameMap.has(jp)) return nameMap.get(jp)!
  // Try split by ・ and translate parts
  if (jp.includes('・')) {
    return jp.split('・').map(p => nameMap.get(p) || p).join('・')
  }
  return jp
}

onMounted(async () => {
  await loadCharDict()
  if (editor.talks.length === 0) return
  loading.value = true
  try {
    const result = await api.speakerCount({
      talks: editor.talks,
      sourceTalks: editor.sourceTalks,
    })
    speakers.value = result.speakers.map(s => ({
      japanese: s.japanese,
      chinese: translateSpeaker(s.japanese),
    }))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <Transition name="dialog-fade" appear>
    <div
      class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]"
      @keydown.esc="emit('close')"
    >
      <!-- scrim -->
      <div class="absolute inset-0 bg-black/45 backdrop-blur-[2px]" @click="emit('close')" />

      <!-- panel -->
      <div class="app-card app-glass relative w-full max-w-lg p-5" style="box-shadow: var(--shadow-lg)">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><Users :size="15" /></span>
          <div class="section-title">批量修改说话人</div>
        </div>

        <div v-if="loading" class="flex items-center justify-center gap-2 py-10 text-sm text-[var(--color-text-secondary)]">
          <span class="loading loading-spinner loading-sm" /> 加载中…
        </div>

        <div v-else-if="!speakers.length" class="flex flex-col items-center gap-2 py-10 text-center text-[var(--color-text-tertiary)]">
          <Users :size="28" />
          <p class="text-sm">没有可修改的说话人</p>
        </div>

        <div v-else class="max-h-80 overflow-y-auto rounded-[var(--radius-control)] border border-[var(--color-border)]">
          <table class="table table-sm">
            <thead>
              <tr class="text-[var(--color-text-secondary)]">
                <th class="text-left app-label">日文/原翻译</th>
                <th class="text-left app-label">翻译</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(s, idx) in speakers" :key="idx" class="border-[var(--color-border)]">
                <td class="text-[var(--color-text-secondary)]">{{ s.japanese }}</td>
                <td>
                  <input v-model="s.chinese" class="app-input" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="flex justify-end gap-2 mt-5">
          <button class="btn btn-sm btn-ghost border border-[var(--color-border)]" @click="emit('close')">
            取消
          </button>
          <button class="btn btn-sm btn-brand" @click="emit('save', speakers)">
            <Check :size="15" /> 应用
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
