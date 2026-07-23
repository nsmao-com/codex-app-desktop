<script setup lang="ts">
import {
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Circle,
  CircleDot,
  Copy,
  FileDiff,
  ListTodo,
  LoaderCircle,
  Pencil,
  Plug,
  RefreshCcw,
  RotateCcw,
  Terminal,
} from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useCodexStore } from '@/stores'
import type { TimelineItem, TurnMetrics } from '@/types/codex'
import { extractFileDiff, parseUnifiedDiff } from '@/utils/diff'
import { formatToolPayload, renderToolPayloadHTML } from '@/utils/formatPayload'
import { renderMarkdown } from '@/utils/markdown'
import { resolveImagePreview } from '@/utils/imagePreview'

const props = defineProps<{
  kind: 'user' | 'agent'
  items: TimelineItem[]
  metrics?: TurnMetrics | null
  animated?: boolean
  streaming?: boolean
  turnDiff?: string
}>()

const emit = defineEmits<{
  retry: [itemID: string]
  rollback: [payload: { turnId: string; mode: 'single' | 'fromHere' }]
  'inspect-diff': [payload: { path: string; diff: string }]
}>()

const { t } = useI18n()
const codexStore = useCodexStore()
const openRows = shallowRef<Record<string, boolean>>({})
const copiedKey = shallowRef('')
const attachmentPreviews = shallowRef<Record<string, string>>({})
let stepEnterBatch = 0
let stepEnterBatchAt = 0
const stepAnimCleanups = new Set<() => void>()

onBeforeUnmount(() => {
  for (const cleanup of stepAnimCleanups) cleanup()
  stepAnimCleanups.clear()
})

watch(
  () => props.items[0]?.attachments?.map((item) => item.source).join('\0') ?? '',
  (signature) => {
    if (props.kind !== 'user' || !signature) return
    for (const attachment of props.items[0]?.attachments ?? []) {
      if (attachmentPreviews.value[attachment.source]) continue
      void resolveImagePreview(attachment.source).then((url) => {
        if (!url) return
        attachmentPreviews.value = { ...attachmentPreviews.value, [attachment.source]: url }
      })
    }
  },
  { immediate: true },
)

function attachmentPreview(path: string): string {
  return attachmentPreviews.value[path] || ''
}

function markdownHTML(source: string): string {
  return renderMarkdown(source, t('timeline.copyMessage'), t('timeline.showMoreCode'))
}

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function nextStepDelay(): number {
  const now = performance.now()
  if (now - stepEnterBatchAt > 90) {
    stepEnterBatch = 0
    stepEnterBatchAt = now
  }
  const delay = Math.min(stepEnterBatch * 55, 220)
  stepEnterBatch += 1
  return delay
}

function clearStepInlineStyles(el: HTMLElement): void {
  el.style.height = ''
  el.style.opacity = ''
  el.style.transform = ''
  el.style.filter = ''
  el.style.overflow = ''
  el.style.marginTop = ''
  el.style.marginBottom = ''
  el.style.paddingTop = ''
  el.style.paddingBottom = ''
  el.style.transition = ''
  el.style.position = ''
  el.style.width = ''
  el.style.pointerEvents = ''
}

function onStepBeforeEnter(el: Element): void {
  if (!props.streaming || prefersReducedMotion()) return
  const html = el as HTMLElement
  html.style.opacity = '0'
  html.style.transform = 'translateY(14px) scale(0.985)'
  html.style.filter = 'blur(4px)'
  html.style.overflow = 'hidden'
  html.style.height = '0px'
  html.style.marginTop = '0px'
  html.style.marginBottom = '0px'
  html.style.willChange = 'height, opacity, transform, filter'
}

function onStepEnter(el: Element, done: () => void): void {
  if (!props.streaming || prefersReducedMotion()) {
    done()
    return
  }
  const html = el as HTMLElement
  const delay = nextStepDelay()
  let finished = false
  let timer = 0

  const finish = (): void => {
    if (finished) return
    finished = true
    html.removeEventListener('transitionend', onEnd)
    if (timer) window.clearTimeout(timer)
    stepAnimCleanups.delete(finish)
    html.style.willChange = ''
    clearStepInlineStyles(html)
    done()
  }
  stepAnimCleanups.add(finish)

  const onEnd = (event: TransitionEvent): void => {
    if (event.target !== html) return
    if (event.propertyName !== 'height' && event.propertyName !== 'opacity') return
    finish()
  }

  void nextTick(() => {
    requestAnimationFrame(() => {
      const target = Math.max(html.scrollHeight, 1)
      html.style.transition = [
        `height 480ms cubic-bezier(0.22, 1, 0.36, 1) ${delay}ms`,
        `opacity 420ms cubic-bezier(0.22, 1, 0.36, 1) ${delay}ms`,
        `transform 480ms cubic-bezier(0.22, 1, 0.36, 1) ${delay}ms`,
        `filter 420ms ease ${delay}ms`,
        `margin-bottom 480ms cubic-bezier(0.22, 1, 0.36, 1) ${delay}ms`,
      ].join(', ')
      html.style.height = `${target}px`
      html.style.opacity = '1'
      html.style.transform = 'translateY(0) scale(1)'
      html.style.filter = 'blur(0)'
      html.style.marginBottom = ''
      html.addEventListener('transitionend', onEnd)
      timer = window.setTimeout(finish, delay + 700)
    })
  })
}

function onStepAfterEnter(el: Element): void {
  clearStepInlineStyles(el as HTMLElement)
}

function onStepBeforeLeave(el: Element): void {
  if (!props.streaming || prefersReducedMotion()) return
  const html = el as HTMLElement
  html.style.overflow = 'hidden'
  html.style.height = `${html.scrollHeight}px`
  html.style.opacity = '1'
  html.style.position = 'relative'
}

