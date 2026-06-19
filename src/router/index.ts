import { createRouter, createWebHashHistory } from 'vue-router'
import EditorPage from '../pages/EditorPage.vue'
import SettingsPage from '../pages/SettingsPage.vue'
import DebugPage from '../pages/DebugPage.vue'
import JsonDownloadPage from '../pages/JsonDownloadPage.vue'
import Live2DPlayerPage from '../pages/Live2DPlayerPage.vue'

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
      path: '/live2d',
      name: 'live2d',
      component: Live2DPlayerPage,
    },
  ],
})

export default router
