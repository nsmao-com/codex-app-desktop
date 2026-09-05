<script setup lang="ts">
import { AlertTriangle, Check, Copy, LoaderCircle, RotateCcw, Settings2 } from '@lucide/vue'
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { SimpleTooltip } from '@/components/ui/tooltip'
import { useCodexStore } from '@/stores/codex'
import type { CodexWarning } from '@/utils/codexWarnings'
import { sameWorkspacePath } from '@/utils/workspacePath'

const props = defineProps<{ threadId: string; workspace: string }>()
const store = useCodexStore()
const { t } = useI18n()
const router = useRouter()
const open = shallowRef(false)
const copied = shallowRef(false)
const copyFailed = shallowRef(false)
const rechecking = shallowRef(false)
const warnings = computed(() => store.warnings.filter((warning) => {
  if (warning.threadId) return store.sameThread(warning.threadId, props.threadId)
  return warning.kind !== 'runtime' || !warning.workspace || sameWorkspacePath(warning.workspace, props.workspace)
}))
const label = computed(() => t('codexWarnings.indicator', { count: warnings.value.length }))
const tooltip = computed(() => `${label.value}\n${summary(warnings.value[0])}\n${t('codexWarnings.hoverHint')}`)
const canRecheck = computed(() => Boolean(props.workspace)
  && !store.busy && !rechecking.value
  && !store.loadingSequenceByThread.size && !store.workspaceSelectionSequenceByThread.size
  && !store.runningThreadIds.length && !store.pendingRequests.length
  && !store.threads.some((thread) => store.threadHasActiveWork(thread.id))
  && !Object.values(store.queuedMessagesByThread).some((queue) => queue.length > 0))

function summary(warning?: CodexWarning): string {
  if (!warning) return ''
  return warning.summary || (warning.method === 'windows/worldWritableWarning'
    ? t('codexWarnings.worldWritable') : t('codexWarnings.missingSummary'))
}

async function copyDiagnostics(): Promise<void> {
  copied.value = false
  copyFailed.value = false
  try {
    const diagnostics = warnings.value.map((warning) => [
      summary(warning), warning.details,
      warning.path ? `${warning.path}${warning.location ? `:${warning.location}` : ''}` : '',
      ...warning.samplePaths,
      warning.extraCount ? t('codexWarnings.extraPaths', { count: warning.extraCount }) : '',
      warning.failedScan ? t('codexWarnings.scanFailed') : '',
    ].filter(Boolean).join('\n')).join('\n\n')
    await navigator.clipboard.writeText(diagnostics)
    copied.value = true
  } catch {
    copyFailed.value = true
  }
}

async function recheck(): Promise<void> {
  // Never stop a live/queued task merely to refresh diagnostics.
  if (!canRecheck.value) return
  rechecking.value = true
  try {
    await store.connect(props.workspace, { forceRestart: true })
  } finally {
    rechecking.value = false
  }
}

function openSettings(): void {
  open.value = false
  void router.push({ name: 'settings', query: { section: 'agent' } })
}
</script>

