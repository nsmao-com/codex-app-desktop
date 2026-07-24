<script setup lang="ts">
import { AlertCircle, FolderOpen, Loader2, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAppStore, useCodexStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const { t } = useI18n()

const state = computed(() => codexStore.connection.state)
const isConnecting = computed(() => state.value === 'starting' || state.value === 'initializing')
const isError = computed(() => state.value === 'error')
const isIdleDisconnected = computed(() => state.value === 'disconnected' || state.value === 'stopping')
const needsWorkspace = computed(() => isIdleDisconnected.value && !appStore.settings.workspace)
const canConnect = computed(() => isIdleDisconnected.value && Boolean(appStore.settings.workspace) && appStore.codexAvailable)
const missingCli = computed(() => isIdleDisconnected.value && !appStore.codexAvailable)

// Codex connection banner only — Claude / Grok use their own CLI readiness paths.
const visible = computed(() =>
  appStore.isCodexMode && state.value !== 'ready',
)

const title = computed(() => {
  if (isError.value) return t('app.connectionError')
  if (isConnecting.value) return t('app.connecting')
  if (needsWorkspace.value) return t('app.needWorkspace')
  if (missingCli.value) return t('app.cliMissing')
  if (canConnect.value) return t('app.notConnected')
  return t('app.disconnected')
})

const description = computed(() => {
  if (isConnecting.value || isError.value) {
    return codexStore.connection.message || t('app.connectionHint')
  }
  if (needsWorkspace.value) {
    return appStore.codexAvailable
      ? t('app.needWorkspaceHintReady')
      : t('app.needWorkspaceHint')
  }
  if (missingCli.value) {
    return t('welcome.installHint')
  }
  if (canConnect.value) {
    return t('app.notConnectedHint')
  }
  return codexStore.connection.message || t('app.connectionHint')
})
</script>

<template>
  <div v-if="visible" class="pointer-events-none absolute inset-x-0 top-3 z-50 flex justify-center px-4">
    <Alert
      class="pointer-events-auto w-full max-w-lg rounded-md border bg-card/95 py-2 shadow-lg backdrop-blur"
      :variant="isError || missingCli ? 'destructive' : 'default'"
    >
      <Loader2 v-if="isConnecting" class="size-4 animate-spin" />
      <AlertCircle v-else class="size-4" />
      <AlertTitle class="text-xs">
        {{ title }}
      </AlertTitle>
      <AlertDescription class="flex items-center justify-between gap-3 text-[11px]">
        <span class="line-clamp-2">{{ description }}</span>
        <div class="flex shrink-0 items-center">
          <Button
            v-if="needsWorkspace"
            size="xs"
            variant="outline"
            @click="codexStore.selectProject()"
          >
            <FolderOpen :size="11" class="mr-1" />
            {{ t('welcome.chooseWorkspace') }}
          </Button>
          <Button
            v-else-if="isError || canConnect"
            size="xs"
            variant="outline"
            @click="codexStore.connect()"
          >
            <RefreshCw :size="11" class="mr-1" />
            {{ t('common.reconnect') }}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  </div>
</template>
