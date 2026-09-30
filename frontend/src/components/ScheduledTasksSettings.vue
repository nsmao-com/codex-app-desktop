<script setup lang="ts">
import { computed, onUnmounted, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Pencil, Plus, RefreshCw, Trash2 } from '@lucide/vue'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { ScheduledTask } from '../../bindings/nice_codex_desktop/models'
import { useAppStore } from '@/stores/app'
import { useDialogStore } from '@/stores/dialog'
import { friendlyErrorMessage } from '@/utils/errorMessage'
import { notify } from '@/utils/notify'
import SearchableSelect from '@/components/SearchableSelect.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'

const props = defineProps<{ active: boolean }>()
const appStore = useAppStore()
const dialogStore = useDialogStore()
const { t, locale } = useI18n()
const fieldId = useId()
const tasks = shallowRef<ScheduledTask[]>([])
const loading = shallowRef(false)
const saving = shallowRef(false)
const mutatingId = shallowRef('')
const listError = shallowRef('')
const formError = shallowRef('')
const editingId = shallowRef('')
const title = shallowRef('')
const prompt = shallowRef('')
const workspace = shallowRef(appStore.settings.workspace || '')
const intervalMin = shallowRef<string | number>(60)
const useWorktree = shallowRef(true)
const busy = computed(() => saving.value || Boolean(mutatingId.value))
const workspaceOptions = computed(() => [...new Set([
  workspace.value, appStore.settings.workspace, ...(appStore.settings.recentWorkspaces ?? []),
].filter(Boolean))].map((path) => ({
  value: path,
  label: path.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || path,
  description: path,
})))
const formatter = computed(() => new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }))
let requestSequence = 0
let refreshTimer: ReturnType<typeof setInterval> | undefined
let disposed = false

function formatTime(value: number): string {
  const date = new Date(value * 1000)
  return value > 0 && Number.isFinite(date.getTime()) ? formatter.value.format(date) : t('settings.scheduledNever')
}

function invalidateReads(): void {
  requestSequence++
  loading.value = false
}

