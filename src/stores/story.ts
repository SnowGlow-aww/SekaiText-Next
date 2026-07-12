import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { SourceTalk } from '../types/translation'
import type { DocMeta } from './editor'

export const useStoryStore = defineStore('story', () => {
  const storyTypes = ref<string[]>([])
  const sorts = ref<{ label: string; value: string }[]>([])
  const indices = ref<{ label: string; value: string; chapters?: number[] }[]>([])
  const chapters = ref<{ number: number; label: string }[]>([])

  const selectedType = ref('')
  const selectedSort = ref('')
  const selectedIndex = ref('')
  const selectedIndexLabel = ref('')
  const selectedChapter = ref(-1)
  const selectedSource = ref('haruki')

  const scenarioId = ref('')
  const sourceTalks = ref<SourceTalk[]>([])
  const saveTitle = ref('')
  const chapterTitle = ref('')
  const loading = ref(false)

  // Per-list request sequence guards. Rapid selection changes fire these fetches
  // concurrently (the navigator watchers don't cancel in-flight calls), so we
  // stamp each call with a monotonically increasing token and only commit the
  // result if it's still the latest issued for that list. This makes the winner
  // the last request STARTED rather than the last one to resolve, so a slow
  // earlier response can't clobber a newer selection.
  let typesSeq = 0
  let sortsSeq = 0
  let indexSeq = 0
  let chaptersSeq = 0

  async function fetchTypes() {
    const seq = ++typesSeq
    const result = await api.storyTypes()
    if (seq !== typesSeq) return
    storyTypes.value = result
  }

  async function fetchSorts(type: string) {
    const seq = ++sortsSeq
    const result = await api.storySorts(type)
    if (seq !== sortsSeq) return
    sorts.value = result
  }

  async function fetchIndex(type: string, sort: string) {
    const seq = ++indexSeq
    const result = await api.storyIndex(type, sort)
    if (seq !== indexSeq) return
    indices.value = result
  }

  async function fetchChapters(type: string, sort: string, index: string) {
    const seq = ++chaptersSeq
    const result = await api.storyChapter(type, sort, index)
    if (seq !== chaptersSeq) return
    chapters.value = result
  }

  // In-flight guard: a double-click (or any re-entrant trigger) must not launch a
  // second load whose result races and overwrites the first, nor let the inner
  // finally flip `loading` back off while another load is still running.
  let loadInFlight = false

  async function loadStory() {
    if (loadInFlight) return
    loadInFlight = true
    loading.value = true
    try {
      const result = await api.storyLoad({
        storyType: selectedType.value,
        sort: selectedSort.value,
        index: selectedIndex.value,
        chapter: selectedChapter.value,
        source: selectedSource.value,
      })
      scenarioId.value = result.scenarioId
      sourceTalks.value = result.sourceTalks
      saveTitle.value = result.saveTitle || ''
      chapterTitle.value = result.chapterTitle || ''
    } finally {
      loading.value = false
      loadInFlight = false
    }
  }

  async function loadStoryLocal(content: string) {
    loading.value = true
    try {
      const result = await api.storyLoadLocal(content)
      scenarioId.value = result.scenarioId
      sourceTalks.value = result.sourceTalks
      saveTitle.value = ''
      chapterTitle.value = ''
      selectedType.value = ''
      selectedSort.value = ''
      selectedIndex.value = ''
      selectedChapter.value = -1
    } finally {
      loading.value = false
    }
  }

  // 当前选择/载入状态的一次性快照，供编辑器在载入内容时绑定文档身份
  // （editor.docMeta）。之后这里的全局状态再怎么变都不影响已载入的文档。
  function snapshotDocMeta(): DocMeta {
    return {
      saveTitle: saveTitle.value,
      chapterTitle: chapterTitle.value,
      type: selectedType.value,
      sort: selectedSort.value,
      index: selectedIndex.value,
      indexLabel: selectedIndexLabel.value,
      chapter: selectedChapter.value,
      source: selectedSource.value,
      scenarioId: scenarioId.value,
    }
  }

  return {
    storyTypes, sorts, indices, chapters,
    selectedType, selectedSort, selectedIndex, selectedIndexLabel, selectedChapter, selectedSource,
    scenarioId, sourceTalks, saveTitle, chapterTitle, loading,
    fetchTypes, fetchSorts, fetchIndex, fetchChapters, loadStory, loadStoryLocal, snapshotDocMeta,
  }
})
