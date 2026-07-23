<script setup lang="ts">
import { MotionConfig } from 'motion-v'
import { onMounted, onUnmounted, shallowRef } from 'vue'
import { RouterView } from 'vue-router'

import CommandPalette from '@/components/CommandPalette.vue'
import TitleBar from '@/components/TitleBar.vue'
import UpdateCheckDialog from '@/components/UpdateCheckDialog.vue'
import { Toaster } from '@/components/ui/sonner'
import { useNavigationHistory } from '@/composables/useNavigationHistory'
import { useAppStore, useBrowserStore, useCodexStore, useTerminalStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const terminalStore = useTerminalStore()
const browserStore = useBrowserStore()
const commandPaletteOpen = shallowRef(false)

useNavigationHistory()

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
  const key = event.key.toLowerCase()
  if (!event.shiftKey && key === 'k') {
    event.preventDefault()
    commandPaletteOpen.value = !commandPaletteOpen.value
    return
  }
  if (event.target instanceof HTMLElement && (event.target.isContentEditable || event.target.matches('input, textarea, [role="textbox"]'))) return
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
  <MotionConfig :reducedMotion="'user'">
    <div class="app-shell flex h-screen w-screen flex-col overflow-hidden text-foreground">
      <TitleBar />
      <div class="relative min-h-0 flex-1 overflow-hidden">
        <RouterView v-slot="{ Component, route }">
          <Transition :name="route.name === 'workbench' ? 'route-fade' : 'route-slide'" mode="out-in">
            <KeepAlive include="WorkbenchView" :max="1">
              <component :is="Component" :key="String(route.name || route.path)" />
            </KeepAlive>
          </Transition>
        </RouterView>
      </div>
      <Toaster position="bottom-right" :rich-colors="true" />
      <UpdateCheckDialog />
      <CommandPalette v-model:open="commandPaletteOpen" />
    </div>
  </MotionConfig>
</template>
