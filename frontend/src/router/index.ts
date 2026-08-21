import { createRouter, createWebHashHistory } from 'vue-router'

import SettingsView from '@/views/SettingsView.vue'
import WorkbenchView from '@/views/WorkbenchView.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      name: 'workbench',
      component: WorkbenchView,
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsView,
    },
    {
      path: '/capabilities',
      name: 'capabilities',
      component: () => import('@/views/CapabilitiesView.vue'),
    },
  ],
})

export default router
