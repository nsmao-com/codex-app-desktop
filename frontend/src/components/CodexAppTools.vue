<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { useAppStore } from '@/stores/app'
import { useCapabilitiesStore } from '@/stores/capabilities'
import type { AppView } from '@/types/codex'
import { asRecord, asString } from '@/utils/protocol'
import { friendlyErrorMessage } from '@/utils/errorMessage'
import * as backend from '../../bindings/nice_codex_desktop/appservice'

const props = defineProps<{ app: AppView }>()
const { t } = useI18n()
const appStore = useAppStore()
const capabilitiesStore = useCapabilitiesStore()
const panelID = useId()
const open = ref(false)
const busy = ref(false)
const error = ref('')
type Tool = { name: string; title: string; description: string; enabled: boolean; reason: string; readOnly: boolean }
const tools = shallowRef<Tool[] | null>(null)
const ready = computed(() => appStore.codexAvailable && capabilitiesStore.connectionReady && !capabilitiesStore.capabilityMutation && !capabilitiesStore.capabilitiesLoading)
let revision = 0
let request: ReturnType<typeof backend.ReadAppDetails> | null = null

function close(): void {
  revision++
  request?.cancel()
  request = null
  open.value = false
  busy.value = false
  error.value = ''
  tools.value = null
}

async function read(): Promise<void> {
  if (!ready.value || busy.value) return
  const token = ++revision
  open.value = true
  busy.value = true
  error.value = ''
  tools.value = null
  try {
    request = backend.ReadAppDetails(props.app.id)
    const response = asRecord(await request)
    if (token !== revision) return
    if (!Array.isArray(response.apps)) throw new Error(t('capabilities.appToolsInvalid'))
    const app = response.apps.map(asRecord).find((item) => item.id === props.app.id)
    if (!app) throw new Error(t('capabilities.appToolsMissing'))
    if (app.toolSummaries == null) return
    if (!Array.isArray(app.toolSummaries)) throw new Error(t('capabilities.appToolsInvalid'))
    tools.value = app.toolSummaries.map((value) => {
      const tool = asRecord(value)
      if (!asString(tool.name)) throw new Error(t('capabilities.appToolsInvalid'))
      return { name: asString(tool.name), title: asString(tool.title), description: asString(tool.description), enabled: tool.isEnabled !== false, reason: asString(tool.disabledReason), readOnly: tool.isReadOnly === true }
    })
  } catch (reason) {
    if (token === revision) error.value = friendlyErrorMessage(reason, t('capabilities.appToolsFailed'))
  } finally {
    if (token === revision) { busy.value = false; request = null }
  }
}

watch(() => [props.app, appStore.settings.workspace, ready.value], close, { flush: 'sync' })
onBeforeUnmount(close)
</script>

<template>
  <div class="min-w-0 border-t px-4 py-2">
    <Button variant="ghost" size="sm" class="h-7 text-xs" :disabled="!open && !ready" :aria-expanded="open" :aria-controls="panelID" @click="open ? close() : read()">
      {{ t(open ? 'capabilities.appToolsClose' : 'capabilities.appToolsView') }}
    </Button>
    <div v-if="open" :id="panelID" class="mt-2 min-w-0 space-y-3 pb-2" :aria-busy="busy">
      <p class="text-xs text-muted-foreground">{{ t('capabilities.appToolsHint') }}</p>
      <p v-if="busy" role="status" class="text-xs text-muted-foreground">{{ t('capabilities.appToolsLoading') }}</p>
      <div v-else-if="error" role="alert" class="space-y-2">
        <p class="break-words text-xs text-destructive">{{ error }}</p>
        <p class="text-xs text-muted-foreground">{{ t('capabilities.appToolsFailed') }}</p>
        <Button variant="outline" size="sm" :disabled="!ready" @click="read">{{ t('capabilities.appToolsRetry') }}</Button>
      </div>
      <p v-else-if="tools === null" class="text-xs text-muted-foreground">{{ t('capabilities.appToolsUnknown') }}</p>
      <template v-else>
        <p class="text-xs text-muted-foreground">{{ t('capabilities.appToolsCount', { count: tools.length }) }}</p>
        <div v-if="tools.length" class="max-h-80 space-y-2 overflow-y-auto overscroll-contain">
          <div v-for="(tool, index) in tools" :key="`${tool.name}:${index}`" class="min-w-0 space-y-1 rounded-md border p-3">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <span class="min-w-0 break-all text-xs font-medium">{{ tool.title || tool.name }}</span>
              <Badge v-if="!tool.enabled" variant="secondary">{{ t('capabilities.appToolDisabled') }}</Badge>
              <Badge v-if="tool.readOnly" variant="outline">{{ t('capabilities.appToolReadOnly') }}</Badge>
            </div>
            <p v-if="tool.title" class="break-all font-mono text-[10px] text-muted-foreground">{{ tool.name }}</p>
            <p class="whitespace-pre-wrap break-words text-xs text-muted-foreground">{{ tool.description }}</p>
            <p v-if="tool.reason" class="break-words text-xs text-muted-foreground">{{ tool.reason }}</p>
          </div>
        </div>
        <p v-else class="text-xs text-muted-foreground">{{ t('capabilities.appToolsEmpty') }}</p>
      </template>
    </div>
  </div>
</template>