async function refresh(): Promise<void> {
  if (!props.active || disposed) return
  const sequence = ++requestSequence
  loading.value = true
  try {
    const result = await backend.ListScheduledTasks()
    if (disposed || sequence !== requestSequence) return
    tasks.value = result ?? []
    listError.value = ''
  } catch (error) {
    if (!disposed && sequence === requestSequence) listError.value = friendlyErrorMessage(error)
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

watch(() => props.active, (active) => {
  clearInterval(refreshTimer)
  invalidateReads()
  if (!active) return
  if (!workspace.value && !editingId.value) workspace.value = appStore.settings.workspace || ''
  void refresh()
  refreshTimer = setInterval(() => {
    if (!busy.value && !loading.value && document.visibilityState !== 'hidden') void refresh()
  }, 30_000)
}, { immediate: true })

onUnmounted(() => {
  disposed = true
  clearInterval(refreshTimer)
  invalidateReads()
})

function resetDraft(): void {
  editingId.value = ''
  title.value = ''
  prompt.value = ''
  workspace.value = appStore.settings.workspace || ''
  intervalMin.value = 60
  useWorktree.value = true
  formError.value = ''
}

async function edit(task: ScheduledTask): Promise<void> {
  if (busy.value || task.activeSessionId) return
  if ((title.value.trim() || prompt.value.trim()) && !await dialogStore.confirm({
    title: t('settings.scheduledReplaceDraft'),
    description: t('settings.scheduledReplaceDraftHint'),
    confirmLabel: t('settings.scheduledEdit'),
  })) return
  if (disposed || busy.value) return
  editingId.value = task.id
  title.value = task.title
  prompt.value = task.prompt
  workspace.value = task.workspace
  intervalMin.value = task.intervalMin
  useWorktree.value = task.useWorktree
  formError.value = ''
  document.getElementById(`${fieldId}-title`)?.focus()
}

async function save(): Promise<void> {
  if (busy.value || !props.active || !appStore.isCodexMode) return
  const minutes = Number(intervalMin.value)
  if (!title.value.trim() || !prompt.value.trim() || !workspace.value
    || !Number.isInteger(minutes) || minutes < 5 || minutes > 10080
    || title.value.length > 200 || prompt.value.length > 32000) {
    formError.value = t('settings.scheduledValidation')
    return
  }
  saving.value = true
  formError.value = ''
  invalidateReads()
  const existing = tasks.value.find((task) => task.id === editingId.value)
  try {
    await backend.SaveScheduledTask({
      id: editingId.value, title: title.value.trim(), prompt: prompt.value.trim(),
      workspace: workspace.value, enabled: existing?.enabled ?? true,
      intervalMin: minutes, useWorktree: useWorktree.value,
      lastRunAt: 0, nextRunAt: 0, createdAt: 0, updatedAt: 0,
    })
    if (disposed) return
    resetDraft()
    notify('success', t('settings.scheduledSaved'))
    await refresh()
  } catch (error) {
    if (!disposed) formError.value = friendlyErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function toggle(task: ScheduledTask, enabled: boolean): Promise<void> {
  if (busy.value) return
  mutatingId.value = task.id
  invalidateReads()
  try {
    await backend.SaveScheduledTask({ ...task, enabled })
    await refresh()
  } catch (error) {
    if (!disposed) listError.value = friendlyErrorMessage(error)
  } finally {
    mutatingId.value = ''
  }
}

async function remove(task: ScheduledTask): Promise<void> {
  if (busy.value || task.activeSessionId) return
  mutatingId.value = task.id
  try {
    if (!await dialogStore.confirm({
      title: t('settings.scheduledDeleteTitle', { title: task.title }),
      description: t('settings.scheduledDeleteHint'),
      confirmLabel: t('common.delete'), destructive: true,
    }) || disposed) return
    invalidateReads()
    await backend.DeleteScheduledTask(task.id)
    if (editingId.value === task.id) resetDraft()
    await refresh()
  } catch (error) {
    if (!disposed) listError.value = friendlyErrorMessage(error)
  } finally {
    mutatingId.value = ''
  }
}
</script>

<template>
  <div v-show="active" class="space-y-5">
    <section class="overflow-hidden rounded-xl border bg-card" :aria-busy="saving">
      <div class="border-b px-4 py-3">
        <h2 class="text-[13px] font-semibold">{{ editingId ? t('settings.scheduledEdit') : t('settings.scheduledTitle') }}</h2>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">{{ t('settings.scheduledLifecycleHint') }}</p>
      </div>
      <fieldset :disabled="busy" class="space-y-4 p-4">
        <div class="space-y-1.5">
          <Label :for="`${fieldId}-title`">{{ t('settings.scheduledTitleLabel') }}</Label>
          <Input :id="`${fieldId}-title`" v-model="title" class="h-9 text-xs" :placeholder="t('settings.scheduledTitlePlaceholder')" maxlength="200" />
        </div>
        <div class="space-y-1.5">
          <Label :for="`${fieldId}-prompt`">{{ t('settings.scheduledPromptLabel') }}</Label>
          <Textarea :id="`${fieldId}-prompt`" v-model="prompt" class="min-h-[100px] resize-y text-xs" :placeholder="t('settings.scheduledPromptPlaceholder')" maxlength="32000" />
        </div>
        <div class="grid gap-4 sm:grid-cols-[minmax(0,1fr)_180px]">
          <div class="min-w-0 space-y-1.5">
            <p class="text-sm font-medium">{{ t('settings.projectInstructionsWorkspace') }}</p>
            <SearchableSelect v-model="workspace" :options="workspaceOptions" :disabled="busy" :aria-label="t('settings.projectInstructionsWorkspace')" :placeholder="t('settings.scheduledWorkspaceRequired')" />
          </div>
          <div class="space-y-1.5">
            <Label :for="`${fieldId}-interval`">{{ t('settings.scheduledInterval') }}</Label>
            <Input :id="`${fieldId}-interval`" v-model="intervalMin" type="number" min="5" max="10080" step="1" class="h-8 text-xs" />
          </div>
        </div>
        <div class="flex items-start gap-3">
          <Switch :checked="useWorktree" :disabled="busy" :aria-label="t('settings.scheduledWorktree')" @update:checked="useWorktree = $event" />
          <div class="space-y-1">
            <p class="text-xs font-medium">{{ t('settings.scheduledWorktree') }}</p>
            <p class="text-[11px] text-muted-foreground">{{ t('settings.scheduledWorktreeHint') }}</p>
          </div>
        </div>
        <p v-if="formError" role="alert" class="text-xs text-destructive">{{ formError }}</p>
        <div class="flex flex-wrap gap-2">
          <Button type="button" size="sm" :disabled="busy || !workspace || !title.trim() || !prompt.trim()" @click="save">
            <Plus v-if="!editingId && !saving" :size="13" class="mr-1" />
            {{ saving ? t('common.saving') : editingId ? t('settings.scheduledSaveAction') : t('settings.scheduledAdd') }}
          </Button>
          <Button v-if="editingId" type="button" variant="outline" size="sm" :disabled="busy" @click="resetDraft">{{ t('common.cancel') }}</Button>
        </div>
      </fieldset>
    </section>

    <section class="overflow-hidden rounded-xl border bg-card" :aria-busy="loading">
      <div class="flex items-center justify-between gap-3 border-b px-4 py-3">
        <h2 class="text-[13px] font-semibold">{{ t('settings.scheduledList') }}</h2>
        <Button type="button" variant="ghost" size="icon-xs" :disabled="loading || busy" :aria-label="t('settings.instructionsReload')" @click="refresh">
          <RefreshCw :size="14" :class="{ 'animate-spin': loading }" />
        </Button>
      </div>
      <div v-if="listError" role="alert" class="space-y-2 border-b bg-destructive/5 px-4 py-3 text-xs text-destructive">
        <p>{{ t('settings.scheduledReadFailed') }}</p>
        <p class="break-words">{{ listError }}</p>
        <Button type="button" variant="outline" size="sm" :disabled="loading || busy" @click="refresh">{{ t('common.retry') }}</Button>
      </div>
      <p v-if="loading && !tasks.length" role="status" class="px-4 py-6 text-center text-xs text-muted-foreground">{{ t('common.loading') }}</p>
      <p v-else-if="!tasks.length && !listError" class="px-4 py-6 text-center text-xs text-muted-foreground">{{ t('settings.scheduledEmpty') }}</p>
      <div v-else class="divide-y">
        <article v-for="task in tasks" :key="task.id" class="space-y-2 px-4 py-4">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0 flex-1 space-y-1">
              <h3 class="break-words text-[13px] font-medium">{{ task.title }}</h3>
              <Badge variant="outline" class="text-[10px]">{{ task.activeSessionId ? t('settings.scheduledRunning') : task.enabled ? t('settings.scheduledEnabled') : t('settings.scheduledPaused') }}</Badge>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <Switch :checked="task.enabled" :disabled="busy" :aria-label="t('settings.scheduledEnableTask', { title: task.title })" @update:checked="toggle(task, $event)" />
              <Button type="button" variant="ghost" size="icon-xs" :disabled="busy || Boolean(task.activeSessionId)" :aria-label="t('settings.scheduledEditTask', { title: task.title })" @click="edit(task)"><Pencil :size="13" /></Button>
              <Button type="button" variant="ghost" size="icon-xs" :disabled="busy || Boolean(task.activeSessionId)" :aria-label="t('settings.scheduledDeleteTitle', { title: task.title })" @click="remove(task)"><Trash2 :size="13" /></Button>
            </div>
          </div>
          <p class="line-clamp-3 break-words text-xs leading-5 text-muted-foreground">{{ task.prompt }}</p>
          <p class="truncate text-[11px] text-muted-foreground" :title="task.workspace">{{ task.workspace }}</p>
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
            <span>{{ t('settings.scheduledMeta', { minutes: task.intervalMin }) }}</span>
            <span>{{ t('settings.scheduledLastRun') }} · {{ formatTime(task.lastRunAt) }}</span>
            <span v-if="task.enabled && !task.activeSessionId">{{ t('settings.scheduledNextRun') }} · {{ formatTime(task.nextRunAt) }}</span>
          </div>
          <p v-if="task.activeSessionId" class="text-[11px] text-muted-foreground">{{ t('settings.scheduledRunningHint') }}</p>
          <p v-if="task.lastError" class="break-words text-xs text-destructive">{{ task.lastError }}</p>
        </article>
      </div>
    </section>
  </div>
</template>
