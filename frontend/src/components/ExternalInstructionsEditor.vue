<script setup lang="ts">
import { computed, onUnmounted, reactive, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RefreshCw } from '@lucide/vue'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { GlobalInstructionsInfo } from '../../bindings/nice_codex_desktop/models'
import { useDialogStore } from '@/stores/dialog'
import { instructionBytes, instructionError, maxInstructionBytes } from '@/utils/instructions'
import { notify } from '@/utils/notify'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

const props = withDefaults(defineProps<{
  runtime: 'gemini' | 'opencode'
  workspace: string
  active?: boolean
  disabled?: boolean
}>(), { active: true, disabled: false })
type Scope = 'global' | 'project'
interface Draft {
  runtime: 'gemini' | 'opencode'
  workspace: string
  scope: Scope
  info: GlobalInstructionsInfo | null
  content: string
  error: string
  loading: boolean
  readFailed: boolean
  saving: boolean
  sequence: number
}
const { t } = useI18n()
const dialog = useDialogStore()
const fieldId = useId()
const scope = shallowRef<Scope>('global')
const drafts = new Map<string, Draft>()
const current = shallowRef<Draft | null>(null)
const size = computed(() => instructionBytes(current.value?.content ?? ''))
const busy = computed(() => props.disabled || Boolean(current.value?.loading || current.value?.saving))
const changed = computed(() => current.value?.info && current.value.content !== current.value.info.content)
let disposed = false

watch(() => [props.runtime, props.workspace, props.active, scope.value] as const, () => {
  if (!props.active) return
  const workspace = scope.value === 'project' ? props.workspace : ''
  const key = JSON.stringify([props.runtime, scope.value, workspace])
  let entry = drafts.get(key)
  if (!entry) {
    entry = reactive<Draft>({ runtime: props.runtime, workspace, scope: scope.value, info: null,
      content: '', error: '', loading: false, readFailed: false, saving: false, sequence: 0 })
    drafts.set(key, entry)
  }
  current.value = entry
  if (!entry.info && !entry.loading && !entry.error) void load(entry)
}, { immediate: true })

onUnmounted(() => { disposed = true })

async function load(entry: Draft): Promise<void> {
  if (entry.scope === 'project' && !entry.workspace) {
    entry.error = t('settings.projectInstructionsUnavailable')
    return
  }
  const sequence = ++entry.sequence
  entry.loading = true
  entry.error = ''
  try {
    const info = await backend.ReadExternalRuntimeInstructions(entry.runtime, entry.scope, entry.workspace)
    if (disposed || sequence !== entry.sequence) return
    entry.info = info
    entry.readFailed = !info.available || !info.revision
    if (!info.available || !info.revision) {
      entry.error = info.readError ? instructionError(info.readError) : t('settings.instructionsLoadFailed')
      return
    }
    entry.content = info.content
  } catch (error) {
    if (!disposed && sequence === entry.sequence) { entry.readFailed = true; entry.error = instructionError(error) }
  } finally {
    if (sequence === entry.sequence) entry.loading = false
  }
}

async function reload(): Promise<void> {
  const entry = current.value
  if (!entry || busy.value) return
  if (entry.info && entry.content !== entry.info.content && !await dialog.confirm({
    title: t('settings.instructionsReload'), description: t('settings.instructionsReloadConfirm'),
    confirmLabel: t('settings.instructionsReload'),
  })) return
  if (!disposed && current.value === entry && !busy.value) await load(entry)
}

async function save(): Promise<void> {
  const entry = current.value
  if (!entry?.info?.available || entry.readFailed || !entry.info.revision || busy.value || !props.active || !changed.value) return
  if (size.value > maxInstructionBytes) { entry.error = t('settings.instructionsTooLarge'); return }
  entry.saving = true
  entry.error = ''
  const content = entry.content
  try {
    const info = await backend.SaveExternalRuntimeInstructions({
      runtime: entry.runtime, workspace: entry.workspace, scope: entry.scope, content, revision: entry.info.revision,
    })
    if (disposed) return
    entry.info = info
    if (entry.content === content) entry.content = info.content
    if (current.value === entry && props.active) notify('success', t('settings.externalInstructionsSaved'))
  } catch (error) {
    if (!disposed) entry.error = instructionError(error)
  } finally {
    entry.saving = false
  }
}
</script>

<template>
  <section v-show="active" class="space-y-3 rounded-xl border bg-card p-4" :aria-busy="busy">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-[13px] font-semibold">{{ t('settings.externalInstructionsTitle') }}</h2>
      <Button type="button" size="sm" variant="ghost" :disabled="busy" @click="reload">
        <RefreshCw :size="12" class="mr-1" :class="current?.loading ? 'animate-spin' : ''" />
        {{ t('settings.instructionsReload') }}
      </Button>
    </div>
    <div class="grid grid-cols-2 gap-1 rounded-md border bg-muted/40 p-1" :aria-label="t('settings.externalInstructionsTitle')">
      <Button type="button" size="sm" :aria-pressed="scope === 'global'" :variant="scope === 'global' ? 'secondary' : 'ghost'" :disabled="current?.saving || disabled" @click="scope = 'global'">{{ t('settings.instructionsGlobal') }}</Button>
      <Button type="button" size="sm" :aria-pressed="scope === 'project'" :variant="scope === 'project' ? 'secondary' : 'ghost'" :disabled="current?.saving || disabled" @click="scope = 'project'">{{ t('settings.instructionsProject') }}</Button>
    </div>
    <p v-if="current?.info?.path" class="break-all font-mono text-[10px] text-muted-foreground">{{ current.info.path }}</p>
    <label :for="fieldId" class="sr-only">{{ t(scope === 'global' ? 'settings.instructionsGlobal' : 'settings.instructionsProject') }}</label>
    <Textarea v-if="current" :id="fieldId" v-model="current.content" :disabled="busy || !current.info?.available || current.readFailed" class="min-h-[180px] resize-y font-mono text-xs leading-5" spellcheck="false" :aria-invalid="size > maxInstructionBytes" />
    <div class="flex flex-wrap items-center justify-between gap-2 text-[10px] text-muted-foreground">
      <span>{{ t('settings.instructionsSize', { size: (size / 1024).toFixed(1) }) }}</span>
      <span v-if="current?.loading" role="status">{{ t('common.loading') }}</span>
      <span v-else-if="changed">{{ t('settings.instructionsUnsaved') }}</span>
    </div>
    <p class="text-[10px] leading-5 text-muted-foreground">{{ t('settings.instructionsClearHint') }}</p>
    <p v-if="current?.error || size > maxInstructionBytes" role="alert" class="text-xs leading-5 text-destructive">{{ size > maxInstructionBytes ? t('settings.instructionsTooLarge') : current?.error }}</p>
    <div class="flex justify-end">
      <Button type="button" size="sm" :disabled="busy || !current?.info?.available || current.readFailed || !current.info.revision || !changed || size > maxInstructionBytes" @click="save">{{ current?.saving ? t('common.saving') : t('settings.saveNativeInstructions') }}</Button>
    </div>
  </section>
</template>
