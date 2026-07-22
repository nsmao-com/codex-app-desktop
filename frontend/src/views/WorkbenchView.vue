<script setup lang="ts">
import { onMounted, onUnmounted, shallowRef } from 'vue'

defineOptions({ name: 'WorkbenchView' })

import AppSidebar from '@/components/AppSidebar.vue'
import AppTopbar from '@/components/AppTopbar.vue'
import BrowserLauncher from '@/components/BrowserLauncher.vue'
import ChatWorkspace from '@/components/ChatWorkspace.vue'
import ConnectionBanner from '@/components/ConnectionBanner.vue'
import InspectorPanel from '@/components/InspectorPanel.vue'
import LiveDiffPanel from '@/components/LiveDiffPanel.vue'
import TerminalPanel from '@/components/TerminalPanel.vue'
import { useTerminalStore, useWorkspaceStore } from '@/stores'

const terminalStore = useTerminalStore()
const workspaceStore = useWorkspaceStore()

const sidebarCollapsed = shallowRef(window.innerWidth < 768)
const isMobile = shallowRef(window.innerWidth < 768)
const inspectorCollapsed = shallowRef(true)
const browserLauncherOpen = shallowRef(false)

function syncResponsiveLayout(): void {
  isMobile.value = window.innerWidth < 768
  if (isMobile.value) {
    sidebarCollapsed.value = true
    inspectorCollapsed.value = true
  }
}

function toggleSidebar(): void {
  sidebarCollapsed.value = !sidebarCollapsed.value
}

onMounted(() => window.addEventListener('resize', syncResponsiveLayout, { passive: true }))
onUnmounted(() => window.removeEventListener('resize', syncResponsiveLayout))

</script>

<template>
  <div class="flex h-screen w-screen overflow-hidden bg-background text-foreground">
    <AppSidebar
      :collapsed="sidebarCollapsed"
      @toggle-sidebar="toggleSidebar"
    />

    <main class="flex min-w-0 flex-1 flex-col">
      <AppTopbar
        :inspector-collapsed="inspectorCollapsed"
        @toggle-sidebar="toggleSidebar"
        @toggle-inspector="inspectorCollapsed = !inspectorCollapsed"
        @open-terminal="terminalStore.openTerminal"
        @open-browser="browserLauncherOpen = true"
      />
      <div class="relative flex min-h-0 flex-1 bg-panel">
        <div class="flex min-w-0 flex-1 flex-col">
          <ChatWorkspace @show-inspector="inspectorCollapsed = false" />
          <ConnectionBanner />
        </div>
        <InspectorPanel
          v-if="!inspectorCollapsed"
          @collapse="inspectorCollapsed = true"
        />
        <LiveDiffPanel
          v-if="workspaceStore.diffSidebarOpen"
          @collapse="workspaceStore.closeDiffSidebar()"
        />
      </div>
    </main>

    <button
      v-if="!sidebarCollapsed && isMobile"
      type="button"
      class="fixed inset-y-0 left-[292px] right-0 z-30 bg-black/20 backdrop-blur-[1px]"
      aria-label="Close sidebar overlay"
      @click="sidebarCollapsed = true"
    />

    <TerminalPanel />
    <BrowserLauncher :open="browserLauncherOpen" @close="browserLauncherOpen = false" />
  </div>
</template>
