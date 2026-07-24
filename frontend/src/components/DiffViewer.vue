<script setup lang="ts">
import { Columns2, FileCode2, Rows3 } from '@lucide/vue'
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { DiffLineView } from '@/types/codex'
import { parseUnifiedDiff } from '@/utils/diff'

const props = defineProps<{
  diff: string
}>()

interface SplitRow {
  left: DiffLineView | null
  right: DiffLineView | null
}

const { t } = useI18n()
const mode = shallowRef<'unified' | 'split'>('unified')
const selectedIndex = shallowRef(0)
const lineLimit = shallowRef(600)
const truncated = computed(() => props.diff.length > 300_000)
const files = computed(() => parseUnifiedDiff(props.diff.slice(0, 300_000)))
const selectedFile = computed(() => files.value[selectedIndex.value] ?? files.value[0] ?? null)
const splitRowsCache = new WeakMap<DiffLineView[], SplitRow[]>()

watch(files, (next) => {
  if (selectedIndex.value >= next.length) selectedIndex.value = 0
  lineLimit.value = 600
})

watch(selectedIndex, () => {
  lineLimit.value = 600
})

function splitRows(lines: DiffLineView[]): SplitRow[] {
  const cached = splitRowsCache.get(lines)
  if (cached) return cached
  const rows: SplitRow[] = []
  let index = 0
  while (index < lines.length) {
    const line = lines[index]
    if (!line) break
    if (line.kind !== 'delete' && line.kind !== 'add') {
      rows.push({ left: line, right: line })
      index += 1
      continue
    }
    const deletes: DiffLineView[] = []
    const additions: DiffLineView[] = []
    while (lines[index]?.kind === 'delete') {
      deletes.push(lines[index]!)
      index += 1
    }
    while (lines[index]?.kind === 'add') {
      additions.push(lines[index]!)
      index += 1
    }
    const count = Math.max(deletes.length, additions.length)
    for (let pair = 0; pair < count; pair += 1) {
      rows.push({ left: deletes[pair] ?? null, right: additions[pair] ?? null })
    }
  }
  splitRowsCache.set(lines, rows)
  return rows
}

const selectedHunkViews = computed(() => {
  const file = selectedFile.value
  if (!file) return []
  let remaining = lineLimit.value
  return file.hunks.map((hunk) => {
    if (remaining <= 0) {
      return { hunk, unifiedLines: [] as DiffLineView[], split: [] as SplitRow[], hidden: hunk.lines.length }
    }
    const take = Math.min(remaining, hunk.lines.length)
    remaining -= take
    const unifiedLines = hunk.lines.slice(0, take)
    const split = splitRows(hunk.lines).slice(0, take)
    return {
      hunk,
      unifiedLines,
      split,
      hidden: Math.max(0, hunk.lines.length - take),
    }
  })
})

const hasMoreLines = computed(() =>
  selectedHunkViews.value.some((view) => view.hidden > 0)
    || (selectedFile.value?.hunks.reduce((sum, hunk) => sum + hunk.lines.length, 0) ?? 0) > lineLimit.value,
)

