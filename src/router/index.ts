import { createRouter, createWebHashHistory } from 'vue-router'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      name: 'editor',
      component: () => import('../pages/EditorPage.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('../pages/SettingsPage.vue'),
    },
    {
      path: '/debug',
      name: 'debug',
      component: () => import('../pages/DebugPage.vue'),
    },
    {
      path: '/download',
      name: 'download',
      component: () => import('../pages/JsonDownloadPage.vue'),
    },
    {
      path: '/glossary',
      name: 'glossary',
      component: () => import('../pages/GlossaryPage.vue'),
    },
    {
      path: '/grammar',
      name: 'grammar',
      component: () => import('../pages/GrammarPage.vue'),
    },
    {
      path: '/market',
      name: 'market',
      component: () => import('../pages/MarketPage.vue'),
    },
    {
      path: '/account',
      name: 'account',
      component: () => import('../pages/AccountPage.vue'),
    },
  ],
})

export default router
