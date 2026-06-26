<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { AlertTriangle, HelpCircle } from 'lucide-vue-next'
import { confirmState, confirmEnabled, resolveDialog } from '../../composables/useConfirm'

const inputEl = ref<HTMLInputElement | null>(null)
const canConfirm = computed(() => confirmEnabled())

function onConfirm() {
  if (!canConfirm.value) return
  resolveDialog(confirmState.mode === 'prompt' ? confirmState.input : true)
}
function onCancel() {
  resolveDialog(confirmState.mode === 'prompt' ? null : false)
}

// Focus the input (prompt) or the confirm button when the dialog opens.
watch(
  () => confirmState.open,
  async (open) => {
    if (open && confirmState.mode === 'prompt') {
      await nextTick()
      inputEl.value?.focus()
      inputEl.value?.select()
    }
  },
)
</script>

<template>
  <Transition name="confirm-fade">
    <div
      v-if="confirmState.open"
      class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]"
      @keydown.esc="onCancel"
    >
      <!-- scrim -->
      <div
        class="absolute inset-0 bg-black/45 backdrop-blur-[2px]"
        @click="onCancel"
      />
      <!-- panel -->
      <div
        class="app-card relative w-full max-w-sm p-5"
        style="box-shadow: var(--shadow-lg)"
        @keydown.enter="onConfirm"
      >
        <div class="flex items-start gap-3">
          <div
            class="grid place-items-center w-9 h-9 rounded-full shrink-0"
            :class="confirmState.tone === 'danger' ? 'bg-error/15 text-error' : 'bg-primary/15 text-primary'"
          >
            <AlertTriangle v-if="confirmState.tone === 'danger'" :size="18" />
            <HelpCircle v-else :size="18" />
          </div>
          <div class="min-w-0 flex-1">
            <h3 v-if="confirmState.title" class="section-title mb-1">{{ confirmState.title }}</h3>
            <p class="text-sm text-[var(--color-text)] leading-relaxed whitespace-pre-line">{{ confirmState.message }}</p>
            <p v-if="confirmState.detail" class="app-help mt-1.5 whitespace-pre-line">{{ confirmState.detail }}</p>

            <input
              v-if="confirmState.mode === 'prompt'"
              ref="inputEl"
              v-model="confirmState.input"
              :type="confirmState.password ? 'password' : 'text'"
              :placeholder="confirmState.placeholder"
              class="app-input mt-3"
            />
          </div>
        </div>

        <div class="flex justify-end gap-2 mt-5">
          <button class="btn btn-ghost btn-sm" @click="onCancel">{{ confirmState.cancelText }}</button>
          <button
            class="btn btn-sm"
            :class="confirmState.tone === 'danger' ? 'btn-error' : 'btn-brand'"
            :disabled="!canConfirm"
            @click="onConfirm"
          >
            {{ confirmState.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.confirm-fade-enter-active,
.confirm-fade-leave-active {
  transition: opacity var(--dur) var(--ease-out);
}
.confirm-fade-enter-from,
.confirm-fade-leave-to {
  opacity: 0;
}
.confirm-fade-enter-active .app-card,
.confirm-fade-leave-active .app-card {
  transition: transform var(--dur) var(--ease-out);
}
.confirm-fade-enter-from .app-card {
  transform: translateY(8px) scale(0.97);
}
</style>
