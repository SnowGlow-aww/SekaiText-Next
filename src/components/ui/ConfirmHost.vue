<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { AlertTriangle, HelpCircle } from 'lucide-vue-next'
import { confirmState, confirmEnabled, resolveDialog } from '../../composables/useConfirm'

const inputEl = ref<HTMLInputElement | null>(null)
const panelEl = ref<HTMLElement | null>(null)
const canConfirm = computed(() => confirmEnabled())

// The element focused before the dialog opened, restored when it closes.
let lastFocused: HTMLElement | null = null

function onConfirm() {
  if (!canConfirm.value) return
  resolveDialog(confirmState.mode === 'prompt' ? confirmState.input : true)
}
function onCancel() {
  resolveDialog(confirmState.mode === 'prompt' ? null : false)
}

// Visible, focusable controls inside the panel (for the Tab focus trap).
function focusablesInPanel(): HTMLElement[] {
  const root = panelEl.value
  if (!root) return []
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((el) => el.offsetParent !== null)
}

// Keyboard is handled at the window level so it works regardless of which
// element holds focus. Binding on the dialog subtree alone fails in confirm
// mode: no field is focused there, so focus stays on the triggering background
// button (still in the DOM) and the dialog never receives the keydown.
function onWindowKeydown(e: KeyboardEvent) {
  if (!confirmState.open) return
  // Let the IME consume keys while composing (Enter/Esc commit or cancel it).
  if (e.isComposing) return
  if (e.key === 'Escape') {
    e.preventDefault()
    onCancel()
  } else if (e.key === 'Enter') {
    e.preventDefault()
    onConfirm()
  } else if (e.key === 'Tab') {
    // Basic focus trap: keep Tab cycling within the dialog panel.
    const items = focusablesInPanel()
    if (items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    const active = document.activeElement as HTMLElement | null
    const inPanel = !!(active && panelEl.value?.contains(active))
    if (e.shiftKey && (!inPanel || active === first)) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && (!inPanel || active === last)) {
      e.preventDefault()
      first.focus()
    }
  }
}

onMounted(() => window.addEventListener('keydown', onWindowKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onWindowKeydown))

// Focus the input (prompt) or the panel (confirm) when the dialog opens, and
// restore focus to whatever triggered it when it closes.
watch(
  () => confirmState.open,
  async (open) => {
    if (open) {
      lastFocused = document.activeElement as HTMLElement | null
      await nextTick()
      if (confirmState.mode === 'prompt') {
        inputEl.value?.focus()
        inputEl.value?.select()
      } else {
        panelEl.value?.focus()
      }
    } else {
      lastFocused?.focus()
      lastFocused = null
    }
  },
)
</script>

<template>
  <Transition name="confirm-fade">
    <div
      v-if="confirmState.open"
      class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]"
    >
      <!-- scrim -->
      <div
        class="absolute inset-0 bg-black/45 backdrop-blur-[2px]"
        @click="onCancel"
      />
      <!-- panel -->
      <div
        ref="panelEl"
        tabindex="-1"
        class="app-card app-glass relative w-full max-w-sm p-5 focus:outline-none"
        style="box-shadow: var(--shadow-lg)"
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
