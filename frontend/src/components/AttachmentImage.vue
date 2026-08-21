<script setup lang="ts">
import { ImageOff, RefreshCw } from '@lucide/vue'
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { forgetImagePreview, resolveImagePreview } from '@/utils/imagePreview'

const props = withDefaults(defineProps<{
  source: string
  kind?: 'local' | 'remote' | 'preview'
  alt?: string
  imageClass?: string
}>(), {
  kind: 'local',
  alt: '',
  imageClass: '',
})

const { t } = useI18n()

const preview = shallowRef('')
const state = shallowRef<'loading' | 'loaded' | 'failed'>('loading')
const dialogOpen = shallowRef(false)
let generation = 0

function safeRemoteURL(source: string): string {
  const inline = safePreviewURL(source)
  if (inline) return inline
  try {
    const url = new URL(source)
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.toString() : ''
  } catch {
    return ''
  }
}

function safePreviewURL(source: string): string {
  if (source.startsWith('blob:')) return source
  return /^data:image\/(?:png|jpeg|webp|gif);base64,/i.test(source) ? source : ''
}

async function load(force = false): Promise<void> {
  const current = ++generation
  state.value = 'loading'
  if (force && props.kind === 'local') forgetImagePreview(props.source)
  const value = props.kind === 'remote'
    ? safeRemoteURL(props.source)
    : props.kind === 'preview'
      ? safePreviewURL(props.source)
      : await resolveImagePreview(props.source)
  if (current !== generation) return
  preview.value = value
  state.value = value ? 'loaded' : 'failed'
}

function failed(): void {
  state.value = 'failed'
}

const label = computed(() => props.alt || props.source.split(/[\\/]/).filter(Boolean).at(-1) || 'image')

watch(() => [props.source, props.kind] as const, () => { void load() }, { immediate: true })
onBeforeUnmount(() => { generation += 1 })
</script>

<template>
  <div class="relative max-w-full overflow-hidden rounded-lg border border-border/60 bg-muted/30">
    <button
      v-if="state === 'loaded' && preview"
      type="button"
      class="block max-w-full cursor-zoom-in"
      :aria-label="t('chat.openImagePreview', { name: label })"
      @click="dialogOpen = true"
    >
      <img
        :src="preview"
        :alt="label"
        loading="lazy"
        decoding="async"
        :class="imageClass"
        @error="failed"
      >
    </button>
    <div v-else class="grid min-h-24 min-w-32 place-items-center p-3 text-center text-xs text-muted-foreground">
      <div v-if="state === 'loading'" class="size-5 animate-pulse rounded-full bg-muted-foreground/25" />
      <div v-else class="space-y-2">
        <ImageOff :size="20" class="mx-auto" />
        <p class="max-w-40 truncate">{{ label }}</p>
        <Button variant="outline" size="sm" class="h-7 text-[11px]" @click="void load(true)">
          <RefreshCw :size="12" class="mr-1.5" />
          {{ t('common.retry') }}
        </Button>
      </div>
    </div>
  </div>

  <Dialog v-model:open="dialogOpen">
    <DialogContent class="flex max-h-[92vh] max-w-[94vw] flex-col p-3 sm:max-w-[90vw]">
      <DialogTitle class="sr-only">{{ label }}</DialogTitle>
      <DialogDescription class="sr-only">{{ t('chat.imagePreview') }}</DialogDescription>
      <div class="min-h-0 flex-1 overflow-auto rounded-lg bg-black/5 p-2 dark:bg-black/30">
        <img :src="preview" :alt="label" class="mx-auto max-h-[85vh] max-w-full object-contain" @error="failed">
      </div>
    </DialogContent>
  </Dialog>
</template>
