<script setup lang="ts">
import { computed, nextTick, shallowRef, watch } from 'vue'
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
import { Input } from '@/components/ui/input'
import { useDialogStore } from '@/stores/dialog'

const { t } = useI18n()
const dialogStore = useDialogStore()

const draft = shallowRef('')

const open = computed(() => dialogStore.request !== null)
const isPrompt = computed(() => dialogStore.request?.kind === 'prompt')
const title = computed(() => dialogStore.request?.title || '')
const description = computed(() => dialogStore.request?.description || '')
const confirmLabel = computed(() =>
  dialogStore.request?.confirmLabel
  || (dialogStore.request?.kind === 'confirm' && dialogStore.request.destructive
    ? t('common.delete')
    : t('common.confirm')),
)
const cancelLabel = computed(() => dialogStore.request?.cancelLabel || t('common.cancel'))
const destructive = computed(() => dialogStore.request?.kind === 'confirm' && Boolean(dialogStore.request.destructive))
const placeholder = computed(() =>
  dialogStore.request?.kind === 'prompt' ? (dialogStore.request.placeholder || '') : '',
)
const maxlength = computed(() =>
  dialogStore.request?.kind === 'prompt' ? (dialogStore.request.maxlength || 200) : 200,
)
const canConfirm = computed(() => {
  if (!isPrompt.value) return true
  return draft.value.trim().length > 0
})

watch(
  () => dialogStore.request,
  async (value) => {
    if (!value) {
      draft.value = ''
      return
    }
    draft.value = value.kind === 'prompt' ? (value.defaultValue || '') : ''
    if (value.kind === 'prompt') {
      await nextTick()
      const el = document.querySelector<HTMLInputElement>('[data-app-prompt-input]')
      el?.focus()
      el?.select()
    }
  },
)

function onOpenChange(value: boolean): void {
  if (!value) dialogStore.cancel()
}

function onCancel(): void {
  dialogStore.cancel()
}

function onConfirm(): void {
  if (!canConfirm.value) return
  dialogStore.accept(draft.value.trim())
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.isComposing) return
  event.preventDefault()
  onConfirm()
}
</script>

<template>
  <Dialog :open="open" @update:open="onOpenChange">
    <DialogContent class="gap-0 overflow-hidden p-0 sm:max-w-md" :show-close-button="false">
      <DialogHeader class="border-b px-4 py-3">
        <DialogTitle class="text-[14px]">{{ title }}</DialogTitle>
        <DialogDescription v-if="description" class="text-[12px] leading-5">
          {{ description }}
        </DialogDescription>
      </DialogHeader>

      <div v-if="isPrompt" class="px-4 py-3">
        <Input
          v-model="draft"
          data-app-prompt-input
          class="h-9 text-sm"
          :placeholder="placeholder"
          :maxlength="maxlength"
          @keydown="onKeydown"
        />
      </div>

      <DialogFooter class="border-t px-4 py-3">
        <Button type="button" variant="outline" size="sm" class="h-8 text-xs" @click="onCancel">
          {{ cancelLabel }}
        </Button>
        <Button
          type="button"
          size="sm"
          class="h-8 text-xs"
          :variant="destructive ? 'destructive' : 'default'"
          :disabled="!canConfirm"
          @click="onConfirm"
        >
          {{ confirmLabel }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
