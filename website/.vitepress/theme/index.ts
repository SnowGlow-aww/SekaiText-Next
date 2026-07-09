import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import HomePage from './components/HomePage.vue'
import DownloadButtons from './components/DownloadButtons.vue'
import PluginsShowcase from './components/PluginsShowcase.vue'
import './custom.css'

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.component('HomePage', HomePage)
    app.component('DownloadButtons', DownloadButtons)
    app.component('PluginsShowcase', PluginsShowcase)
  },
} satisfies Theme
