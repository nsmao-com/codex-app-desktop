<script setup lang="ts">
import { Brain, FolderOpen } from '@lucide/vue'
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { MemoriesOverview } from '../../bindings/nice_codex_desktop/models'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Switch } from '@/components/ui/switch'
import { useCodexStore } from '@/stores'
import { notify } from '@/utils/notify'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const codexStore = useCodexStore()

const loading = shallowRef(false)
const overview = shallowRef<MemoriesOverview | null>(null)
const useMemories = shallowRef(true)
const generateMemories = shallowRef(true)
const saving = shallowRef(false)

const threadId = computed(() => codexStore.activeThreadId)
const hasThread = computed(() => Boolean(threadId.value) && !threadId.value.startsWith('pending-thread-'))

watch(open, async (value) => {
  if (!value) return
  await refresh()
})

async function refresh(): Promise<void> {
  loading.value = true
  try {
    overview.value = await backend.ListLocalMemories()
    const thread = codexStore.activeThread
    useMemories.value = thread?.useMemories !== false
    generateMemories.value = thread?.generateMemories !== false
  } catch (error) {
    notify('error', t('memories.loadFailed'), error instanceof Error ? error.message : t('notifications.unexpected'))
  } finally {
    loading.value = false
  }
}

async function persist(): Promise<void> {
  if (!hasThread.value) {
    notify('info', t('memories.needThread'), t('memories.needThreadHint'))
    return
  }
  saving.value = true
  try {
    await backend.UpdateSessionMemories({
      sessionId: threadId.value,
      useMemories: useMemories.value,
      generateMemories: generateMemories.value,
    })
    codexStore.patchActiveThreadMemories(useMemories.value, generateMemories.value)
    notify('success', t('memories.saved'))
  } catch (error) {
    notify('error', t('memories.saveFailed'), error instanceof Error ? error.message : t('notifications.unexpected'))
  } finally {
    saving.value = false
  }
}

async function openFolder(): Promise<void> {
  try {
    await backend.OpenMemoriesFolder()
  } catch (error) {
    notify('error', t('memories.openFailed'), error instanceof Error ? error.message : t('notifications.unexpected'))
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="open = $event">
    <DialogContent class="gap-0 overflow-hidden p-0 sm:max-w-lg">
      <DialogHeader class="border-b px-4 py-3">
        <DialogTitle class="flex items-center gap-2 text-[14px]">
          <Brain :size="15" class="text-primary" />
          {{ t('memories.title') }}
        </DialogTitle>
        <DialogDescription class="text-[11px]">
          {{ t('memories.hint') }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-3 px-4 py-3">
        <div class="rounded-lg border bg-muted/20 px-3 py-2 text-[11px] text-muted-foreground">
          <p>{{ overview?.enabled ? t('memories.featureOn') : t('memories.featureOff') }}</p>
          <p v-if="overview?.root" class="mt-1 truncate font-mono text-[10px]" :title="overview.root">{{ overview.root }}</p>
        </div>

        <div class="divide-y rounded-lg border">
          <div class="flex items-center justify-between gap-3 px-3 py-2.5">
            <div class="min-w-0">
              <p class="text-[13px]">{{ t('memories.useThisChat') }}</p>
              <p class="text-[11px] text-muted-foreground">{{ t('memories.useThisChatHint') }}</p>
            </div>
            <Switch :checked="useMemories" :disabled="!hasThread" @update:checked="useMemories = $event" />
          </div>
          <div class="flex items-center justify-between gap-3 px-3 py-2.5">
            <div class="min-w-0">
              <p class="text-[13px]">{{ t('memories.generateThisChat') }}</p>
              <p class="text-[11px] text-muted-foreground">{{ t('memories.generateThisChatHint') }}</p>
            </div>
            <Switch :checked="generateMemories" :disabled="!hasThread" @update:checked="generateMemories = $event" />
          </div>
        </div>

        <div v-if="overview?.summaryPreview" class="rounded-lg border px-3 py-2">
          <p class="text-[11px] font-medium">{{ t('memories.summary') }}</p>
          <p class="mt-1 whitespace-pre-wrap text-[11px] leading-5 text-muted-foreground">{{ overview.summaryPreview }}</p>
        </div>
        <div v-else-if="!loading" class="rounded-lg border border-dashed px-3 py-4 text-center text-[11px] text-muted-foreground">
          {{ t('memories.empty') }}
        </div>
      </div>

      <div class="flex items-center justify-between gap-2 border-t px-4 py-3">
        <Button type="button" variant="outline" size="sm" class="h-8 text-xs" @click="openFolder">
          <FolderOpen :size="13" class="mr-1.5" />
          {{ t('memories.openFolder') }}
        </Button>
        <Button type="button" size="sm" class="h-8 text-xs" :disabled="saving || !hasThread" @click="persist">
          {{ saving ? t('common.saving') : t('common.save') }}
        </Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
