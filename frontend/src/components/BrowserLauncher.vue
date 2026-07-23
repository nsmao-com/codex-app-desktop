<script setup lang="ts">
import {
  ArrowLeft,
  ArrowRight,
  ExternalLink,
  Globe2,
  Maximize2,
  RefreshCw,
  X,
} from '@lucide/vue'
import { Motion } from 'motion-v'
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { panelFromRight } from '@/lib/motion'
import { useBrowserStore } from '@/stores'

const browserStore = useBrowserStore()
const { t } = useI18n()

const props = defineProps<{
  open?: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

const address = shallowRef(browserStore.embeddedBrowserUrl || localStorage.getItem('nice-codex.browserUrl') || 'http://localhost:3000')
const frameKey = shallowRef(0)
const loading = shallowRef(false)
const historyLength = computed(() => Array.isArray(browserStore.browserHistory) ? browserStore.browserHistory.length : 0)
const historyIndex = computed(() => typeof browserStore.browserHistoryIndex === 'number' ? browserStore.browserHistoryIndex : -1)

const quickLinks = computed(() => [
  { label: 'localhost:3000', value: 'http://localhost:3000' },
  { label: 'localhost:5173', value: 'http://localhost:5173' },
  { label: '127.0.0.1:8080', value: 'http://127.0.0.1:8080' },
  { label: t('browser.codexDocs'), value: 'https://developers.openai.com/codex/' },
])

watch(() => browserStore.embeddedBrowserUrl, (value) => {
  if (value) address.value = value
})

async function navigate(): Promise<void> {
  if (!address.value.trim()) return
  loading.value = true
  try {
    if (await browserStore.openBrowser(address.value)) {
      localStorage.setItem('nice-codex.browserUrl', address.value.trim())
      frameKey.value += 1
    }
  } finally {
    loading.value = false
  }
}

function useQuickLink(value: string): void {
  address.value = value
  void navigate()
}

function closeBrowser(): void {
  browserStore.closeBrowser()
  emit('close')
}
</script>

<template>
  <Motion
    as="aside"
    class="flex h-full w-[min(44vw,520px)] shrink-0 flex-col border-l bg-panel"
    :aria-label="t('browser.title')"
    :initial="panelFromRight.initial"
    :animate="panelFromRight.animate"
    :exit="panelFromRight.exit"
    :transition="panelFromRight.transition"
  >
    <header class="flex h-11 shrink-0 items-center gap-2 border-b px-3">
      <div class="flex size-7 items-center justify-center rounded-md border bg-muted text-muted-foreground">
        <Globe2 :size="15" />
      </div>
      <form class="flex min-w-0 flex-1 items-center gap-2" @submit.prevent="navigate">
        <Input
          id="browser-address"
          v-model="address"
          type="text"
          autocomplete="url"
          spellcheck="false"
          class="h-8 rounded-md bg-muted/60 text-xs shadow-none"
          :placeholder="t('browser.placeholder')"
        />
        <Button type="submit" size="sm" class="h-8 shrink-0" :disabled="loading || !address.trim()">
          {{ loading ? t('browser.opening') : t('browser.open') }}
        </Button>
      </form>
      <Button type="button" variant="ghost" size="icon-xs" :aria-label="t('browser.reload')" @click.stop="frameKey += 1">
        <RefreshCw :size="14" />
      </Button>
      <Button type="button" variant="ghost" size="icon-xs" :aria-label="t('common.close')" @click.stop.prevent="closeBrowser">
        <X :size="15" />
      </Button>
    </header>

    <div class="scrollbar-thin flex shrink-0 items-center gap-1 overflow-x-auto border-b px-3 py-1.5">
      <Button
        v-for="link in quickLinks"
        :key="link.value"
        variant="outline"
        size="xs"
        class="h-7 text-[10px]"
        @click="useQuickLink(link.value)"
      >
        {{ link.label }}
      </Button>
      <Button variant="ghost" size="icon-xs" class="ml-auto" :aria-label="t('browser.back')" :disabled="historyIndex <= 0" @click="browserStore.browserBack">
        <ArrowLeft :size="14" />
      </Button>
      <Button variant="ghost" size="icon-xs" :aria-label="t('browser.forward')" :disabled="historyIndex >= historyLength - 1" @click="browserStore.browserForward">
        <ArrowRight :size="14" />
      </Button>
      <Button variant="ghost" size="icon-xs" :aria-label="t('browser.focus')" @click="browserStore.focusBrowser">
        <Maximize2 :size="14" />
      </Button>
    </div>

    <div class="relative min-h-0 flex-1 bg-white">
      <iframe
        v-if="browserStore.embeddedBrowserUrl"
        :key="`${browserStore.embeddedBrowserUrl}:${frameKey}`"
        class="absolute inset-0 size-full border-0 bg-white"
        :src="browserStore.embeddedBrowserUrl"
        :title="t('browser.title')"
        sandbox="allow-forms allow-modals allow-popups allow-same-origin allow-scripts allow-downloads"
        referrerpolicy="no-referrer"
      />
      <div v-else class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-xs text-muted-foreground">
        <ExternalLink :size="24" class="text-muted-foreground/50" />
        <p>{{ t('browser.embeddedEmpty') }}</p>
      </div>
    </div>

    <p class="border-t px-3 py-2 text-[10px] text-muted-foreground">{{ t('browser.frameHint') }}</p>
  </Motion>
</template>