<template>
  <Popover v-if="warnings.length" v-model:open="open">
    <PopoverTrigger as-child>
      <Button
        type="button" variant="ghost" size="sm"
        class="h-7 shrink-0 gap-1 rounded-lg px-1.5 text-[11px] text-amber-700 hover:bg-amber-500/10 dark:text-amber-400"
        :aria-label="label"
        :aria-description="tooltip"
        @click="copied = false; copyFailed = false"
      >
        <SimpleTooltip :content="tooltip" :disabled="open" content-class="max-w-80 whitespace-pre-line break-words">
          <span class="inline-flex items-center gap-1">
            <AlertTriangle :size="14" aria-hidden="true" />
            <span class="tabular-nums">{{ warnings.length }}</span>
          </span>
        </SimpleTooltip>
      </Button>
    </PopoverTrigger>
    <PopoverContent
      align="start" side="top" :side-offset="10"
      class="w-[min(440px,calc(100vw-24px))] overflow-hidden rounded-xl p-0 shadow-lg"
      :aria-label="t('codexWarnings.title')"
    >
      <div class="border-b px-4 py-3">
        <h3 class="flex items-center gap-2 text-sm font-medium">
          <AlertTriangle :size="15" class="text-amber-600 dark:text-amber-400" />
          {{ t('codexWarnings.title') }}
          <span class="ml-auto text-xs text-muted-foreground">{{ warnings.length }}</span>
        </h3>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">{{ t('codexWarnings.hint') }}</p>
      </div>
      <div class="max-h-[min(360px,45vh)] overflow-y-auto overscroll-contain px-4">
        <article v-for="warning in warnings" :key="warning.id" class="space-y-2 border-b py-3 last:border-0">
          <span class="text-[10px] font-medium text-amber-700 dark:text-amber-400">{{ t(`codexWarnings.${warning.kind}`) }}</span>
          <p class="whitespace-pre-wrap break-words text-xs font-medium leading-5 [overflow-wrap:anywhere]">{{ summary(warning) }}</p>
          <p v-if="warning.details" class="whitespace-pre-wrap break-words text-xs leading-5 text-muted-foreground [overflow-wrap:anywhere]">{{ warning.details }}</p>
          <div v-if="warning.path" class="rounded-lg bg-muted/60 px-2.5 py-2 text-[11px]">
            <p class="mb-1 text-muted-foreground">{{ t('codexWarnings.location') }}</p>
            <p class="select-text break-all font-mono">{{ warning.path }}<span v-if="warning.location">:{{ warning.location }}</span></p>
          </div>
          <p v-else-if="warning.kind === 'config'" class="text-[11px] leading-5 text-muted-foreground">{{ t('codexWarnings.noPath') }}</p>
          <ul v-if="warning.samplePaths.length" class="space-y-1 rounded-lg bg-muted/60 p-2 text-[11px]">
            <li v-for="(path, index) in warning.samplePaths" :key="index" class="select-text break-all font-mono">{{ path }}</li>
          </ul>
          <p v-if="warning.extraCount" class="text-xs text-muted-foreground">{{ t('codexWarnings.extraPaths', { count: warning.extraCount }) }}</p>
          <p v-if="warning.failedScan" class="text-xs text-amber-700 dark:text-amber-400">{{ t('codexWarnings.scanFailed') }}</p>
          <p v-if="warning.kind === 'config'" class="text-[11px] leading-5 text-muted-foreground">{{ t('codexWarnings.configHelp') }}</p>
          <p v-if="warning.kind === 'compatibility'" class="text-[11px] leading-5 text-muted-foreground">{{ t('codexWarnings.compatibilityHelp') }}</p>
        </article>
      </div>
      <div class="space-y-2 border-t bg-muted/20 px-3 py-2.5">
        <div class="flex flex-wrap items-center gap-1">
          <Button type="button" variant="ghost" size="sm" class="h-8 gap-1.5 text-xs" @click="copyDiagnostics">
            <Check v-if="copied" :size="13" /><Copy v-else :size="13" />
            {{ t(copied ? 'codexWarnings.copied' : 'codexWarnings.copy') }}
          </Button>
          <Button type="button" variant="ghost" size="sm" class="h-8 gap-1.5 text-xs" @click="openSettings">
            <Settings2 :size="13" />{{ t('codexWarnings.settings') }}
          </Button>
          <Button type="button" variant="outline" size="sm" class="ml-auto h-8 gap-1.5 text-xs" :disabled="!canRecheck" @click="recheck">
            <LoaderCircle v-if="rechecking" :size="13" class="animate-spin" /><RotateCcw v-else :size="13" />
            {{ t('codexWarnings.recheck') }}
          </Button>
        </div>
        <p v-if="copyFailed" role="status" class="text-xs text-destructive">{{ t('codexWarnings.copyFailed') }}</p>
        <p class="px-1 text-[10px] leading-4 text-muted-foreground">{{ t(!workspace ? 'codexWarnings.noWorkspace' : canRecheck ? 'codexWarnings.recheckHint' : 'codexWarnings.busyHint') }}</p>
      </div>
    </PopoverContent>
  </Popover>
</template>