function onStepLeave(el: Element, done: () => void): void {
  if (!props.streaming || prefersReducedMotion()) {
    done()
    return
  }
  const html = el as HTMLElement
  let finished = false
  let timer = 0
  const finish = (): void => {
    if (finished) return
    finished = true
    html.removeEventListener('transitionend', onEnd)
    if (timer) window.clearTimeout(timer)
    stepAnimCleanups.delete(finish)
    done()
  }
  stepAnimCleanups.add(finish)
  const onEnd = (event: TransitionEvent): void => {
    if (event.target !== html) return
    if (event.propertyName !== 'height' && event.propertyName !== 'opacity') return
    finish()
  }
  requestAnimationFrame(() => {
    html.style.transition = [
      'height 320ms cubic-bezier(0.4, 0, 0.2, 1)',
      'opacity 240ms ease',
      'transform 320ms cubic-bezier(0.4, 0, 0.2, 1)',
      'filter 240ms ease',
    ].join(', ')
    html.style.height = '0px'
    html.style.opacity = '0'
    html.style.transform = 'translateY(-6px) scale(0.99)'
    html.style.filter = 'blur(2px)'
    html.style.marginTop = '0px'
    html.style.marginBottom = '0px'
    html.addEventListener('transitionend', onEnd)
    timer = window.setTimeout(finish, 450)
  })
}

const turnId = computed(() => props.items[0]?.turnId ?? '')
const turnIds = computed(() => [
  ...new Set(codexStore.activeItems.map((item) => item.turnId).filter(Boolean)),
])
const turnIndex = computed(() => turnIds.value.indexOf(turnId.value))
const isLastTurn = computed(() => turnIndex.value >= 0 && turnIndex.value === turnIds.value.length - 1)
const turnsFromHere = computed(() => {
  if (turnIndex.value < 0) return 0
  return turnIds.value.length - turnIndex.value
})
const isFailed = computed(() => props.items.some((item) => item.failed))
const agentPlainText = computed(() =>
  props.items
    .filter((item) =>
      item.type === 'agentMessage'
      || item.type === 'plan'
      || item.type === 'hookPrompt'
      || item.type === 'notice',
    )
    .map((item) => item.text)
    .filter(Boolean)
    .join('\n\n'),
)

type StreamBlock =
  | { kind: 'text'; item: TimelineItem }
  | { kind: 'reasoning'; item: TimelineItem }
  | { kind: 'plan'; item: TimelineItem }
  | { kind: 'command'; item: TimelineItem }
  | { kind: 'patch'; item: TimelineItem; nestedCommand?: TimelineItem }
  | {
      kind: 'toolGroup'
      id: string
      integrationKey: string
      integrationName: string
      items: TimelineItem[]
    }

type PlanStep = { step: string; status: 'pending' | 'in_progress' | 'completed' }

const TOOL_GROUP_TYPES = new Set([
  'mcpToolCall',
  'dynamicToolCall',
  'collabAgentToolCall',
  'webSearch',
  'imageGeneration',
  'imageView',
  'sleep',
  'subAgentActivity',
])

const stream = computed<StreamBlock[]>(() => {
  const blocks: StreamBlock[] = []
  const items = props.items.filter((item) => item.type !== 'userMessage')
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index]
    if (!item) continue
    if (item.type === 'agentMessage' || item.type === 'hookPrompt' || item.type === 'notice') {
      blocks.push({ kind: 'text', item })
      continue
    }
    if (item.type === 'plan') {
      blocks.push({ kind: 'plan', item })
      continue
    }
    if (item.type === 'reasoning') {
      blocks.push({ kind: 'reasoning', item })
      continue
    }
    if (item.type === 'commandExecution') {
      // Official nests a preceding shell under the following apply-patch step.
      const next = items[index + 1]
      if (next?.type === 'fileChange') continue
      blocks.push({ kind: 'command', item })
      continue
    }
    if (item.type === 'fileChange') {
      const prev = items[index - 1]
      const nestedCommand = prev?.type === 'commandExecution' ? prev : undefined
      blocks.push({ kind: 'patch', item, nestedCommand })
      continue
    }
    if (TOOL_GROUP_TYPES.has(item.type)) {
      const integrationKey = toolIntegrationKey(item)
      const last = blocks.at(-1)
      if (last?.kind === 'toolGroup' && last.integrationKey === integrationKey) {
        blocks[blocks.length - 1] = {
          ...last,
          items: [...last.items, item],
        }
        continue
      }
      blocks.push({
        kind: 'toolGroup',
        id: `tool-group:${item.id}`,
        integrationKey,
        integrationName: toolIntegrationName(item),
        items: [item],
      })
      continue
    }
    // Fallback: treat unknown activity as its own integration group.
    blocks.push({
      kind: 'toolGroup',
      id: `tool-group:${item.id}`,
      integrationKey: toolIntegrationKey(item),
      integrationName: toolIntegrationName(item),
      items: [item],
    })
  }
  return blocks
})

function streamBlockKey(block: StreamBlock): string {
  if (block.kind === 'toolGroup') return block.id
  return block.item.id
}

type DisplayFileChange = {
  path: string
  kind: string
  add: number
  del: number
  diff: string
}

const resolvedFileChanges = computed<DisplayFileChange[]>(() => {
  const byPath = new Map<string, DisplayFileChange>()

  for (const item of props.items) {
    if (item.type !== 'fileChange') continue
    for (const change of item.changes) {
      const path = change.path.trim()
      if (!path) continue
      const stats = diffStats(change.diff)
      const prev = byPath.get(path)
      byPath.set(path, {
        path,
        kind: change.kind || prev?.kind || 'update',
        add: Math.max(stats.add, prev?.add ?? 0),
        del: Math.max(stats.del, prev?.del ?? 0),
        diff: change.diff || prev?.diff || '',
      })
    }
  }

  const turnDiff = props.turnDiff?.trim() ?? ''
  if (turnDiff) {
    for (const file of parseUnifiedDiff(turnDiff)) {
      const path = file.displayPath.trim()
      if (!path) continue
      const kind = file.newPath === '/dev/null'
        ? 'delete'
        : (!file.oldPath || file.oldPath === '/dev/null')
          ? 'add'
          : 'update'
      const prev = byPath.get(path)
      const fileDiff = prev?.diff || extractFileDiff(turnDiff, path) || turnDiff
      byPath.set(path, {
        path,
        kind: prev?.kind || kind,
        add: Math.max(file.additions, prev?.add ?? 0),
        del: Math.max(file.deletions, prev?.del ?? 0),
        diff: fileDiff,
      })
    }
  }

  return [...byPath.values()].sort((a, b) => a.path.localeCompare(b.path))
})

