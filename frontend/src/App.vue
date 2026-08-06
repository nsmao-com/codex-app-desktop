<script setup lang="ts">
import { MotionConfig } from 'motion-v'
import { computed, onMounted, onUnmounted, shallowRef, watch } from 'vue'
import { RouterView } from 'vue-router'

import * as backend from '../bindings/nice_codex_desktop/appservice'
import CommandPalette from '@/components/CommandPalette.vue'
import MemoriesDialog from '@/components/MemoriesDialog.vue'
import AppPromptDialog from '@/components/AppPromptDialog.vue'
import TitleBar from '@/components/TitleBar.vue'
import UpdateCheckDialog from '@/components/UpdateCheckDialog.vue'
import OnboardingView from '@/views/OnboardingView.vue'
import { Toaster } from '@/components/ui/sonner'
import { useNavigationHistory } from '@/composables/useNavigationHistory'
import { useAppStore, useBrowserStore, useClaudeStore, useCodexStore, useGrokStore, useTerminalStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const terminalStore = useTerminalStore()
const browserStore = useBrowserStore()
const commandPaletteOpen = shallowRef(false)
const memoriesOpen = shallowRef(false)
let runtimeSwitchReady = false
let runtimeActivationSequence = 0
let bootstrapActivationRuntime: WorkspaceRuntime | null = null
let bootstrapActivationCompleted = false

useNavigationHistory()

const showOnboarding = computed(() =>
  !appStore.bootstrapping && !appStore.settings.onboardingCompleted,
)

const anyTurnRunning = computed(() =>
  codexStore.runningThreadIds.length > 0
  || grokStore.runningSessionIds.length > 0
  || claudeStore.runningSessionIds.length > 0,
)
const preventSleepActive = computed(() =>
  Boolean(appStore.settings.preventSleepWhileRunning) && anyTurnRunning.value,
)

watch(preventSleepActive, (active) => {
  void backend.SetPreventSleepActive(active).catch(() => undefined)
}, { immediate: true })

function openMemoriesDialog(): void {
  memoriesOpen.value = true
}

async function activateRuntime(runtime: WorkspaceRuntime): Promise<void> {
  const sequence = ++runtimeActivationSequence
  if (!await appStore.ensureActiveRuntimeSynced(runtime)) return
  await workspaceStore.hydrateActiveRuntimeWorkspace()
  if (sequence !== runtimeActivationSequence || appStore.activeRuntime !== runtime) return
  if (runtime === 'grok') {
    await grokStore.enterRuntime()
    return
  }
  if (runtime === 'claude') {
    await claudeStore.enterRuntime()
    return
  }
  // Gemini/OpenCode share Codex's event bridge and timeline/FIFO store, but do
  // not require a Codex app-server connection.
  if (runtime === 'gemini' || runtime === 'opencode') {
    await Promise.all([
      codexStore.loadModels(),
      codexStore.loadThreads(),
    ])
    return
  }
  await Promise.all([
    codexStore.loadModels(),
    codexStore.loadModelProviders(),
    codexStore.loadThreads(),
  ])
  if (
    !codexStore.isReady
    && appStore.settings.workspace
    && appStore.settings.autoConnect
    && appStore.codexAvailable
  ) {
    void codexStore.connect(appStore.settings.workspace)
  }
}

onMounted(() => {
  codexStore.bootstrapEvents()
  grokStore.bootstrapEvents()
  claudeStore.bootstrapEvents()
  void appStore.bootstrap().then(async () => {
    if (appStore.workspace) workspaceStore.hydrateWorkspace(appStore.workspace)
    bootstrapActivationRuntime = appStore.activeRuntime
    await activateRuntime(bootstrapActivationRuntime)
    bootstrapActivationCompleted = true
  }).finally(() => {
    runtimeSwitchReady = true
    const currentRuntime = appStore.activeRuntime
    if (!bootstrapActivationCompleted || currentRuntime !== bootstrapActivationRuntime) {
      void activateRuntime(currentRuntime)
    }
  })
  window.addEventListener('keydown', onGlobalKeydown)
  window.addEventListener('nice-codex:open-memories', openMemoriesDialog)
})

// Defer heavy work until after the tab paint. Bootstrap owns the first activation.
watch(
  () => appStore.activeRuntime,
  (runtime, _previous, onCleanup) => {
    if (!runtimeSwitchReady) return
    const timer = window.setTimeout(() => {
      void activateRuntime(runtime)
    }, 0)
    onCleanup(() => window.clearTimeout(timer))
  },
)

onUnmounted(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
  window.removeEventListener('nice-codex:open-memories', openMemoriesDialog)
  void backend.SetPreventSleepActive(false).catch(() => undefined)
  codexStore.dispose()
  grokStore.dispose()
  claudeStore.dispose()
})

