<script setup lang="ts">
import { Braces, FileSearch, GitPullRequestArrow } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { useRuntimeMode } from '@/composables/useRuntimeMode'
import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const {
  runtime: paneRuntime,
  isArenaPane,
  paneId,
  isCodexMode,
  isClaudeMode,
  isGrokMode,
  isGeminiMode,
  isOpenCodeMode,
} = useRuntimeMode()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const arenaStore = useArenaStore()
const workspaceStore = useWorkspaceStore()
const { t } = useI18n()

const emit = defineEmits<{
  suggestion: [prompt: string]
}>()

const isGrok = computed(() => isGrokMode.value)
const isClaude = computed(() => isClaudeMode.value)
const isGemini = computed(() => isGeminiMode.value)
const isOpenCode = computed(() => isOpenCodeMode.value)
const isExternal = computed(() => isGemini.value || isOpenCode.value)
const externalRuntimeName = computed(() => isGemini.value ? 'Gemini CLI' : 'OpenCode')
const externalProvider = computed(() => appStore.agentProviders.find((item) => item.kind === paneRuntime.value))
const isCowork = computed(() => isCodexMode.value && appStore.settings.workMode === 'cowork')
const titleText = computed(() => {
  if (isGrok.value) return t('chat.grokTitle')
  if (isClaude.value) return t('chat.claudeTitle')
  if (isExternal.value) return t('chat.runtimeTitle', { runtime: externalRuntimeName.value })
  return isCowork.value ? t('chat.coworkTitle') : t('chat.title')
})
const titleChars = computed(() => [...titleText.value])
const suggestions = computed(() => {
  if (isGrok.value) {
    return [
      { icon: FileSearch, title: t('chat.grokTraceBug'), prompt: t('chat.grokTraceBugPrompt') },
      { icon: Braces, title: t('chat.grokUnderstandCodebase'), prompt: t('chat.grokUnderstandCodebasePrompt') },
      { icon: GitPullRequestArrow, title: t('chat.grokReviewChanges'), prompt: t('chat.grokReviewChangesPrompt') },
    ]
  }
  if (isClaude.value) {
    return [
      { icon: FileSearch, title: t('chat.claudeTraceBug'), prompt: t('chat.claudeTraceBugPrompt') },
      { icon: Braces, title: t('chat.claudeUnderstandCodebase'), prompt: t('chat.claudeUnderstandCodebasePrompt') },
      { icon: GitPullRequestArrow, title: t('chat.claudeReviewChanges'), prompt: t('chat.claudeReviewChangesPrompt') },
    ]
  }
  if (isGemini.value) {
    return [
      { icon: FileSearch, title: t('chat.geminiTraceBug'), prompt: t('chat.geminiTraceBugPrompt') },
      { icon: Braces, title: t('chat.geminiUnderstandCodebase'), prompt: t('chat.geminiUnderstandCodebasePrompt') },
      { icon: GitPullRequestArrow, title: t('chat.geminiReviewChanges'), prompt: t('chat.geminiReviewChangesPrompt') },
    ]
  }
  if (isOpenCode.value) {
    return [
      { icon: FileSearch, title: t('chat.opencodeTraceBug'), prompt: t('chat.opencodeTraceBugPrompt') },
      { icon: Braces, title: t('chat.opencodeUnderstandCodebase'), prompt: t('chat.opencodeUnderstandCodebasePrompt') },
      { icon: GitPullRequestArrow, title: t('chat.opencodeReviewChanges'), prompt: t('chat.opencodeReviewChangesPrompt') },
    ]
  }
  return [
    { icon: FileSearch, title: t('chat.traceBug'), prompt: t('chat.traceBugPrompt') },
    { icon: Braces, title: t('chat.understandCodebase'), prompt: t('chat.understandCodebasePrompt') },
    { icon: GitPullRequestArrow, title: t('chat.reviewChanges'), prompt: t('chat.reviewChangesPrompt') },
  ]
})
const runtimeWarning = computed(() => {
  if (isGrok.value) {
    if (grokStore.isReady) return ''
    return t('sidebar.grokRuntimeMissing')
  }
  if (isClaude.value) {
    if (claudeStore.isReady) return ''
    return claudeStore.runtime.message || t('sidebar.claudeRuntimeMissing')
  }
  if (isExternal.value) {
    if (externalProvider.value?.runtimeReady) return ''
    return externalProvider.value?.message
      || t('welcome.runtimeNotReady', { name: externalRuntimeName.value })
  }
  if (appStore.codexAvailable) return ''
  return appStore.codexVersion || t('welcome.cliRequired')
})
const runtimeWorkspacePath = computed(() => {
  if (isGrok.value) return grokStore.workspacePath
  if (isClaude.value) return claudeStore.workspacePath
  if (isGemini.value) return appStore.settings.geminiWorkspace || appStore.settings.workspace
  if (isOpenCode.value) return appStore.settings.openCodeWorkspace || appStore.settings.workspace
  return appStore.settings.workspace
})
const needsWorkspace = computed(() => !runtimeWorkspacePath.value)
const kickerText = computed(() => {
  if (isGrok.value) return t('chat.grokReadyHere')
  if (isClaude.value) return t('chat.claudeReadyHere')
  if (isExternal.value) return t('chat.runtimeReadyHere', { runtime: externalRuntimeName.value })
  return t('chat.readyHere')
})
const descriptionText = computed(() => {
  if (isGrok.value) return t('chat.grokDescription')
  if (isClaude.value) return t('chat.claudeDescription')
  if (isExternal.value) return t('chat.runtimeDescription', { runtime: externalRuntimeName.value })
  return isCowork.value ? t('chat.coworkDescription') : t('chat.description')
})

