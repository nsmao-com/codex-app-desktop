<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { RefreshCw } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import { notify } from '@/utils/notify'
import * as backend from '../../bindings/nice_codex_desktop/appservice'

const props = defineProps<{ runtime: WorkspaceRuntime }>()
const app = useAppStore()
const arena = useArenaStore()
const claude = useClaudeStore()
const codex = useCodexStore()
const grok = useGrokStore()
const { t } = useI18n()
const reloadingRuntime = shallowRef<WorkspaceRuntime | ''>('')
const runtimeName = computed(() => app.runtimeDisplayName(reloadingRuntime.value || props.runtime))

async function reload(): Promise<void> {
  if (reloadingRuntime.value) return
  const runtime = props.runtime
  // Capture the target before waiting: switching providers or arena focus must
  // not apply a completed reload to another provider's current conversation.
  const sessions = new Set<string>()
  if (arena.isArenaMode) {
    for (const pane of arena.panes) {
      if (pane.runtime === runtime) sessions.add(arena.sessionForPane(pane.id))
    }
  } else if (runtime === 'claude') sessions.add(claude.activeSessionId)
  else if (runtime === 'grok') sessions.add(grok.activeSessionId)
  else if (codex.activeThreadId && codex.runtimeIDForThread(codex.activeThreadId) === runtime) sessions.add(codex.activeThreadId)
  sessions.delete('')
  const name = app.runtimeDisplayName(runtime)
  reloadingRuntime.value = runtime
  try {
    const result = await backend.ReloadRuntimeConfiguration(runtime)
    const provider = result.configuration.runtime
    const existing = app.agentProviders.findIndex((item) => item.kind === runtime)
    const providers = [...app.agentProviders]
    if (existing >= 0) providers[existing] = provider
    else providers.push(provider)
    app.agentProviders = providers
    if (runtime === 'codex') await codex.acceptReloadedCodexConfiguration(result.codexModels || {})
    else if (runtime === 'claude') await claude.refreshRuntime()
    else if (runtime === 'grok') await grok.refreshRuntime()

    const next = { ...app.settings }
    const model = result.model
    let effort = result.effort
    if (runtime === 'codex') {
      next.model = model
      next.modelProvider = ''
      effort ||= app.models.find((item) => item.model === model)?.defaultReasoningEffort || next.effort
      next.effort = effort
      const selected = app.models.find((item) => item.model === model)
      next.serviceTier = selected?.serviceTiers.some((tier) => tier.id === next.serviceTier) ? next.serviceTier : selected?.defaultServiceTier || ''
    } else if (runtime === 'claude') {
      next.claudeModel = model
      effort ||= next.claudeEffort
    } else if (runtime === 'grok') {
      if (next.grokBackend === 'api') next.grokAPIModel = model
      else next.grokBuildModel = model
      effort ||= next.grokEffort
    } else if (runtime === 'gemini') {
      next.geminiModel = model
      effort ||= next.geminiEffort
    } else {
      next.openCodeModel = model
      next.openCodeProvider = model.includes('/') ? model.slice(0, model.indexOf('/')) : ''
      effort ||= next.openCodeEffort
    }
    await app.savePreferences(next, { silent: true })
    for (const id of sessions) {
      if (runtime === 'claude') claude.patchSessionPreferences(id, model, effort)
      else if (runtime === 'grok') grok.patchSessionPreferences(id, model, effort)
      else {
        const thread = codex.threads.find((item) => item.id === id) || (codex.activeThread?.id === id ? codex.activeThread : undefined)
        const modelProvider = runtime === 'codex' ? result.modelProvider : undefined
        await codex.updateSessionPreferences({ sessionId: id, model, modelProvider, effort, collaborationMode: thread?.collaborationMode || '', resetModel: true })
        codex.patchSessionPreferences(id, model, effort, modelProvider, undefined, true)
      }
    }
    notify('success', t('sidebar.reloadDone', { name }), t('sidebar.reloadDoneHint'))
  } catch (error) {
    notify('error', t('sidebar.reloadFailed', { name }), error instanceof Error ? error.message : String(error))
  } finally {
    reloadingRuntime.value = ''
  }
}
</script>

<template>
  <Button
    type="button"
    variant="ghost"
    class="mb-1 h-8 w-full justify-start gap-2 rounded-lg px-2 text-xs text-muted-foreground"
    :disabled="Boolean(reloadingRuntime)"
    :aria-busy="Boolean(reloadingRuntime)"
    :title="t('sidebar.reloadHint', { name: runtimeName })"
    @click="reload"
  >
    <RefreshCw :size="13" :class="{ 'animate-spin': reloadingRuntime }" aria-hidden="true" />
    <span class="truncate" role="status">{{ reloadingRuntime ? t('sidebar.reloadingConfig', { name: runtimeName }) : t('sidebar.reloadConfig') }}</span>
  </Button>
</template>
