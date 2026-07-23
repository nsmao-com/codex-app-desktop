<script setup lang="ts">
import { Download, ExternalLink, LoaderCircle, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()

const open = computed({
  get: () => appStore.updateDialogOpen,
  set: (value: boolean) => {
    appStore.updateDialogOpen = value
  },
})

const info = computed(() => appStore.updateInfo)
const checking = computed(() => appStore.updateChecking)
const error = computed(() => appStore.updateCheckError)

const releaseNotesPreview = computed(() => {
  const notes = info.value?.releaseNotes?.trim() || ''
  if (!notes) return ''
  return notes.length > 480 ? `${notes.slice(0, 480).trimEnd()}…` : notes
})

const statusLabel = computed(() => {
  if (checking.value) return t('updates.checking')
  if (error.value) return t('updates.checkFailed')
  if (info.value?.updateAvailable) return t('updates.available')
  if (info.value) return t('updates.upToDate')
  return t('updates.checkNow')
})

async function onOpenChange(value: boolean): Promise<void> {
  open.value = value
}

async function recheck(): Promise<void> {
  await appStore.openUpdateCheckDialog()
}

async function download(): Promise<void> {
  await appStore.openUpdatePage()
}

async function viewReleases(): Promise<void> {
  await appStore.openReleasesPage()
}
</script>

<template>
  <Dialog :open="open" @update:open="onOpenChange">
    <DialogContent class="sm:max-w-md" :show-close-button="true">
      <DialogHeader>
        <DialogTitle>{{ t('updates.dialogTitle') }}</DialogTitle>
        <DialogDescription>
          {{ t('updates.aboutHint') }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4">
        <div class="rounded-lg border bg-muted/30 px-4 py-3">
          <div class="flex items-start gap-3">
            <LoaderCircle
              v-if="checking"
              :size="18"
              class="mt-0.5 shrink-0 animate-spin text-muted-foreground"
            />
            <div class="min-w-0 flex-1 space-y-1">
              <p class="text-sm font-medium">{{ statusLabel }}</p>
              <p class="text-[12px] text-muted-foreground">
                {{ t('updates.currentVersion') }}
                v{{ info?.currentVersion || appStore.appVersion }}
                <template v-if="info?.latestVersion && !checking">
                  · {{ t('updates.latestVersion') }}
                  v{{ info.latestVersion }}
                </template>
              </p>
              <p v-if="error" class="text-[12px] text-destructive">{{ error }}</p>
              <p
                v-else-if="info?.updateAvailable && !checking"
                class="text-[12px] text-muted-foreground"
              >
                {{ t('updates.availableDialogHint', { version: info.latestVersion }) }}
              </p>
              <p
                v-else-if="info && !info.updateAvailable && !checking"
                class="text-[12px] text-muted-foreground"
              >
                {{ t('updates.upToDateHint') }}
              </p>
            </div>
          </div>
        </div>

        <div
          v-if="releaseNotesPreview && info?.updateAvailable && !checking"
          class="max-h-36 overflow-y-auto rounded-lg border px-3 py-2"
        >
          <p class="mb-1 text-[11px] font-medium text-muted-foreground">{{ t('updates.releaseNotes') }}</p>
          <pre class="whitespace-pre-wrap font-sans text-[12px] leading-5 text-foreground/90">{{ releaseNotesPreview }}</pre>
        </div>
      </div>

      <DialogFooter class="gap-2 sm:justify-between">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          class="justify-start"
          :disabled="checking"
          @click="viewReleases"
        >
          <ExternalLink :size="14" />
          {{ t('updates.viewReleases') }}
        </Button>
        <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button type="button" variant="outline" size="sm" :disabled="checking" @click="recheck">
            <RefreshCw :size="14" :class="checking ? 'animate-spin' : ''" />
            {{ checking ? t('updates.checking') : t('updates.checkAgain') }}
          </Button>
          <Button
            v-if="info?.updateAvailable && !checking"
            type="button"
            size="sm"
            @click="download"
          >
            <Download :size="14" />
            {{ t('updates.download') }}
          </Button>
          <Button
            v-else
            type="button"
            size="sm"
            variant="secondary"
            @click="open = false"
          >
            {{ t('updates.close') }}
          </Button>
        </div>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
