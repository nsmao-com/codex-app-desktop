import { createRouter, createWebHashHistory } from 'vue-router'

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
      component: () => import('@/views/SettingsView.vue'),
    },
    {
      path: '/capabilities',
      name: 'capabilities',
      redirect: (to) => ({ name: 'settings', query: { section: 'capabilities', tab: to.query.tab || 'runtime' } }),
    },
  ],
})

export default router
