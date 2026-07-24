<script setup lang="ts">
import { Braces, FileSearch, GitPullRequestArrow } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { useAppStore, useClaudeStore, useCodexStore, useGrokStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const { t } = useI18n()

const emit = defineEmits<{
  suggestion: [prompt: string]
}>()

const isGrok = computed(() => appStore.isGrokMode)
const isClaude = computed(() => appStore.isClaudeMode)
const isCowork = computed(() => !isGrok.value && !isClaude.value && appStore.settings.workMode === 'cowork')
const titleText = computed(() => {
  if (isGrok.value) return t('chat.grokTitle')
  if (isClaude.value) return t('chat.claudeTitle')
  return isCowork.value ? t('chat.coworkTitle') : t('chat.title')
})
const titleChars = computed(() => [...titleText.value])
const suggestions = computed(() => [
  { icon: FileSearch, title: t('chat.traceBug'), prompt: t('chat.traceBugPrompt') },
  { icon: Braces, title: t('chat.understandCodebase'), prompt: t('chat.understandCodebasePrompt') },
  { icon: GitPullRequestArrow, title: t('chat.reviewChanges'), prompt: t('chat.reviewChangesPrompt') },
])
const runtimeWarning = computed(() => {
  if (isGrok.value) {
    if (grokStore.isReady) return ''
    return t('sidebar.grokRuntimeMissing')
  }
  if (isClaude.value) {
    if (claudeStore.isReady) return ''
    return claudeStore.runtime.message || t('sidebar.claudeRuntimeMissing')
  }
  if (appStore.codexAvailable) return ''
  return appStore.codexVersion || t('welcome.cliRequired')
})
const needsWorkspace = computed(() => !workspaceStore.workspace)
const kickerText = computed(() => {
  if (isGrok.value) return t('chat.grokReadyHere')
  if (isClaude.value) return t('chat.claudeReadyHere')
  return t('chat.readyHere')
})
const descriptionText = computed(() => {
  if (isGrok.value) return t('chat.grokDescription')
  if (isClaude.value) return t('chat.claudeDescription')
  return isCowork.value ? t('chat.coworkDescription') : t('chat.description')
})

function chooseWorkspace(): void {
  if (isGrok.value || isClaude.value) {
    void workspaceStore.selectWorkspace().then(() => {
      if (isGrok.value) void grokStore.loadSessions()
      else void claudeStore.loadSessions()
    })
    return
  }
  void codexStore.selectProject()
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
