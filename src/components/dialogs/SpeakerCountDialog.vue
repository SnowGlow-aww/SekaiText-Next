<script setup lang="ts">
import { ref, onMounted } from 'vue'
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
  <div class="modal modal-open" @click.self="emit('close')">
    <div class="modal-box max-w-md">
      <h2 class="text-lg font-semibold mb-4">说话人统计</h2>

      <div v-if="loading" class="text-center py-8 text-sm opacity-60">加载中...</div>

      <div v-else class="max-h-80 overflow-y-auto">
        <table class="table table-sm">
          <thead>
            <tr>
              <th class="text-left font-medium">说话人</th>
              <th class="text-right font-medium">台词数</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in speakers" :key="s.japanese">
              <td>{{ s.japanese }}</td>
              <td class="text-right">{{ s.count }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="modal-action">
        <button
          class="btn btn-ghost btn-sm"
          @click="emit('close')"
        >
          关闭
        </button>
      </div>
    </div>
  </div>
</template>
