<script setup lang="ts">
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import { SquareTerminal, Trash2, X } from '@lucide/vue'
import { Motion } from 'motion-v'
import { nextTick, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { panelFromRight } from '@/lib/motion'
import { useAppStore, useTerminalStore } from '@/stores'

import '@xterm/xterm/css/xterm.css'

const terminalStore = useTerminalStore()
const appStore = useAppStore()
const { t } = useI18n()

const hostRef = useTemplateRef<HTMLElement>('hostRef')
const termRef = shallowRef<Terminal | null>(null)
const fitRef = shallowRef<FitAddon | null>(null)

let resizeObserver: ResizeObserver | null = null
let resizeTimer = 0
let dataDisposable: { dispose: () => void } | null = null
let applyingRemote = false

function disposeTerminal(): void {
  if (resizeTimer) {
    window.clearTimeout(resizeTimer)
    resizeTimer = 0
  }
  resizeObserver?.disconnect()
  resizeObserver = null
  dataDisposable?.dispose()
  dataDisposable = null
  termRef.value?.dispose()
  termRef.value = null
  fitRef.value = null
}

function scheduleFit(): void {
  if (resizeTimer) window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(() => {
    resizeTimer = 0
    const term = termRef.value
    const fit = fitRef.value
    if (!term || !fit || !hostRef.value) return
    try {
      fit.fit()
    } catch {
      return
    }
    if (terminalStore.terminalRunning) {
      void terminalStore.resizeTerminal(term.rows, term.cols)
    }
  }, 60)
}

async function mountTerminal(): Promise<void> {
  await nextTick()
  const host = hostRef.value
  if (!host || termRef.value) return

  const term = new Terminal({
    cursorBlink: true,
    convertEol: false,
    fontFamily: '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    fontSize: 12,
    lineHeight: 1.35,
    theme: {
      background: '#181817',
      foreground: '#e8e7e3',
      cursor: '#e8e7e3',
      selectionBackground: '#3a3935',
    },
  })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host)
  fit.fit()
  term.focus()

  dataDisposable = term.onData((data) => {
    if (applyingRemote || !terminalStore.terminalRunning) return
    void terminalStore.writeTerminal(data)
  })

  resizeObserver = new ResizeObserver(() => scheduleFit())
  resizeObserver.observe(host)

  termRef.value = term
  fitRef.value = fit
  terminalStore.bindTerminalWriter((chunk) => {
    applyingRemote = true
    try {
      term.write(chunk)
    } finally {
      applyingRemote = false
    }
  })
  if (terminalStore.terminalRunning) {
    void terminalStore.resizeTerminal(term.rows, term.cols)
  }
}

watch(
  () => terminalStore.terminalPanelOpen,
  async (open) => {
    if (open) {
      await mountTerminal()
      return
    }
    terminalStore.bindTerminalWriter(null)
    disposeTerminal()
  },
  { immediate: true },
)

watch(
  () => terminalStore.terminalRunning,
  (running) => {
    const term = termRef.value
    if (!term) return
    if (running) {
      term.focus()
      void terminalStore.resizeTerminal(term.rows, term.cols)
      return
    }
    term.writeln('')
    term.writeln(`\x1b[90m${t('terminal.exited')}\x1b[0m`)
  },
)

onBeforeUnmount(() => {
  terminalStore.bindTerminalWriter(null)
  disposeTerminal()
})

function clearTerminal(): void {
  termRef.value?.clear()
  terminalStore.clearTerminal()
}

function onCloseTerminal(): void {
  void terminalStore.closeTerminal()
}

function focusTerminal(): void {
  termRef.value?.focus()
}
</script>

<template>
  <Motion
    as="aside"
    class="flex h-full w-[min(42vw,480px)] shrink-0 flex-col border-l bg-panel"
    :aria-label="t('terminal.title')"
    :initial="panelFromRight.initial"
    :animate="panelFromRight.animate"
    :exit="panelFromRight.exit"
    :transition="panelFromRight.transition"
  >
    <header class="flex h-11 shrink-0 items-center gap-2 border-b px-3">
      <div class="flex size-7 items-center justify-center rounded-md bg-foreground text-background">
        <SquareTerminal :size="15" />
      </div>
      <div class="min-w-0 flex-1">
        <h2 class="text-xs font-semibold">{{ t('terminal.title') }}</h2>
        <p class="truncate text-[10px] text-muted-foreground">
          {{ appStore.settings.terminalProfile }} · {{ terminalStore.terminalStarting ? t('terminal.starting') : terminalStore.terminalRunning ? t('terminal.running') : t('terminal.exited') }}
        </p>
      </div>
      <Button type="button" variant="ghost" size="icon-xs" :aria-label="t('terminal.clear')" :title="t('terminal.clear')" @click.stop="clearTerminal">
        <Trash2 :size="14" />
      </Button>
      <Button type="button" variant="ghost" size="icon-xs" :aria-label="t('terminal.close')" :title="t('terminal.close')" @click.stop.prevent="onCloseTerminal">
        <X :size="15" />
      </Button>
    </header>

    <div class="relative min-h-0 flex-1 bg-[#181817] p-2" @click="focusTerminal">
      <div ref="hostRef" class="h-full w-full overflow-hidden" />
      <p
        v-if="terminalStore.terminalStarting && !terminalStore.terminalRunning"
        class="pointer-events-none absolute inset-0 flex items-center justify-center font-mono text-[11px] text-[#9c9a93]"
      >
        {{ t('terminal.starting') }}
      </p>
    </div>
  </Motion>
</template>
