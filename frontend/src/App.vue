<script setup lang="ts">
import { MotionConfig } from 'motion-v'
import { computed, onMounted, onUnmounted, shallowRef, watch } from 'vue'
import { RouterView } from 'vue-router'
import { useRoute, useRouter } from 'vue-router'

import * as backend from '../bindings/nice_codex_desktop/appservice'
import CommandPalette from '@/components/CommandPalette.vue'
import MemoriesDialog from '@/components/MemoriesDialog.vue'
import AppPromptDialog from '@/components/AppPromptDialog.vue'
import TitleBar from '@/components/TitleBar.vue'
import UpdateCheckDialog from '@/components/UpdateCheckDialog.vue'
import OnboardingView from '@/views/OnboardingView.vue'
import SettingsView from '@/views/SettingsView.vue'
import WorkbenchView from '@/views/WorkbenchView.vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Toaster } from '@/components/ui/sonner'
import { useNavigationHistory } from '@/composables/useNavigationHistory'
import { useAppStore, useArenaStore, useBrowserStore, useClaudeStore, useCodexStore, useGrokStore, useSubagentsStore, useTerminalStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'

const appStore = useAppStore()
const route = useRoute()
const router = useRouter()
const arenaStore = useArenaStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const subagentsStore = useSubagentsStore()
const workspaceStore = useWorkspaceStore()
const terminalStore = useTerminalStore()
const browserStore = useBrowserStore()
const commandPaletteOpen = shallowRef(false)
const memoriesOpen = shallowRef(false)
let runtimeSwitchReady = false
let runtimeActivationSequence = 0
let bootstrapActivationRuntime: WorkspaceRuntime | null = null
let bootstrapActivationCompleted = false
const lastCodexTimelineThreadByRuntime = new Map<WorkspaceRuntime, string>()

function usesCodexTimeline(runtime: WorkspaceRuntime): boolean {
  return runtime === 'codex' || runtime === 'gemini' || runtime === 'opencode'
}

useNavigationHistory()

const showOnboarding = computed(() =>
  !appStore.bootstrapping && !appStore.settings.onboardingCompleted,
)
const showWorkbench = computed(() => route.name === 'workbench' || route.name === 'settings')
const settingsOpen = computed(() => !showOnboarding.value && route.name === 'settings')

function onSettingsOpenChange(open: boolean): void {
  if (open || route.name !== 'settings') return
  const from = typeof route.query.from === 'string' ? route.query.from : ''
  void router.replace(from === 'capabilities' ? { name: 'capabilities' } : { name: 'workbench' })
}

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
  if (sequence !== runtimeActivationSequence || appStore.activeRuntime !== runtime) return
  // Arena multi-pane keeps each provider's active session so split views stay populated.
  // Single-pane mode still clears pointers when leaving a timeline family.
  const arenaKeepSessions = arenaStore.isArenaMode
  // Codex, Gemini and OpenCode share one timeline store. Clear only the
  // selected pointer when entering the independent providers so composer
  // actions can never target a stale Codex session; background turns remain in
  // their per-thread maps and continue to receive events.
  if (!arenaKeepSessions && (runtime === 'grok' || runtime === 'claude')) {
    await codexStore.clearActiveSession()
  }
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
    // The Codex store is shared by the three timeline runtimes. Never leave a
    // Codex thread active while an external runtime is selected: composer model
    // edits and queued messages would otherwise target the wrong provider.
    if (!arenaKeepSessions) {
      await codexStore.clearActiveSession()
    }
    await Promise.all([
      codexStore.loadModels(),
      codexStore.loadThreads(),
    ])
    // External runtimes do not start the Codex connection, so the usual
    // account-insights refresh is not reached. Warm the runtime-scoped native
    // usage snapshot here so the sidebar shows totals without requiring the
    // user to open the usage popover first.
    void appStore.loadLocalUsage().catch(() => undefined)
    const rememberedID = lastCodexTimelineThreadByRuntime.get(runtime) || ''
    const remembered = rememberedID
      ? codexStore.threadGroups.flatMap((group) => group.threads).find((thread) => thread.id === rememberedID)
      : undefined
    if (!arenaKeepSessions && remembered && sequence === runtimeActivationSequence && appStore.activeRuntime === runtime) {
      await codexStore.openThread(remembered.id)
    }
    return
  }
  if (!arenaKeepSessions && runtime === 'codex') {
    await codexStore.clearActiveSession()
  }
  const autoConnect = Boolean(
    !codexStore.isReady
    && appStore.settings.workspace
    && appStore.settings.autoConnect
    && appStore.codexAvailable,
  )
  if (autoConnect) {
    // connect() already loads models, providers, threads, account, and workspace.
    // Starting it here avoids doing all of that twice before launch can connect.
    const connected = await codexStore.connect(appStore.settings.workspace)
    if (!connected || sequence !== runtimeActivationSequence || appStore.activeRuntime !== runtime) return
    const rememberedID = lastCodexTimelineThreadByRuntime.get('codex') || ''
    const remembered = rememberedID
      ? codexStore.threadGroups.flatMap((group) => group.threads).find((thread) => thread.id === rememberedID)
      : undefined
    if (!arenaKeepSessions && remembered) await codexStore.openThread(remembered.id)
    return
  }
  await Promise.all([
    codexStore.loadModels(),
    codexStore.loadModelProviders(),
    codexStore.loadThreads(),
  ])
  const rememberedID = lastCodexTimelineThreadByRuntime.get('codex') || ''
  const remembered = rememberedID
    ? codexStore.threadGroups.flatMap((group) => group.threads).find((thread) => thread.id === rememberedID)
    : undefined
  if (!arenaKeepSessions && remembered && sequence === runtimeActivationSequence && appStore.activeRuntime === runtime) {
    await codexStore.openThread(remembered.id)
  }
}