async function chooseWorkspace(): Promise<void> {
  const runtime = paneRuntime.value
  const targetPaneId = isArenaPane.value ? paneId.value : ''
  const previousSessionId = targetPaneId ? arenaStore.sessionForPane(targetPaneId) : ''
  const targetIsCurrent = () => targetPaneId
    ? Boolean(
        arenaStore.isArenaMode
        && arenaStore.focusedPaneId === targetPaneId
        && arenaStore.panes.some((pane) => pane.id === targetPaneId && pane.runtime === runtime)
        && arenaStore.sessionForPane(targetPaneId) === previousSessionId,
      )
    : !arenaStore.isArenaMode && appStore.activeRuntime === runtime
  const ready = appStore.activeRuntime === runtime
    ? await appStore.ensureActiveRuntimeSynced(runtime)
    : await appStore.setActiveRuntime(runtime)
  if (!ready || !targetIsCurrent()) return
  if (isGrok.value) {
    const path = await workspaceStore.selectWorkspace()
    if (!path || !targetIsCurrent()) return
    await grokStore.loadSessions(true)
    if (!targetIsCurrent()) return
    const group = grokStore.sessionGroups.find((item) => item.active)
    const selectedId = targetPaneId ? arenaStore.sessionForPane(targetPaneId) : grokStore.activeSessionId
    const target = group?.sessions.find((item) => grokStore.sameSession(item.id, selectedId))
      || group?.sessions.find((item) => !targetPaneId || !arenaStore.isSessionTakenByOtherPane(targetPaneId, item.id, runtime))
      || group?.sessions[0]
    if (target) {
      if (isArenaPane.value) arenaStore.selectPaneSession(paneId.value, target.id)
      else await grokStore.openSession(target.id, { switchWorkspace: false })
    } else {
      grokStore.newSession()
      if (targetPaneId) arenaStore.setPaneSession(targetPaneId, '')
    }
    return
  }
  if (isClaude.value) {
    const path = await workspaceStore.selectWorkspace()
    if (!path || !targetIsCurrent()) return
    await claudeStore.loadSessions()
    if (!targetIsCurrent()) return
    const group = claudeStore.sessionGroups.find((item) => item.active)
    const selectedId = targetPaneId ? arenaStore.sessionForPane(targetPaneId) : claudeStore.activeSessionId
    const target = group?.sessions.find((item) => claudeStore.sameSession(item.id, selectedId))
      || group?.sessions.find((item) => !targetPaneId || !arenaStore.isSessionTakenByOtherPane(targetPaneId, item.id, runtime))
      || group?.sessions[0]
    if (target) {
      if (isArenaPane.value) arenaStore.selectPaneSession(paneId.value, target.id)
      else await claudeStore.openSession(target.id, { switchWorkspace: false })
    } else {
      const sessionId = claudeStore.newSession(Boolean(targetPaneId))
      if (targetPaneId && sessionId) {
        arenaStore.setPaneSession(targetPaneId, sessionId)
      }
    }
    return
  }
  await codexStore.selectProject()
  if (targetPaneId && targetIsCurrent() && codexStore.activeThreadId) {
    arenaStore.selectPaneSession(targetPaneId, codexStore.activeThreadId)
  }
}
</script>

