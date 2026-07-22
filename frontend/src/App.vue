<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { RouterView } from 'vue-router'

import { Toaster } from '@/components/ui/sonner'
import { useAppStore, useBrowserStore, useCodexStore, useTerminalStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const terminalStore = useTerminalStore()
const browserStore = useBrowserStore()

onMounted(() => {
  codexStore.bootstrapEvents()
  void appStore.bootstrap().then(() => {
    if (appStore.workspace) workspaceStore.hydrateWorkspace(appStore.workspace)
    // Seed Codex model catalog from config.toml even before app-server is ready.
    void codexStore.loadModels()
    void codexStore.loadModelProviders()
    if (appStore.settings.workspace && appStore.settings.autoConnect && appStore.codexAvailable) {
      void codexStore.connect(appStore.settings.workspace)
    }
  })
  window.addEventListener('keydown', onGlobalKeydown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
  codexStore.dispose()
})

function onGlobalKeydown(event: KeyboardEvent): void {
  if (!(event.ctrlKey || event.metaKey) || event.altKey) return
  if (event.target instanceof HTMLElement && (event.target.isContentEditable || event.target.matches('input, textarea, [role="textbox"]'))) return
  const key = event.key.toLowerCase()
  if (event.shiftKey && key === 'b') {
    event.preventDefault()
    browserStore.openBrowser('')
  } else if (!event.shiftKey && event.code === 'Backquote' && workspaceStore.workspace) {
    event.preventDefault()
    void terminalStore.openTerminal()
  } else if (!event.shiftKey && key === 'n' && codexStore.isReady) {
    event.preventDefault()
    void codexStore.newThread()
  }
}
</script>

<template>
  <RouterView v-slot="{ Component }">
    <KeepAlive include="WorkbenchView" :max="1">
      <component :is="Component" />
    </KeepAlive>
  </RouterView>
  <Toaster position="bottom-right" :rich-colors="true" />
</template>