function showMoreLines(): void {
  lineLimit.value += 800
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border bg-card">
    <header class="flex h-9 shrink-0 items-center gap-2 border-b px-2">
      <h3 class="flex min-w-0 flex-1 items-center gap-2 text-xs font-semibold">
        <FileCode2 :size="14" class="text-primary" />
        <span class="truncate">{{ selectedFile?.displayPath || t('inspector.liveDiff') }}</span>
        <Badge v-if="selectedFile" variant="outline" class="text-[9px]">
          <span class="text-green-600">+{{ selectedFile.additions }}</span>
          <span class="mx-1">/</span>
          <span class="text-red-600">-{{ selectedFile.deletions }}</span>
        </Badge>
      </h3>
      <div class="flex shrink-0 gap-1">
        <Button variant="ghost" size="icon-xs" :class="mode === 'unified' ? 'bg-accent text-accent-foreground' : ''" :aria-label="t('diff.unified')" @click="mode = 'unified'">
          <Rows3 :size="13" />
        </Button>
        <Button variant="ghost" size="icon-xs" :class="mode === 'split' ? 'bg-accent text-accent-foreground' : ''" :aria-label="t('diff.split')" @click="mode = 'split'">
          <Columns2 :size="13" />
        </Button>
      </div>
    </header>

    <div v-if="files.length > 1" class="flex gap-1 overflow-x-auto border-b px-2 py-1.5">
      <Button
        v-for="(file, index) in files"
        :key="`${file.displayPath}:${index}`"
        variant="ghost"
        size="xs"
        class="h-6 max-w-44 shrink-0 justify-start gap-1.5 px-2 text-[10px]"
        :class="selectedIndex === index ? 'bg-accent text-accent-foreground' : ''"
        :title="file.displayPath"
        @click="selectedIndex = index"
      >
        <span class="truncate">{{ file.displayPath }}</span>
        <span class="text-green-600">+{{ file.additions }}</span>
        <span class="text-red-600">-{{ file.deletions }}</span>
      </Button>
    </div>

    <!-- Native overflow so long diffs always get a real vertical/horizontal scrollbar
         (reka ScrollArea needs a fixed height and often hides the thumb). -->
    <div class="scrollbar-thin min-h-0 flex-1 overflow-auto overscroll-contain">
      <div v-if="selectedFile" class="min-w-max font-mono text-[10px] leading-relaxed">
        <section v-for="view in selectedHunkViews" :key="view.hunk.header" class="border-b last:border-0">
          <header class="sticky top-0 z-[1] bg-muted px-3 py-1 text-[9px] text-muted-foreground">{{ view.hunk.header }}</header>

          <template v-if="mode === 'unified'">
            <div
              v-for="(line, index) in view.unifiedLines"
              :key="index"
              class="grid grid-cols-[40px_40px_minmax(420px,1fr)]"
              :class="{
                'bg-green-500/10': line.kind === 'add',
                'bg-red-500/10': line.kind === 'delete',
              }"
            >
              <span class="select-none border-r px-1 text-right text-muted-foreground">{{ line.oldLine ?? '' }}</span>
              <span class="select-none border-r px-1 text-right text-muted-foreground">{{ line.newLine ?? '' }}</span>
              <code class="whitespace-pre px-2">
                <span class="mr-1 text-muted-foreground">{{ line.kind === 'add' ? '+' : line.kind === 'delete' ? '-' : ' ' }}</span>
                {{ line.content }}
              </code>
            </div>
          </template>

          <template v-else>
            <div
              v-for="(row, index) in view.split"
              :key="index"
              class="grid min-w-[760px] grid-cols-2 border-b last:border-0"
            >
              <div class="grid grid-cols-[40px_minmax(320px,1fr)]" :class="{ 'bg-red-500/10': row.left?.kind === 'delete' }">
                <span class="select-none border-r px-1 text-right text-muted-foreground">{{ row.left?.oldLine ?? '' }}</span>
                <code class="whitespace-pre px-2">{{ row.left?.content ?? '' }}</code>
              </div>
              <div class="grid grid-cols-[40px_minmax(320px,1fr)] border-l" :class="{ 'bg-green-500/10': row.right?.kind === 'add' }">
                <span class="select-none border-r px-1 text-right text-muted-foreground">{{ row.right?.newLine ?? '' }}</span>
                <code class="whitespace-pre px-2">{{ row.right?.content ?? '' }}</code>
              </div>
            </div>
          </template>
        </section>
        <div v-if="hasMoreLines" class="border-t px-3 py-2">
          <Button variant="ghost" size="sm" class="h-7 px-2 text-[11px]" @click="showMoreLines">
            {{ t('timeline.showMoreCode') }}
          </Button>
        </div>
      </div>
      <div v-else class="grid h-full min-h-32 place-items-center text-xs text-muted-foreground">
        {{ t('inspector.noFileDiff') }}
      </div>
    </div>

    <p v-if="truncated" class="border-t px-3 py-1.5 text-[10px] text-muted-foreground">{{ t('inspector.truncated') }}</p>
  </section>
</template>