onMounted(() => {
  codexStore.bootstrapEvents()
  grokStore.bootstrapEvents()
  claudeStore.bootstrapEvents()
  // Keep child-agent activity even while the inspector is collapsed.
  subagentsStore.bootstrapEvents()
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
  (runtime, previous, onCleanup) => {
    if (!runtimeSwitchReady) return
    if (previous && usesCodexTimeline(previous) && codexStore.activeThreadId) {
      lastCodexTimelineThreadByRuntime.set(previous, codexStore.activeThreadId)
    }
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
  subagentsStore.dispose()
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

async function createShortcutSession(): Promise<void> {
  const pane = arenaStore.isArenaMode ? arenaStore.focusedPane : null
  const runtime = pane?.runtime || appStore.activeRuntime
  const previousSessionId = pane ? arenaStore.sessionForPane(pane.id) : ''
  if (runtime === 'grok') {
    grokStore.newSession()
    if (pane) arenaStore.setPaneSession(pane.id, grokStore.activeSessionId)
    return
  }
  if (runtime === 'claude') {
    const sessionId = claudeStore.newSession(Boolean(pane))
    if (pane && sessionId) arenaStore.setPaneSession(pane.id, sessionId)
    return
  }
  const thread = pane
    ? await codexStore.newRuntimeThread(runtime, true)
    : await codexStore.newThread()
  if (
    pane
    && thread?.id
    && arenaStore.isArenaMode
    && arenaStore.focusedPaneId === pane.id
    && arenaStore.panes.some((item) => item.id === pane.id && item.runtime === runtime)
    && arenaStore.sessionForPane(pane.id) === previousSessionId
  ) arenaStore.setPaneSession(pane.id, thread.id)
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
    void createShortcutSession()
  }
}
</script>

<template>
  <MotionConfig :reducedMotion="'user'">
    <div class="app-shell flex h-screen w-screen flex-col overflow-hidden text-foreground">
      <TitleBar v-if="!showOnboarding" />
      <div class="relative min-h-0 flex-1 overflow-hidden">
        <OnboardingView v-if="showOnboarding" />
        <template v-else>
          <KeepAlive include="WorkbenchView" :max="1">
            <WorkbenchView v-if="showWorkbench" />
          </KeepAlive>
          <RouterView v-if="!showWorkbench" v-slot="{ Component, route: activeRoute }">
            <Transition name="route-slide" mode="out-in">
              <component :is="Component" :key="String(activeRoute.name || activeRoute.path)" />
            </Transition>
          </RouterView>
          <Dialog :open="settingsOpen" @update:open="onSettingsOpenChange">
            <DialogContent
              :show-close-button="false"
              class="h-[min(880px,calc(100vh-3rem))] w-[min(1180px,calc(100vw-3rem))] max-w-none gap-0 overflow-hidden rounded-2xl bg-background/98 p-0 shadow-2xl backdrop-blur-xl sm:max-w-none"
            >
              <DialogTitle class="sr-only">{{ $t('settings.title') }}</DialogTitle>
              <DialogDescription class="sr-only">{{ $t('settings.pageDescription') }}</DialogDescription>
              <SettingsView />
            </DialogContent>
          </Dialog>
        </template>
      </div>
      <UpdateCheckDialog />
      <CommandPalette v-model:open="commandPaletteOpen" />
      <MemoriesDialog v-model:open="memoriesOpen" />
      <AppPromptDialog />
    </div>
    <Toaster position="bottom-right" :rich-colors="true" close-button />
  </MotionConfig>
</template>
