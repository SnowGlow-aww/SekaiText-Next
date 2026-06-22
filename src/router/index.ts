import { createRouter, createWebHashHistory } from 'vue-router'
import EditorPage from '../pages/EditorPage.vue'
import SettingsPage from '../pages/SettingsPage.vue'
import DebugPage from '../pages/DebugPage.vue'
import JsonDownloadPage from '../pages/JsonDownloadPage.vue'
import GlossaryPage from '../pages/GlossaryPage.vue'
import GrammarPage from '../pages/GrammarPage.vue'
import MarketPage from '../pages/MarketPage.vue'
import AccountPage from '../pages/AccountPage.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      name: 'editor',
      component: EditorPage,
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsPage,
    },
    {
      path: '/debug',
      name: 'debug',
      component: DebugPage,
    },
    {
      path: '/download',
      name: 'download',
      component: JsonDownloadPage,
    },
    {
      path: '/glossary',
      name: 'glossary',
      component: GlossaryPage,
    },
    {
      path: '/grammar',
      name: 'grammar',
      component: GrammarPage,
    },
    {
      path: '/market',
      name: 'market',
      component: MarketPage,
    },
    {
      path: '/account',
      name: 'account',
      component: AccountPage,
    },
  ],
})

export default router