function matchShortcut(event: KeyboardEvent, binding: string): boolean {
  const parts = binding.toLowerCase().split('+').map((part) => part.trim()).filter(Boolean)
  if (!parts.length) return false
  const key = parts[parts.length - 1]
  const needCtrl = parts.includes('ctrl') || parts.includes('control') || parts.includes('cmd') || parts.includes('meta')
  const needShift = parts.includes('shift')
  const needAlt = parts.includes('alt')
  const ctrl = event.ctrlKey || event.metaKey
  if (needCtrl !== ctrl) return false
  if (needShift !== event.shiftKey) return false
  if (needAlt !== event.altKey) return false
  if (key === '`') return event.code === 'Backquote'
  return event.key.toLowerCase() === key.toLowerCase()
}

function onGlobalKeydown(event: KeyboardEvent): void {
  const settings = appStore.settings
  const paletteBinding = settings.shortcutCommandPalette || 'Ctrl+K'
  if (matchShortcut(event, paletteBinding)) {
    event.preventDefault()
    commandPaletteOpen.value = !commandPaletteOpen.value
    return
  }
  if (event.target instanceof HTMLElement && (event.target.isContentEditable || event.target.matches('input, textarea, [role="textbox"]'))) return
  const browserBinding = settings.shortcutBrowser || 'Ctrl+Shift+B'
  if (matchShortcut(event, browserBinding)) {
    event.preventDefault()
    browserStore.openBrowser('')
    return
  }
  const terminalBinding = settings.shortcutTerminal || 'Ctrl+`'
  if (matchShortcut(event, terminalBinding) && workspaceStore.workspace) {
    event.preventDefault()
    void terminalStore.openTerminal()
    return
  }
  const newThreadBinding = settings.shortcutNewThread || 'Ctrl+N'
  if (matchShortcut(event, newThreadBinding)) {
    event.preventDefault()
    if (appStore.isGrokMode) {
      grokStore.newSession()
      return
    }
    if (appStore.isClaudeMode) {
      claudeStore.newSession()
      return
    }
    if (appStore.isGeminiMode || appStore.isOpenCodeMode) {
      void codexStore.newThread()
      return
    }
    if (codexStore.isReady) void codexStore.newThread()
  }
}
</script>

<template>
  <MotionConfig :reducedMotion="'user'">
    <div class="app-shell flex h-screen w-screen flex-col overflow-hidden text-foreground">
      <TitleBar v-if="!showOnboarding" />
      <div class="relative min-h-0 flex-1 overflow-hidden">
        <OnboardingView v-if="showOnboarding" />
        <RouterView v-else v-slot="{ Component, route }">
          <Transition :name="route.name === 'workbench' ? 'route-fade' : 'route-slide'" mode="out-in">
            <KeepAlive include="WorkbenchView" :max="1">
              <component :is="Component" :key="String(route.name || route.path)" />
            </KeepAlive>
          </Transition>
        </RouterView>
      </div>
      <UpdateCheckDialog />
      <CommandPalette v-model:open="commandPaletteOpen" />
      <MemoriesDialog v-model:open="memoriesOpen" />
      <AppPromptDialog />
    </div>
    <Toaster position="bottom-right" :rich-colors="true" close-button />
  </MotionConfig>
</template>