const turnFileTotals = computed(() => {
  let add = 0
  let del = 0
  for (const change of resolvedFileChanges.value) {
    add += change.add
    del += change.del
  }
  return { count: resolvedFileChanges.value.length, add, del }
})

/** Caret only on actively streaming text; never stick to completed text above tools. */
const liveTextId = computed(() => {
  if (!props.streaming) return ''
  for (let index = stream.value.length - 1; index >= 0; index -= 1) {
    const block = stream.value[index]
    if (block?.kind !== 'text') continue
    if (isRunning(block.item.status)) return block.item.id
    break
  }
  const last = stream.value.at(-1)
  return last?.kind === 'text' ? last.item.id : ''
})

const liveReasoningId = computed(() => {
  if (!props.streaming) return ''
  const last = stream.value.at(-1)
  return last?.kind === 'reasoning' ? last.item.id : ''
})

watch(
  liveReasoningId,
  (id, prev) => {
    const next = { ...openRows.value }
    // Auto-collapse the previous thinking block when the stream moves on.
    if (prev && prev !== id) delete next[prev]
    if (id) next[id] = true
    openRows.value = next
  },
  { immediate: true },
)

function reasoningOpen(id: string): boolean {
  if (Object.prototype.hasOwnProperty.call(openRows.value, id)) {
    return openRows.value[id] === true
  }
  return liveReasoningId.value === id
}

