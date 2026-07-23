import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

export const useShellStore = defineStore('shell', () => {
  const sidebarCollapsed = shallowRef(typeof window !== 'undefined' ? window.innerWidth < 768 : false)

  function toggleSidebar(): void {
    sidebarCollapsed.value = !sidebarCollapsed.value
  }

  function setSidebarCollapsed(collapsed: boolean): void {
    sidebarCollapsed.value = collapsed
  }

  return {
    sidebarCollapsed,
    toggleSidebar,
    setSidebarCollapsed,
  }
})
