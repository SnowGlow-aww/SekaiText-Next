<script setup lang="ts">
import { ref, onMounted } from 'vue'
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
  <div class="modal modal-open" @click.self="emit('close')">
    <div class="modal-box max-w-lg">
      <h2 class="text-lg font-semibold mb-4">批量修改说话人</h2>

      <div v-if="loading" class="text-center py-8 text-sm opacity-60">加载中...</div>

      <div v-else class="max-h-80 overflow-y-auto">
        <table class="table table-sm">
          <thead>
            <tr>
              <th class="text-left font-medium">日文/原翻译</th>
              <th class="text-left font-medium">翻译</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(s, idx) in speakers" :key="idx">
              <td class="opacity-70">{{ s.japanese }}</td>
              <td>
                <input
                  v-model="s.chinese"
                  class="input input-bordered input-sm w-full"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="modal-action">
        <button
          class="btn btn-ghost btn-sm"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          class="btn btn-primary btn-sm"
          @click="emit('save', speakers)"
        >
          应用
        </button>
      </div>
    </div>
  </div>
</template>