function stripReasoningMarkdown(text: string): string {
  return text
    .replace(/\*\*/g, '')
    .replace(/__/g, '')
    .replace(/^#+\s*/gm, '')
    .replace(/`+/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

function reasoningTitle(item: TimelineItem): string {
  const raw = (item.reasoningSummary || item.reasoningContent || item.text || '').trim()
  if (!raw) return t('timeline.reasoningLive')
  const firstLine = raw.split(/\n+/).map((line) => line.trim()).find(Boolean) || raw
  const cleaned = stripReasoningMarkdown(firstLine)
  if (!cleaned) return t('timeline.reasoningLive')
  return cleaned.length > 64 ? `${cleaned.slice(0, 64)}…` : cleaned
}

function plainStreamText(item: TimelineItem): string {
  if (item.type === 'reasoning') {
    return item.reasoningSummary || item.reasoningContent || item.text || ''
  }
  return stripProposedPlanTags(item.text || '')
}

function stripProposedPlanTags(text: string): string {
  return text
    .replace(/<proposed_plan>\s*/gi, '')
    .replace(/\s*<\/proposed_plan>/gi, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

function isOpen(id: string): boolean {
  return Boolean(openRows.value[id])
}

function toggle(id: string): void {
  const currently = Object.prototype.hasOwnProperty.call(openRows.value, id)
    ? openRows.value[id] === true
    : reasoningOpen(id)
  openRows.value = { ...openRows.value, [id]: !currently }
}

function toggleTurnFiles(): void {
  openRows.value = { ...openRows.value, 'turn-files': !turnFilesOpen.value }
}

const turnFilesOpen = computed(() => {
  if (Object.prototype.hasOwnProperty.call(openRows.value, 'turn-files')) {
    return openRows.value['turn-files'] === true
  }
  return true
})

/** Official Codex keeps patch steps expanded so file +/- stay visible. */
function stepOpen(id: string, fallback = true): boolean {
  if (Object.prototype.hasOwnProperty.call(openRows.value, id)) {
    return openRows.value[id] === true
  }
  return fallback
}

function toggleStep(id: string, fallback = true): void {
  openRows.value = { ...openRows.value, [id]: !stepOpen(id, fallback) }
}

function patchChanges(item: TimelineItem): DisplayFileChange[] {
  if (item.changes.length) {
    return item.changes.map((change) => {
      const stats = diffStats(change.diff)
      const fromTurn = resolvedFileChanges.value.find((entry) =>
        pathsLooseEqual(entry.path, change.path),
      )
      return {
        path: change.path,
        kind: normalizeKindLabel(change.kind || fromTurn?.kind || 'update'),
        add: Math.max(stats.add, fromTurn?.add ?? 0),
        del: Math.max(stats.del, fromTurn?.del ?? 0),
        diff: change.diff || fromTurn?.diff || '',
      }
    })
  }
  // Only fall back to turn/diff when this is the sole patch item in the turn.
  const patchItems = props.items.filter((entry) => entry.type === 'fileChange')
  if (patchItems.length === 1 && patchItems[0]?.id === item.id) {
    return resolvedFileChanges.value
  }
  return []
}

function pathsLooseEqual(left: string, right: string): boolean {
  const a = left.replace(/\\/g, '/').replace(/^\.\//, '')
  const b = right.replace(/\\/g, '/').replace(/^\.\//, '')
  return a === b || a.endsWith(`/${b}`) || b.endsWith(`/${a}`)
}

function normalizeKindLabel(kind: string): string {
  const key = kind.trim().toLowerCase()
  if (key === 'add' || key === 'added' || key === 'create') return 'add'
  if (key === 'delete' || key === 'deleted' || key === 'remove') return 'delete'
  return 'update'
}

function patchStepTitle(item: TimelineItem): string {
  if (item.title?.startsWith('Applying patch')) return item.title
  const changes = patchChanges(item)
  if (changes.length === 1) {
    const name = fileStem(changes[0]?.path || '')
    return t('timeline.applyingPatchTo', { name })
  }
  if (changes.length > 1) return t('timeline.applyingPatchFiles', { count: changes.length })
  return t('timeline.applyingPatch')
}

function fileStem(path: string): string {
  const name = path.split(/[\\/]/).filter(Boolean).at(-1) || path || 'file'
  return name.replace(/\.[^.]+$/, '') || name
}

function fileActionLabel(kind: string): string {
  if (kind === 'add') return t('timeline.addedFile')
  if (kind === 'delete') return t('timeline.deletedFile')
  return t('timeline.editedFile')
}

function commandRanLabel(command: string): string {
  const compact = commandLabel(command)
  return compact ? t('timeline.ranCommand', { command: compact }) : t('timeline.command')
}

function parsePlanSteps(text: string): PlanStep[] | null {
  const raw = text.trim()
  if (!raw) return null
  try {
    const parsed: unknown = JSON.parse(raw)
    const record = parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
      ? parsed as Record<string, unknown>
      : null
    const list = Array.isArray(parsed)
      ? parsed
      : Array.isArray(record?.plan)
        ? record.plan
        : Array.isArray(record?.steps)
          ? record.steps
          : null
    if (list?.length) {
      const steps = list.map((entry) => {
        const row = asPlanRecord(entry)
        const step = String(row.step ?? row.text ?? row.title ?? '').trim()
        const status = normalizePlanStatus(String(row.status ?? 'pending'))
        return step ? { step, status } : null
      }).filter((entry): entry is PlanStep => Boolean(entry))
      if (steps.length) return steps
    }
  } catch {
    // fall through to markdown / plain lists
  }

  const checkbox = raw.split(/\r?\n/).flatMap((line) => {
    const match = line.match(/^\s*[-*]\s+\[([ xX])\]\s+(.+)$/)
    if (!match) return []
    return [{
      step: match[2]?.trim() || '',
      status: match[1]?.toLowerCase() === 'x' ? 'completed' as const : 'pending' as const,
    }]
  }).filter((entry) => entry.step)
  if (checkbox.length) return checkbox

  return null
}

function asPlanRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {}
}

function normalizePlanStatus(value: string): PlanStep['status'] {
  const key = value.trim().toLowerCase().replace(/[_-]/g, '')
  if (key === 'completed' || key === 'done' || key === 'complete') return 'completed'
  if (key === 'inprogress' || key === 'running' || key === 'active') return 'in_progress'
  return 'pending'
}

function isRunning(status: string): boolean {
  const s = status.toLowerCase().replace(/[_-]/g, '')
  return s === 'running' || s === 'inprogress' || s === 'started' || s === 'pending' || s === 'active'
}

function isError(status: string, failed?: boolean): boolean {
  if (failed) return true
  const s = status.toLowerCase().replace(/[_-]/g, '')
  return s === 'failed' || s === 'error' || s === 'cancelled' || s === 'canceled' || s === 'interrupted'
}

function itemDuration(item: TimelineItem): string {
  if (!item.startedAt || !item.completedAt) return ''
  return formatDuration(Math.max(0, item.completedAt - item.startedAt))
}

function copyText(text: string, key = 'default'): void {
  if (!text) return
  void navigator.clipboard.writeText(text).then(() => {
    copiedKey.value = key
    window.setTimeout(() => {
      if (copiedKey.value === key) copiedKey.value = ''
    }, 1500)
  })
}

function isCopied(key: string): boolean {
  return copiedKey.value === key
}

function payloadText(source: string): string {
  return formatToolPayload(source).text || source
}

function requestRollback(mode: 'single' | 'fromHere'): void {
  if (!turnId.value) return
  if (mode === 'single' && !isLastTurn.value) return
  emit('rollback', { turnId: turnId.value, mode })
}

function onMarkdownClick(event: MouseEvent): void {
  const collapseTarget = event.target instanceof HTMLElement
    ? event.target.closest<HTMLElement>('[data-collapse-code]')
    : null
  if (collapseTarget) {
    const block = collapseTarget.closest('.markdown-code')
    if (!block) return
    const collapsed = block.classList.toggle('is-collapsed')
    collapseTarget.setAttribute('aria-expanded', collapsed ? 'false' : 'true')
    collapseTarget.textContent = collapsed ? t('timeline.showMoreCode') : t('timeline.showLessCode')
    return
  }

  const target = event.target instanceof HTMLElement ? event.target.closest<HTMLElement>('[data-copy-code]') : null
  const code = target?.parentElement?.querySelector('code')?.textContent
  if (!target || !code) return
  void navigator.clipboard.writeText(code).then(() => {
    const original = target.textContent
    target.textContent = t('timeline.copied')
    window.setTimeout(() => { target.textContent = original }, 1200)
  })
}

function attachmentName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function formatDuration(durationMs: number | null | undefined): string {
  if (!durationMs) return ''
  if (durationMs < 1000) return `${durationMs}ms`
  const seconds = Math.round(durationMs / 1000)
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function shortPath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean)
  if (parts.length <= 2) return path
  return `…/${parts.slice(-2).join('/')}`
}

function commandLabel(command: string): string {
  const compact = command.replace(/\s+/g, ' ').trim()
  if (compact.length <= 72) return compact
  return `${compact.slice(0, 72)}…`
}

function splitToolTitle(item: TimelineItem): { integration: string; action: string } {
  const raw = (item.title || item.type || '').trim()
  const parts = raw.split(/\s*\/\s*/).map((part) => part.trim()).filter(Boolean)
  if (parts.length >= 2) {
    return { integration: parts[0] || raw, action: parts.slice(1).join(' / ') }
  }
  if (item.type === 'webSearch') return { integration: 'Web Search', action: item.detail || raw || 'Search' }
  if (item.type === 'mcpToolCall') return { integration: raw || 'MCP', action: t('timeline.mcpTool') }
  if (item.type === 'dynamicToolCall') return { integration: raw || 'Tool', action: raw || t('timeline.mcpTool') }
  return { integration: humanizeToolLabel(raw || item.type), action: humanizeToolLabel(raw || item.type) }
}

function toolIntegrationKey(item: TimelineItem): string {
  return splitToolTitle(item).integration.toLowerCase()
}

function toolIntegrationName(item: TimelineItem): string {
  return humanizeToolLabel(splitToolTitle(item).integration)
}

function toolActionLabel(item: TimelineItem): string {
  return humanizeToolLabel(splitToolTitle(item).action)
}

function humanizeToolLabel(value: string): string {
  const raw = value.trim()
  if (!raw) return ''
  if (!/[_-]/.test(raw) && /[A-Z]/.test(raw[0] || '')) return raw
  return raw
    .split(/[_.\s-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

/** Official Cursor: same MCP consecutive calls collapse under one integration header. */
function toolGroupOpen(groupId: string, _items: TimelineItem[]): boolean {
  if (Object.prototype.hasOwnProperty.call(openRows.value, groupId)) {
    return openRows.value[groupId] === true
  }
  // Match official: show nested tool names by default; click header to collapse.
  return true
}

function toggleToolGroup(groupId: string, items: TimelineItem[]): void {
  openRows.value = { ...openRows.value, [groupId]: !toolGroupOpen(groupId, items) }
}

function toolGroupHasError(items: TimelineItem[]): boolean {
  return items.some((item) => isError(item.status, item.failed))
}

function toolGroupRunning(items: TimelineItem[]): boolean {
  return items.some((item) => isRunning(item.status))
}

function diffStats(diff: string): { add: number; del: number } {
  let add = 0
  let del = 0
  for (const line of diff.split('\n')) {
    if (line.startsWith('+') && !line.startsWith('+++')) add += 1
    else if (line.startsWith('-') && !line.startsWith('---')) del += 1
  }
  return { add, del }
}
</script>

<template>
  <div
    v-motion
    :initial="animated ? { opacity: 0, y: 8 } : false"
    :animate="{ opacity: 1, y: 0 }"
    :transition="{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }"
    class="group w-full"
    :class="streaming ? '' : '[content-visibility:auto] [contain-intrinsic-size:72px]'"
  >
    <!-- User prompt — Claude-style soft bubble, right aligned -->
    <div v-if="kind === 'user'" class="flex flex-col items-end gap-1">
      <div class="max-w-[min(100%,42rem)] rounded-2xl bg-muted/70 px-3.5 py-2.5 text-[14px] leading-6 text-foreground">
        <p class="whitespace-pre-wrap break-words">{{ items[0]?.text }}</p>
        <div v-if="items[0]?.attachments.length" class="mt-1.5 flex flex-wrap gap-1.5">
          <div
            v-for="attachment in items[0].attachments"
            :key="attachment.source"
            class="overflow-hidden rounded-lg border border-border/60 bg-background/70"
          >
            <img
              v-if="attachmentPreview(attachment.source)"
              :src="attachmentPreview(attachment.source)"
              :alt="attachmentName(attachment.source)"
              class="max-h-36 max-w-[180px] object-cover"
              loading="lazy"
            >
            <Badge
              v-else
              variant="secondary"
              class="h-5 rounded-full text-[10px] font-normal"
            >
              {{ attachmentName(attachment.source) }}
            </Badge>
          </div>
        </div>
      </div>
      <div class="flex h-6 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <TooltipProvider v-if="isFailed">
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="ghost"
                size="icon-xs"
                class="size-6 text-muted-foreground"
                :aria-label="t('chat.retryMessage')"
                @click="emit('retry', items[0].id)"
              >
                <RefreshCcw :size="12" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{{ t('chat.retryMessage') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="ghost"
                size="icon-xs"
                class="size-6 text-muted-foreground"
                :aria-label="isCopied('user') ? t('timeline.copied') : t('timeline.copyMessage')"
                @click="copyText(items[0]?.text ?? '', 'user')"
              >
                <Check v-if="isCopied('user')" :size="12" class="text-positive" />
                <Copy v-else :size="12" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{{ isCopied('user') ? t('timeline.copied') : t('timeline.copyMessage') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </div>

    <!-- Agent stream — Claude-like: quiet tools, airy prose -->
    <div v-else class="space-y-2">
      <TransitionGroup
        tag="div"
        class="timeline-step-list space-y-1.5"
        :css="false"
        move-class="timeline-step-move"
        @before-enter="onStepBeforeEnter"
        @enter="onStepEnter"
        @after-enter="onStepAfterEnter"
        @before-leave="onStepBeforeLeave"
        @leave="onStepLeave"
      >
        <div
          v-if="streaming && !stream.length"
          key="thinking-placeholder"
          class="flex items-center gap-2 py-1 text-[13px] text-muted-foreground"
        >
          <span class="thinking-dots thinking-dots-sm" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <span>{{ t('chat.thinking') }}</span>
        </div>

        <div
          v-for="block in stream"
          :key="streamBlockKey(block)"
          class="timeline-step-item"
        >
          <!-- Reasoning — Codex-style: show streamed summary text, not just a spinner -->
          <div v-if="block.kind === 'reasoning'" class="reasoning-block py-0.5">
            <button
              type="button"
              class="inline-flex max-w-full items-center gap-1.5 rounded-md px-0.5 py-0.5 text-left text-[12.5px] text-muted-foreground transition-colors hover:text-foreground"
              :title="t('timeline.reasoningHint')"
              @click="toggle(block.item.id)"
            >
              <component
                :is="(reasoningOpen(block.item.id) || liveReasoningId === block.item.id) ? ChevronDown : ChevronRight"
                :size="12"
                class="shrink-0 opacity-50"
              />
              <span class="shrink-0 font-medium text-foreground/70">{{ t('timeline.reasoning') }}</span>
              <span
                v-if="liveReasoningId === block.item.id"
                class="thinking-dots thinking-dots-sm"
                aria-hidden="true"
              >
                <span /><span /><span />
              </span>
              <span
                v-if="liveReasoningId !== block.item.id && !reasoningOpen(block.item.id)"
                class="min-w-0 truncate text-[12px] text-foreground/65"
              >
                {{ reasoningTitle(block.item) }}
              </span>
              <span
                v-else-if="liveReasoningId === block.item.id && !plainStreamText(block.item)"
                class="text-[12px] text-foreground/55"
              >
                {{ t('timeline.reasoningLive') }}
              </span>
            </button>
            <div
              v-if="reasoningOpen(block.item.id) || liveReasoningId === block.item.id"
              class="reasoning-body mt-1 max-h-56 overflow-y-auto border-l-2 border-border/40 pl-3 text-[13px] leading-6 text-foreground/80"
            >
              <template v-if="plainStreamText(block.item)">
                <div
                  class="prose prose-sm max-w-none reasoning-prose prose-headings:mb-1.5 prose-headings:mt-2.5 prose-headings:text-[0.95em] prose-headings:font-semibold prose-p:my-1.5 prose-p:leading-6 prose-li:my-0.5 prose-ul:my-1.5 prose-ol:my-1.5 prose-pre:my-2 prose-pre:rounded-lg prose-code:rounded prose-code:px-1 prose-code:py-0.5 prose-code:text-[0.86em] prose-code:before:content-none prose-code:after:content-none prose-strong:font-semibold"
                  @click="onMarkdownClick"
                  v-html="markdownHTML(plainStreamText(block.item))"
                />
                <span
                  v-if="liveReasoningId === block.item.id"
                  class="streaming-caret ml-0.5 inline-block h-[1em] w-[2px] translate-y-[0.15em] bg-foreground/70 align-baseline"
                  aria-hidden="true"
                />
              </template>
              <div
                v-else-if="liveReasoningId === block.item.id"
                class="flex items-center gap-2 text-[12.5px] text-muted-foreground"
              >
                <span class="thinking-dots thinking-dots-sm" aria-hidden="true">
                  <span /><span /><span />
                </span>
                <span>{{ t('timeline.reasoningLive') }}</span>
              </div>
            </div>
          </div>

          <!-- Plan checklist (update_plan) -->
          <div v-else-if="block.kind === 'plan'" class="space-y-1.5 py-0.5">
            <div class="inline-flex items-center gap-1.5 text-[12px] text-muted-foreground">
              <ListTodo :size="12" class="opacity-50" />
              <span class="font-medium text-foreground/70">{{ t('timeline.plan') }}</span>
            </div>
            <template v-for="planSteps in [parsePlanSteps(block.item.text)]" :key="`${block.item.id}:plan`">
              <ul v-if="planSteps?.length" class="space-y-1 pl-1">
                <li
                  v-for="(step, stepIndex) in planSteps"
                  :key="`${block.item.id}:${stepIndex}`"
                  class="flex items-start gap-2 text-[13px] leading-5 text-foreground/85"
                >
                  <CheckCircle2 v-if="step.status === 'completed'" :size="14" class="mt-0.5 shrink-0 text-positive" />
                  <CircleDot v-else-if="step.status === 'in_progress'" :size="14" class="mt-0.5 shrink-0 text-foreground/70" />
                  <Circle v-else :size="14" class="mt-0.5 shrink-0 text-muted-foreground/50" />
                  <span :class="step.status === 'completed' ? 'text-muted-foreground line-through decoration-muted-foreground/40' : ''">
                    {{ step.step }}
                  </span>
                </li>
              </ul>
              <div
                v-else
                class="prose prose-sm max-w-none reasoning-prose"
                @click="onMarkdownClick"
                v-html="markdownHTML(block.item.text)"
              />
            </template>
          </div>

          <!-- Command — official "Ran …" style -->
          <div v-else-if="block.kind === 'command'">
            <button
              type="button"
              class="group/tool inline-flex max-w-full items-center gap-1.5 rounded-lg px-1.5 py-1 text-left text-[12px] text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground"
              :class="isError(block.item.status, block.item.failed) ? 'text-destructive' : ''"
              @click="toggleStep(block.item.id, false)"
            >
              <LoaderCircle
                v-if="isRunning(block.item.status)"
                :size="12"
                class="shrink-0 animate-spin opacity-70"
              />
              <Terminal v-else :size="12" class="shrink-0 opacity-50" />
              <span class="min-w-0 truncate font-mono text-[11.5px]">{{ commandRanLabel(block.item.command) }}</span>
              <span v-if="itemDuration(block.item)" class="shrink-0 tabular-nums text-[10px] opacity-50">{{ itemDuration(block.item) }}</span>
              <component
                :is="stepOpen(block.item.id, false) ? ChevronDown : ChevronRight"
                v-if="block.item.output"
                :size="11"
                class="shrink-0 opacity-40"
              />
            </button>
            <div
              v-if="stepOpen(block.item.id, false) && block.item.output"
              class="relative mt-1 ml-1"
            >
              <button
                type="button"
                class="absolute right-1.5 top-1.5 z-[1] inline-flex size-5 items-center justify-center rounded bg-card/80 text-muted-foreground hover:text-foreground"
                :aria-label="isCopied(`cmd:${block.item.id}`) ? t('timeline.copied') : t('timeline.copyMessage')"
                @click="copyText(payloadText(block.item.output), `cmd:${block.item.id}`)"
              >
                <Check v-if="isCopied(`cmd:${block.item.id}`)" :size="11" class="text-positive" />
                <Copy v-else :size="11" />
              </button>
              <pre
                class="tool-payload max-h-44 overflow-auto rounded-xl bg-muted/45 px-3 py-2 pr-8 font-mono text-[11px] leading-5 text-foreground"
                v-html="renderToolPayloadHTML(block.item.output)"
              />
            </div>
          </div>

          <!-- File change / apply_patch — official collapsible step with per-file +/- -->
          <div v-else-if="block.kind === 'patch'" class="space-y-0.5 py-0.5">
            <button
              type="button"
              class="inline-flex max-w-full items-center gap-1.5 rounded-lg px-1.5 py-1 text-left text-[12px] text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground"
              @click="toggleStep(block.item.id, true)"
            >
              <LoaderCircle
                v-if="isRunning(block.item.status)"
                :size="12"
                class="shrink-0 animate-spin opacity-70"
              />
              <FileDiff v-else :size="12" class="shrink-0 opacity-50" />
              <span class="min-w-0 truncate">{{ patchStepTitle(block.item) }}</span>
              <component
                :is="stepOpen(block.item.id, true) ? ChevronDown : ChevronRight"
                :size="11"
                class="shrink-0 opacity-40"
              />
            </button>
            <div v-if="stepOpen(block.item.id, true)" class="space-y-0.5 pl-2">
              <button
                v-if="block.nestedCommand"
                type="button"
                class="flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 text-left text-[12px] text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground"
                :class="isError(block.nestedCommand.status, block.nestedCommand.failed) ? 'text-destructive' : ''"
                @click="toggleStep(block.nestedCommand.id, false)"
              >
                <Terminal :size="12" class="shrink-0 opacity-50" />
                <span class="min-w-0 truncate font-mono text-[11.5px]">{{ commandRanLabel(block.nestedCommand.command) }}</span>
              </button>
              <div
                v-if="block.nestedCommand && stepOpen(block.nestedCommand.id, false) && block.nestedCommand.output"
                class="relative ml-1"
              >
                <pre
                  class="tool-payload max-h-36 overflow-auto rounded-xl bg-muted/45 px-3 py-2 font-mono text-[11px] leading-5 text-foreground"
                  v-html="renderToolPayloadHTML(block.nestedCommand.output)"
                />
              </div>
              <button
                v-for="change in patchChanges(block.item)"
                :key="`${block.item.id}:${change.path}`"
                type="button"
                class="flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 text-left text-[12px] text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground"
                :title="change.path"
                @click="emit('inspect-diff', { path: change.path, diff: change.diff })"
              >
                <Pencil :size="12" class="shrink-0 opacity-50" />
                <span class="shrink-0">{{ fileActionLabel(change.kind) }}</span>
                <span class="min-w-0 truncate font-medium text-foreground/80 underline decoration-dotted decoration-muted-foreground/50 underline-offset-2">
                  {{ shortPath(change.path) }}
                </span>
                <span class="shrink-0 tabular-nums text-[11px] text-positive">+{{ change.add }}</span>
                <span class="shrink-0 tabular-nums text-[11px] text-destructive">-{{ change.del }}</span>
              </button>
              <div
                v-if="isRunning(block.item.status) && !patchChanges(block.item).length"
                class="flex items-center gap-1.5 px-1.5 py-1 text-[12px] text-muted-foreground"
              >
                <LoaderCircle :size="12" class="animate-spin opacity-70" />
                <span>{{ t('timeline.applyingPatch') }}…</span>
              </div>
            </div>
          </div>

          <!-- Tool group — official Cursor: collapse consecutive same-MCP calls -->
          <div v-else-if="block.kind === 'toolGroup'" class="tool-integration-group">
            <button
              type="button"
              class="inline-flex max-w-full items-center gap-1.5 rounded-md px-1 py-0.5 text-left text-[12px] text-muted-foreground/80 transition-colors hover:bg-muted/40 hover:text-foreground"
              :class="toolGroupHasError(block.items) ? 'text-destructive' : ''"
              @click="toggleToolGroup(block.id, block.items)"
            >
              <LoaderCircle
                v-if="toolGroupRunning(block.items)"
                :size="12"
                class="shrink-0 animate-spin opacity-70"
              />
              <Plug v-else :size="12" class="shrink-0 opacity-45" />
              <span class="min-w-0 truncate">{{ t('timeline.usedIntegration', { name: block.integrationName }) }}</span>
              <span
                v-if="block.items.length > 1"
                class="shrink-0 tabular-nums text-[10px] opacity-50"
              >{{ block.items.length }}</span>
              <component
                :is="toolGroupOpen(block.id, block.items) ? ChevronDown : ChevronRight"
                :size="11"
                class="shrink-0 opacity-40"
              />
            </button>

            <div
              v-if="toolGroupOpen(block.id, block.items)"
              class="mt-0.5 space-y-0.5 pl-1"
            >
              <div
                v-for="item in block.items"
                :key="item.id"
                class="min-w-0"
              >
                <button
                  type="button"
                  class="inline-flex max-w-full items-center gap-1.5 rounded-md px-1 py-0.5 text-left text-[12px] text-muted-foreground/75 transition-colors hover:bg-muted/35 hover:text-foreground"
                  :class="isError(item.status, item.failed) ? 'text-destructive' : ''"
                  @click="toggle(item.id)"
                >
                  <LoaderCircle
                    v-if="isRunning(item.status)"
                    :size="11"
                    class="shrink-0 animate-spin opacity-70"
                  />
                  <Plug v-else :size="11" class="shrink-0 opacity-40" />
                  <span class="min-w-0 truncate">{{ toolActionLabel(item) }}</span>
                  <span
                    v-if="itemDuration(item)"
                    class="shrink-0 tabular-nums text-[10px] opacity-45"
                  >{{ itemDuration(item) }}</span>
                </button>

                <div v-if="isOpen(item.id)" class="mt-1 ml-1 space-y-1.5">
                  <div v-if="item.detail" class="relative">
                    <button
                      type="button"
                      class="absolute right-1.5 top-1.5 z-[1] inline-flex size-5 items-center justify-center rounded bg-card/80 text-muted-foreground hover:text-foreground"
                      :aria-label="isCopied(`tool-d:${item.id}`) ? t('timeline.copied') : t('timeline.copyMessage')"
                      @click="copyText(payloadText(item.detail), `tool-d:${item.id}`)"
                    >
                      <Check v-if="isCopied(`tool-d:${item.id}`)" :size="11" class="text-positive" />
                      <Copy v-else :size="11" />
                    </button>
                    <pre
                      class="tool-payload max-h-40 overflow-auto rounded-xl bg-muted/45 px-3 py-2 pr-8 font-mono text-[11px] leading-5 text-foreground"
                      v-html="renderToolPayloadHTML(item.detail)"
                    />
                  </div>
                  <div v-if="item.output" class="relative">
                    <button
                      type="button"
                      class="absolute right-1.5 top-1.5 z-[1] inline-flex size-5 items-center justify-center rounded bg-card/80 text-muted-foreground hover:text-foreground"
                      :aria-label="isCopied(`tool-o:${item.id}`) ? t('timeline.copied') : t('timeline.copyMessage')"
                      @click="copyText(payloadText(item.output), `tool-o:${item.id}`)"
                    >
                      <Check v-if="isCopied(`tool-o:${item.id}`)" :size="11" class="text-positive" />
                      <Copy v-else :size="11" />
                    </button>
                    <pre
                      class="tool-payload max-h-48 overflow-auto rounded-xl bg-muted/45 px-3 py-2 pr-8 font-mono text-[11px] leading-5 text-foreground"
                      v-html="renderToolPayloadHTML(item.output)"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Agent text -->
          <div
            v-else-if="block.kind === 'text'"
            class="claude-prose text-[14.5px] leading-7 text-foreground"
          >
            <template v-if="block.item.id === liveTextId">
              <div
                class="prose max-w-none prose-headings:mb-2.5 prose-headings:mt-4 prose-headings:text-[1.05em] prose-headings:font-semibold prose-headings:tracking-tight prose-p:my-2.5 prose-p:leading-7 prose-li:my-1 prose-ul:my-2.5 prose-ol:my-2.5 prose-pre:my-3 prose-pre:rounded-xl prose-code:rounded-md prose-code:px-1.5 prose-code:py-0.5 prose-code:text-[0.86em] prose-code:before:content-none prose-code:after:content-none prose-a:font-medium prose-strong:font-semibold"
                @click="onMarkdownClick"
                v-html="markdownHTML(stripProposedPlanTags(plainStreamText(block.item)))"
              />
              <span
                class="streaming-caret ml-0.5 inline-block h-[1em] w-[2px] translate-y-[0.15em] bg-foreground/80 align-baseline"
                aria-hidden="true"
              />
            </template>
            <div
              v-else
              class="prose max-w-none stream-settle prose-headings:mb-2.5 prose-headings:mt-4 prose-headings:text-[1.05em] prose-headings:font-semibold prose-headings:tracking-tight prose-p:my-2.5 prose-p:leading-7 prose-li:my-1 prose-ul:my-2.5 prose-ol:my-2.5 prose-pre:my-3 prose-pre:rounded-xl prose-code:rounded-md prose-code:px-1.5 prose-code:py-0.5 prose-code:text-[0.86em] prose-code:before:content-none prose-code:after:content-none prose-a:font-medium prose-strong:font-semibold"
              @click="onMarkdownClick"
              v-html="markdownHTML(stripProposedPlanTags(block.item.text))"
            />
          </div>
        </div>
      </TransitionGroup>

      <!-- Official Codex: consolidated file change list for the turn (live + end summary). -->
      <div
        v-if="resolvedFileChanges.length"
        class="mt-1 space-y-0.5 border-t border-border/50 pt-2"
      >
        <button
          type="button"
          class="inline-flex max-w-full items-center gap-1.5 rounded-lg px-1.5 py-1 text-[12px] text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground"
          @click="toggleTurnFiles"
        >
          <FileDiff :size="12" class="shrink-0 opacity-50" />
          <span>{{ t('timeline.filesChanged') }}</span>
          <span class="tabular-nums opacity-70">{{ turnFileTotals.count }}</span>
          <span class="tabular-nums text-[11px] text-positive">+{{ turnFileTotals.add }}</span>
          <span class="tabular-nums text-[11px] text-destructive">-{{ turnFileTotals.del }}</span>
          <component :is="turnFilesOpen ? ChevronDown : ChevronRight" :size="11" class="opacity-40" />
        </button>
        <div v-if="turnFilesOpen" class="space-y-0.5 pl-2">
          <button
            v-for="change in resolvedFileChanges"
            :key="change.path"
            type="button"
            class="flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 text-left text-[12px] text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground"
            :title="change.path"
            @click="emit('inspect-diff', { path: change.path, diff: change.diff })"
          >
            <Pencil :size="12" class="shrink-0 opacity-50" />
            <span class="shrink-0">{{ fileActionLabel(change.kind) }}</span>
            <span class="min-w-0 flex-1 truncate font-medium text-foreground/80 underline decoration-dotted decoration-muted-foreground/50 underline-offset-2">
              {{ shortPath(change.path) }}
            </span>
            <span class="shrink-0 tabular-nums text-[11px] text-positive">+{{ change.add }}</span>
            <span class="shrink-0 tabular-nums text-[11px] text-destructive">-{{ change.del }}</span>
          </button>
        </div>
      </div>

      <div class="flex min-h-5 items-center gap-2 pt-0.5">
        <div
          v-if="metrics?.tokenUsage || metrics?.durationMs || turnFileTotals.count"
          class="flex items-center gap-1.5 text-[11px] tabular-nums text-muted-foreground/65"
        >
          <span v-if="metrics?.durationMs">{{ t('timeline.processed', { value: formatDuration(metrics.durationMs) }) }}</span>
          <span v-if="metrics?.durationMs && (metrics?.tokenUsage || turnFileTotals.count)">·</span>
          <span v-if="metrics?.tokenUsage">
            {{ (metrics.tokenUsage.inputTokens + metrics.tokenUsage.outputTokens).toLocaleString() }} tokens
          </span>
          <span v-if="metrics?.tokenUsage && turnFileTotals.count">·</span>
          <span v-if="turnFileTotals.count" class="inline-flex items-center gap-1">
            <span>{{ t('timeline.fileCount', { count: turnFileTotals.count }) }}</span>
            <span class="text-positive">+{{ turnFileTotals.add }}</span>
            <span class="text-destructive">-{{ turnFileTotals.del }}</span>
          </span>
        </div>
        <div class="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
          <TooltipProvider v-if="agentPlainText">
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="size-6 text-muted-foreground"
                  :aria-label="isCopied('agent') ? t('timeline.copied') : t('timeline.copyMessage')"
                  @click="copyText(agentPlainText, 'agent')"
                >
                  <Check v-if="isCopied('agent')" :size="12" class="text-positive" />
                  <Copy v-else :size="12" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">{{ isCopied('agent') ? t('timeline.copied') : t('timeline.copyMessage') }}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
          <DropdownMenu v-if="turnId && !streaming">
            <DropdownMenuTrigger as-child>
              <Button
                variant="ghost"
                size="icon-xs"
                class="size-6 text-muted-foreground"
                :aria-label="t('timeline.rollback')"
                :disabled="codexStore.threadMutation === 'rollback'"
              >
                <RotateCcw :size="12" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" class="w-64">
              <DropdownMenuItem
                class="flex h-auto flex-col items-start gap-0.5 py-2"
                :disabled="!isLastTurn"
                @click="requestRollback('single')"
              >
                <span>{{ t('timeline.rollbackSingle') }}</span>
                <span class="text-[10px] font-normal text-muted-foreground">{{ t('timeline.rollbackSingleHint') }}</span>
              </DropdownMenuItem>
              <DropdownMenuItem
                variant="destructive"
                class="flex h-auto flex-col items-start gap-0.5 py-2"
                @click="requestRollback('fromHere')"
              >
                <span>{{ t('timeline.rollbackFromHere') }}{{ turnsFromHere > 1 ? ` · ${turnsFromHere}` : '' }}</span>
                <span class="text-[10px] font-normal text-muted-foreground">{{ t('timeline.rollbackFromHereHint') }}</span>
              </DropdownMenuItem>
              <p class="px-2 pb-1.5 pt-1 text-[10px] text-muted-foreground">{{ t('timeline.rollbackHint') }}</p>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </div>
  </div>
</template>
