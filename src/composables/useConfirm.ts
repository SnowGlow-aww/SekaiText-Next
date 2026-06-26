import { reactive } from 'vue'

// Promise-based confirm / prompt to replace the browser's native confirm()/
// prompt() (which can't be themed and look out of place). A single ConfirmHost
// mounted at the app root renders this shared state; callers just await.
//
//   if (!(await confirmDialog('确定删除？'))) return
//   const name = await promptDialog({ message: '新名称', defaultValue: old })

export type DialogTone = 'primary' | 'danger'

export interface ConfirmOptions {
  title?: string
  message: string
  detail?: string
  confirmText?: string
  cancelText?: string
  tone?: DialogTone
}

export interface PromptOptions extends ConfirmOptions {
  placeholder?: string
  defaultValue?: string
  password?: boolean
  /** confirm stays disabled until the typed value === requireMatch (exact). */
  requireMatch?: string
  /** confirm stays disabled until input length >= minLength. */
  minLength?: number
}

interface DialogState {
  open: boolean
  mode: 'confirm' | 'prompt'
  title: string
  message: string
  detail: string
  confirmText: string
  cancelText: string
  tone: DialogTone
  placeholder: string
  password: boolean
  requireMatch: string | null
  minLength: number
  input: string
  resolve: ((v: boolean | string | null) => void) | null
}

export const confirmState = reactive<DialogState>({
  open: false,
  mode: 'confirm',
  title: '',
  message: '',
  detail: '',
  confirmText: '确定',
  cancelText: '取消',
  tone: 'primary',
  placeholder: '',
  password: false,
  requireMatch: null,
  minLength: 0,
  input: '',
  resolve: null,
})

function reset(partial: Partial<DialogState>) {
  // Resolve any dialog currently open (treat as cancel) before replacing it.
  if (confirmState.resolve) {
    const prev = confirmState.resolve
    confirmState.resolve = null
    prev(confirmState.mode === 'prompt' ? null : false)
  }
  Object.assign(confirmState, {
    open: true,
    title: '',
    message: '',
    detail: '',
    confirmText: '确定',
    cancelText: '取消',
    tone: 'primary',
    placeholder: '',
    password: false,
    requireMatch: null,
    minLength: 0,
    input: '',
    resolve: null,
    ...partial,
  })
}

export function confirmDialog(opts: ConfirmOptions | string): Promise<boolean> {
  const o: ConfirmOptions = typeof opts === 'string' ? { message: opts } : opts
  return new Promise<boolean>((resolve) => {
    reset({ mode: 'confirm', ...o, resolve: resolve as DialogState['resolve'] })
  })
}

export function promptDialog(opts: PromptOptions | string): Promise<string | null> {
  const o: PromptOptions = typeof opts === 'string' ? { message: opts } : opts
  return new Promise<string | null>((resolve) => {
    reset({
      mode: 'prompt',
      ...o,
      input: o.defaultValue ?? '',
      resolve: resolve as DialogState['resolve'],
    })
  })
}

// Whether the confirm button should be enabled for the current prompt input.
export function confirmEnabled(): boolean {
  if (confirmState.mode !== 'prompt') return true
  const v = confirmState.input
  if (confirmState.requireMatch != null) return v === confirmState.requireMatch
  if (confirmState.minLength > 0) return v.length >= confirmState.minLength
  return true
}

export function resolveDialog(value: boolean | string | null) {
  const r = confirmState.resolve
  confirmState.resolve = null
  confirmState.open = false
  if (r) r(value)
}

export function useConfirm() {
  return { confirm: confirmDialog, prompt: promptDialog }
}
