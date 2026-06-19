import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import AutoImport from 'unplugin-auto-import/vite'
import { readFileSync } from 'node:fs'

const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))

// https://vite.dev/config/
export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  build: {
    rolldownOptions: {
      output: {
        // Split the heavy Live2D-only deps (pixi + live2d runtime + howler) into
        // their own chunk so the editor's first load doesn't pay for them. They
        // are reached only via the lazy-loaded /live2d route.
        advancedChunks: {
          groups: [
            {
              name: 'live2d-vendor',
              test: /node_modules[/\\](pixi\.js|@pixi|@sekai-world|howler)/,
            },
          ],
        },
      },
    },
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
      '/api': {
        target: 'http://localhost:9800',
        changeOrigin: true,
      },
      '/health': {
        target: 'http://localhost:9800',
        changeOrigin: true,
      },
    },
  },
})
