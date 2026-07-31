import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import { capabilities } from '../platform/capabilities'

const sharedRoutes: RouteRecordRaw[] = [
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
    path: '/account',
    name: 'account',
    component: () => import('../pages/AccountPage.vue'),
  },
]

const desktopAndWebRoutes: RouteRecordRaw[] = [
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
    path: '/market',
    name: 'market',
    component: () => import('../pages/MarketPage.vue'),
  },
]

const androidRemovedRouteRedirects: RouteRecordRaw[] = [
  { path: '/debug', redirect: '/' },
  { path: '/download', redirect: '/' },
  { path: '/market', redirect: '/' },
]

export const hostRoutes = capabilities.supportsPlugins
  ? [...sharedRoutes, ...desktopAndWebRoutes]
  : [...sharedRoutes, ...androidRemovedRouteRedirects]

const router = createRouter({
  history: createWebHashHistory(),
  routes: hostRoutes,
})

export default router
