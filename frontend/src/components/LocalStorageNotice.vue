<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { Button } from '@/components/ui/button'

const appStore = useAppStore()
const { t } = useI18n()
const issues = computed(() => appStore.storageHealth.issues ?? [])
const canRetry = computed(() => issues.value.some((issue) => issue.canRetry))
</script>

<template>
  <section v-if="issues.length" class="shrink-0 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-xs" :aria-label="t('storage.title')">
    <div class="flex items-start gap-3">
      <details class="min-w-0 flex-1">
        <summary class="cursor-pointer py-1 font-medium focus-visible:outline-2 focus-visible:outline-ring">
          <span role="status">{{ t('storage.title') }} · {{ issues.length }}</span>
        </summary>
        <ul class="mt-2 max-h-48 space-y-3 overflow-y-auto pr-2">
          <li v-for="issue in issues" :key="issue.key" class="space-y-1">
            <p class="font-medium">{{ t(`storage.files.${issue.key}`) }} · {{ t(issue.operation === 'read' ? 'storage.readFailed' : 'storage.writeFailed') }}</p>
            <p class="break-all font-mono text-[11px]">{{ issue.path }}</p>
            <p class="break-words text-muted-foreground">{{ issue.message }}</p>
            <p class="leading-5">{{ t(issue.operation === 'read' ? 'storage.readHint' : issue.canRetry ? 'storage.writeHint' : 'storage.settingsHint') }}</p>
          </li>
        </ul>
      </details>
      <Button v-if="canRetry" type="button" variant="outline" size="sm" class="shrink-0" :disabled="appStore.storageRetrying" @click="appStore.retryLocalStorageWrites">
        {{ t(appStore.storageRetrying ? 'storage.retrying' : 'storage.retry') }}
      </Button>
    </div>
  </section>
</template>
