import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import AutoImport from 'unplugin-auto-import/vite'
import { readFileSync } from 'node:fs'

const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))

// https://vite.dev/config/
export default defineConfig(({ command }) => {
  const authToken = process.env.SEKAI_TEXT_AUTH_TOKEN
  if (command === 'serve' && !authToken) {
    throw new Error('Vite TCP development requires SEKAI_TEXT_AUTH_TOKEN; run npm run dev:tcp')
  }

  const proxy = {
    target: 'http://localhost:9800',
    changeOrigin: true,
    headers: authToken ? { 'X-Sekai-Token': authToken } : undefined,
  }

  return {
    define: {
      __APP_VERSION__: JSON.stringify(pkg.version),
    },
    plugins: [
      vue(),
      tailwindcss(),
      AutoImport({
        imports: ['vue', 'vue-router', '@vueuse/core', 'pinia'],
        dts: 'src/auto-imports.d.ts',
      }),
    ],
    server: {
      port: 5173,
      proxy: {
        '/api': proxy,
        '/health': proxy,
      },
    },
  }
})
