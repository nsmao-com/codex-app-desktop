<script setup lang="ts">
import { Braces, FileSearch, GitPullRequestArrow } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const { t } = useI18n()

const emit = defineEmits<{
  suggestion: [prompt: string]
}>()

const isCowork = computed(() => appStore.settings.workMode === 'cowork')
const suggestions = computed(() => [
  { icon: FileSearch, title: t('chat.traceBug'), prompt: t('chat.traceBugPrompt') },
  { icon: Braces, title: t('chat.understandCodebase'), prompt: t('chat.understandCodebasePrompt') },
  { icon: GitPullRequestArrow, title: t('chat.reviewChanges'), prompt: t('chat.reviewChangesPrompt') },
])
const runtimeWarning = computed(() => {
  if (appStore.codexAvailable) return ''
  return appStore.codexVersion || t('welcome.cliRequired')
})
</script>

<template>
  <div class="flex h-full flex-col items-center justify-center px-6 text-center">
    <h2 class="text-lg font-medium tracking-tight text-foreground">
      {{ isCowork ? t('chat.coworkTitle') : t('chat.title') }}
    </h2>
    <p class="mt-1.5 max-w-md text-[13px] leading-5 text-muted-foreground">
      {{ isCowork ? t('chat.coworkDescription') : t('chat.description') }}
    </p>

    <div class="mt-7 flex w-full max-w-xl flex-wrap items-center justify-center gap-2">
      <button
        v-for="suggestion in suggestions"
        :key="suggestion.title"
        type="button"
        class="inline-flex h-8 items-center gap-1.5 rounded-full border border-border/70 bg-card px-3 text-[12px] text-muted-foreground transition-colors hover:border-border hover:bg-muted/50 hover:text-foreground"
        :title="suggestion.prompt"
        @click="emit('suggestion', suggestion.prompt)"
      >
        <component :is="suggestion.icon" :size="13" class="opacity-70" />
        {{ suggestion.title }}
      </button>
    </div>

    <div
      v-if="runtimeWarning"
      class="mt-6 max-w-md rounded-md border border-destructive/20 bg-destructive/5 px-3 py-2.5 text-[12px] leading-5 text-destructive"
    >
      {{ runtimeWarning }}
    </div>

    <div v-if="!workspaceStore.workspace" class="mt-5">
      <Button variant="secondary" size="sm" class="h-8 text-[12px]" @click="codexStore.selectProject">
        {{ t('welcome.chooseWorkspace') }}
      </Button>
    </div>
  </div>
</template>
