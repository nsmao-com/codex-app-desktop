<script setup lang="ts">
import { AnimatePresence, Motion } from 'motion-v'
import { onMounted, onUnmounted, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

defineOptions({ name: 'WorkbenchView' })

import AppSidebar from '@/components/AppSidebar.vue'
import AppTopbar from '@/components/AppTopbar.vue'
import ArenaChatLayout from '@/components/ArenaChatLayout.vue'
import BrowserLauncher from '@/components/BrowserLauncher.vue'
import ChatWorkspace from '@/components/ChatWorkspace.vue'
import ConnectionBanner from '@/components/ConnectionBanner.vue'
import InspectorPanel from '@/components/InspectorPanel.vue'
import LiveDiffPanel from '@/components/LiveDiffPanel.vue'
import TerminalPanel from '@/components/TerminalPanel.vue'
import { overlayFade, springSoft } from '@/lib/motion'
import { useAppStore, useArenaStore, useBrowserStore, useClaudeStore, useCodexStore, useGrokStore, useShellStore, useTerminalStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'

const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const arenaStore = useArenaStore()
const terminalStore = useTerminalStore()
const workspaceStore = useWorkspaceStore()
const shellStore = useShellStore()
const browserStore = useBrowserStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
let arenaSessionOpenSequence = 0

async function focusArenaRuntime(runtime: string): Promise<void> {
  arenaSessionOpenSequence += 1
  const next = runtime as WorkspaceRuntime
  if (appStore.activeRuntime === next) return
  await appStore.setActiveRuntime(next)
}

async function openArenaPaneSession(payload: { runtime: string; sessionId: string }): Promise<void> {
  const sequence = ++arenaSessionOpenSequence
  const selectionRevision = arenaStore.sessionSelectionRevision
  const runtime = payload.runtime as WorkspaceRuntime
  const sessionId = payload.sessionId.trim()
  if (!sessionId) return
  const runtimeReady = appStore.activeRuntime === runtime
    ? await appStore.ensureActiveRuntimeSynced(runtime)
    : await appStore.setActiveRuntime(runtime)
  if (
    !runtimeReady
    || sequence !== arenaSessionOpenSequence
    || selectionRevision !== arenaStore.sessionSelectionRevision
  ) return
  if (runtime === 'grok') {
    if (grokStore.sameSession(grokStore.activeSessionId, sessionId)) return
    await grokStore.openSession(sessionId)
    return
  }
  if (runtime === 'claude') {
    if (claudeStore.sameSession(claudeStore.activeSessionId, sessionId)) return
    await claudeStore.openSession(sessionId)
    return
  }
  if (codexStore.sameThread(codexStore.activeThreadId, sessionId)) return
  await codexStore.openThread(sessionId)
}

const isMobile = shallowRef(window.innerWidth < 768)
const inspectorCollapsed = shallowRef(true)
const browserLauncherOpen = shallowRef(false)

function syncResponsiveLayout(): void {
  isMobile.value = window.innerWidth < 768
  if (isMobile.value) {
    shellStore.setSidebarCollapsed(true)
    inspectorCollapsed.value = true
  }
}

function consumeOpenBrowserQuery(): void {
  if (route.query.openBrowser !== '1') return
  browserLauncherOpen.value = true
  const nextQuery = { ...route.query }
  delete nextQuery.openBrowser
  void router.replace({ name: 'workbench', query: nextQuery })
}

onMounted(() => {
  window.addEventListener('resize', syncResponsiveLayout, { passive: true })
  consumeOpenBrowserQuery()
})
onUnmounted(() => window.removeEventListener('resize', syncResponsiveLayout))

watch(() => route.query.openBrowser, () => consumeOpenBrowserQuery())
</script>

<template>
  <Motion
    class="flex h-full w-full overflow-hidden bg-transparent text-foreground"
    :initial="{ opacity: 0 }"
    :animate="{ opacity: 1 }"
    :transition="{ duration: 0.2 }"
  >
    <AppSidebar
      :collapsed="shellStore.sidebarCollapsed"
      :mobile="isMobile"
      @toggle-sidebar="shellStore.toggleSidebar()"
    />

    <Motion
      class="flex min-h-0 min-w-0 flex-1 flex-col pb-2 pr-2 pl-1.5 pt-0"
      layout
      :transition="springSoft"
    >
      <Motion
        as="section"
        layout
        class="workbench-card relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-[14px] border bg-card"
        :transition="springSoft"
      >
        <AppTopbar
          :inspector-collapsed="inspectorCollapsed"
          @toggle-sidebar="shellStore.toggleSidebar()"
          @toggle-inspector="inspectorCollapsed = !inspectorCollapsed"
          @open-terminal="terminalStore.openTerminal"
          @open-browser="browserLauncherOpen = true"
        />
        <div class="relative flex min-h-0 flex-1 bg-card">
          <Motion
            class="flex min-w-0 flex-1 flex-col"
            layout
            :transition="springSoft"
          >
            <ArenaChatLayout
              v-if="arenaStore.isArenaMode"
              @show-inspector="inspectorCollapsed = false"
              @focus-runtime="(runtime) => void focusArenaRuntime(runtime)"
              @open-pane-session="(payload) => void openArenaPaneSession(payload)"
            />
            <ChatWorkspace
              v-else
              @show-inspector="inspectorCollapsed = false"
            />
            <ConnectionBanner />
          </Motion>

          <AnimatePresence>
            <InspectorPanel
              v-if="!inspectorCollapsed"
              key="inspector"
              @collapse="inspectorCollapsed = true"
            />
          </AnimatePresence>

          <AnimatePresence>
            <LiveDiffPanel
              v-if="workspaceStore.diffSidebarOpen"
              key="live-diff"
              @collapse="workspaceStore.closeDiffSidebar()"
            />
          </AnimatePresence>

          <AnimatePresence>
            <TerminalPanel
              v-if="terminalStore.terminalPanelOpen"
              key="terminal"
            />
          </AnimatePresence>

          <AnimatePresence>
            <BrowserLauncher
              v-if="browserLauncherOpen || browserStore.browserWindowOpen"
              key="browser"
              :open="browserLauncherOpen"
              @close="browserLauncherOpen = false"
            />
          </AnimatePresence>
        </div>
      </Motion>
    </Motion>

    <AnimatePresence>
      <Motion
        v-if="!shellStore.sidebarCollapsed && isMobile"
        key="sidebar-overlay"
        as="button"
        type="button"
        class="fixed inset-y-0 left-[292px] right-0 z-30 bg-black/20 backdrop-blur-[1px]"
        aria-label="Close sidebar overlay"
        :initial="overlayFade.initial"
        :animate="overlayFade.animate"
        :exit="overlayFade.exit"
        :transition="overlayFade.transition"
        @click="shellStore.setSidebarCollapsed(true)"
      />
    </AnimatePresence>
  </Motion>
</template>
