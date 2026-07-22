<script setup lang="ts">
import { AlertCircle, Loader2, RefreshCw } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useCodexStore } from '@/stores'

const codexStore = useCodexStore()
const { t } = useI18n()
</script>

<template>
  <div v-if="codexStore.connection.state !== 'ready'" class="pointer-events-none absolute inset-x-0 top-3 z-50 flex justify-center px-4">
    <Alert
      class="pointer-events-auto w-full max-w-lg rounded-md border bg-card/95 py-2 shadow-lg backdrop-blur"
      :variant="codexStore.connection.state === 'error' ? 'destructive' : 'default'"
    >
      <Loader2 v-if="codexStore.connection.state === 'starting' || codexStore.connection.state === 'initializing'" class="size-4 animate-spin" />
      <AlertCircle v-else class="size-4" />
      <AlertTitle class="text-xs">
        {{ codexStore.connection.state === 'error' ? t('app.connectionError') : t('app.connecting') }}
      </AlertTitle>
      <AlertDescription class="flex items-center justify-between gap-3 text-[11px]">
        <span class="line-clamp-2">{{ codexStore.connection.message }}</span>
        <Button
          v-if="codexStore.connection.state === 'error'"
          size="xs"
          variant="outline"
          @click="codexStore.connect()"
        >
          <RefreshCw :size="11" class="mr-1" />
          {{ t('common.reconnect') }}
        </Button>
      </AlertDescription>
    </Alert>
  </div>
</template>