<template>
  <div class="welcome-stage relative flex h-full flex-col items-center justify-center overflow-hidden px-6 text-center">
    <div class="welcome-aurora pointer-events-none absolute inset-0" aria-hidden="true" />
    <div class="welcome-orb welcome-orb-a pointer-events-none absolute" aria-hidden="true" />
    <div class="welcome-orb welcome-orb-b pointer-events-none absolute" aria-hidden="true" />
    <div class="welcome-grid pointer-events-none absolute inset-0" aria-hidden="true" />

    <div class="welcome-enter relative z-[1] flex flex-col items-center">
      <p class="welcome-kicker mb-3 text-[10px] font-medium tracking-[0.2em] text-muted-foreground uppercase">
        {{ kickerText }}
      </p>

      <h2 class="welcome-headline text-xl font-semibold tracking-tight text-foreground sm:text-3xl">
        <span
          v-for="(char, index) in titleChars"
          :key="`${char}-${index}`"
          class="welcome-char"
          :style="{ animationDelay: `${120 + index * 28}ms` }"
        >{{ char === ' ' ? '\u00A0' : char }}</span>
      </h2>
      <div class="welcome-underline mt-3 h-[2px] w-24 rounded-full" aria-hidden="true" />

      <p class="welcome-desc mt-4 max-w-md text-[13px] leading-6 text-muted-foreground">
        {{ descriptionText }}
      </p>
    </div>

    <div class="relative z-[1] mt-9 flex w-full max-w-xl flex-wrap items-center justify-center gap-2">
      <button
        v-for="(suggestion, index) in suggestions"
        :key="suggestion.title"
        type="button"
        class="welcome-chip inline-flex h-9 items-center gap-1.5 rounded-full border border-border/70 bg-card/90 px-3.5 text-[12px] text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:border-foreground/20 hover:bg-muted/60 hover:text-foreground"
        :style="{ animationDelay: `${520 + index * 90}ms` }"
        :title="suggestion.prompt"
        @click="emit('suggestion', suggestion.prompt)"
      >
        <component :is="suggestion.icon" :size="13" class="opacity-70" />
        {{ suggestion.title }}
      </button>
    </div>

    <div
      v-if="runtimeWarning"
      class="welcome-note relative z-[1] mt-6 max-w-md rounded-md border border-destructive/20 bg-destructive/5 px-3 py-2.5 text-[12px] leading-5 text-destructive"
    >
      {{ runtimeWarning }}
    </div>

    <div
      v-else-if="needsWorkspace"
      class="welcome-note relative z-[1] mt-6 max-w-md rounded-md border border-border/70 bg-muted/30 px-3 py-2.5 text-[12px] leading-5 text-muted-foreground"
    >
      {{ t('app.needWorkspaceHintReady') }}
    </div>

    <div v-if="needsWorkspace" class="welcome-note relative z-[1] mt-5">
      <Button variant="secondary" size="sm" class="h-8 text-[12px]" @click="chooseWorkspace">
        {{ t('welcome.chooseWorkspace') }}
      </Button>
    </div>
  </div>
</template>
