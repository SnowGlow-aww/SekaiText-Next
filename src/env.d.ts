declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

declare const __APP_VERSION__: string

declare interface Window {
  __SEKAI_ORIGIN__?: string
  __SEKAI_PLATFORM__?: 'android' | 'desktop' | 'web'
  __TAURI_INTERNALS__?: unknown
}
