<script setup lang="ts">
import {
  Check,
  CheckCircle2,
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
import { computed, shallowRef, watch } from 'vue'
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
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { useCodexStore } from '@/stores'
import type { TimelineItem, TurnMetrics } from '@/types/codex'
import { extractFileDiff, parseUnifiedDiff } from '@/utils/diff'
import { formatToolPayload, renderToolPayloadHTML } from '@/utils/formatPayload'
import { renderMarkdown, extractCandidateFilePaths } from '@/utils/markdown'
import { resolveImagePreview } from '@/utils/imagePreview'
import { notify } from '@/utils/notify'

const props = defineProps<{
  kind: 'user' | 'agent'
  items: TimelineItem[]
  metrics?: TurnMetrics | null
  animated?: boolean
  streaming?: boolean
  turnDiff?: string
  allowTurnActions?: boolean
  /** Precomputed in ChatTimeline to avoid per-group O(n) scans. */
  turnIndex?: number
  turnCount?: number
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
const parsedTurnDiffCache = new Map<string, ReturnType<typeof parseUnifiedDiff>>()

/** Soft enter for live/recent groups only — history mounts stay instant. */
const animateEnter = computed(() => Boolean(props.streaming || props.animated))

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

/** History and live tails share the same GFM renderer; parse failures fall back to plain text. */
function markdownHTML(source: string, _item?: { id?: string; status?: string } | null): string {
  return renderMarkdown(
    source,
    t('timeline.copyMessage'),
    t('timeline.showMoreCode'),
  )
}

const turnId = computed(() => props.items[0]?.turnId ?? '')
const turnIndex = computed(() => {
  if (typeof props.turnIndex === 'number' && props.turnIndex >= 0) return props.turnIndex
  const ids = [...new Set(codexStore.activeItems.map((item) => item.turnId).filter(Boolean))]
  return ids.indexOf(turnId.value)
})
const turnCount = computed(() => {
  if (typeof props.turnCount === 'number' && props.turnCount > 0) return props.turnCount
  return [...new Set(codexStore.activeItems.map((item) => item.turnId).filter(Boolean))].length
})
const isLastTurn = computed(() => turnIndex.value >= 0 && turnIndex.value === turnCount.value - 1)
const turnsFromHere = computed(() => {
  if (turnIndex.value < 0) return 0
  return turnCount.value - turnIndex.value
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

const stream = computed<StreamBlock[]>(() => {
  const blocks: StreamBlock[] = []
  const items = props.items.filter((item) => item.type !== 'userMessage')
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index]
    if (!item) continue
    if (item.type === 'agentMessage' || item.type === 'hookPrompt' || item.type === 'notice') {
      // Keep empty in-progress agent rows while streaming so the caret/placeholder can render.
      if (!item.text?.trim() && !isRunning(item.status) && !props.streaming) continue
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
    // MCP / dynamic / other tools: collapse consecutive same-integration calls.
    // Reasoning is still inserted into the stream but hidden in the UI — skip it
    // when deciding merge so "tool → think → tool" stays one expandable group.
    pushToolGroupItem(blocks, item)
  }
  return blocks
})

/** Hidden stream kinds that must not split consecutive same-integration tool groups. */
function isSkippableToolMergeBlock(block: StreamBlock): boolean {
  return block.kind === 'reasoning'
}

/**
 * Find the latest same-integration tool group that is still "consecutive"
 * (only hidden reasoning may sit after it). Visible text/plan/command/patch break the chain.
 */
function findMergeableToolGroupIndex(blocks: StreamBlock[], integrationKey: string): number {
  for (let i = blocks.length - 1; i >= 0; i -= 1) {
    const block = blocks[i]
    if (!block) continue
    if (isSkippableToolMergeBlock(block)) continue
    if (block.kind === 'toolGroup' && block.integrationKey === integrationKey) return i
    return -1
  }
  return -1
}

function pushToolGroupItem(blocks: StreamBlock[], item: TimelineItem): void {
  const integrationKey = toolIntegrationKey(item)
  const mergeIndex = findMergeableToolGroupIndex(blocks, integrationKey)
  if (mergeIndex >= 0) {
    const last = blocks[mergeIndex]
    if (last?.kind === 'toolGroup') {
      blocks[mergeIndex] = {
        ...last,
        items: [...last.items, item],
      }
      return
    }
  }
  blocks.push({
    kind: 'toolGroup',
    id: `tool-group:${item.id}`,
    integrationKey,
    integrationName: toolIntegrationName(item),
    items: [item],
  })
}

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
  // Skip heavy diff parsing while the turn is still streaming — list shows after completion.
  if (props.streaming) return []

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
    const cacheKey = `${turnDiff.length}:${turnDiff.slice(0, 64)}:${turnDiff.slice(-64)}`
    let parsed = parsedTurnDiffCache.get(cacheKey)
    if (!parsed) {
      parsed = parseUnifiedDiff(turnDiff)
      parsedTurnDiffCache.set(cacheKey, parsed)
      if (parsedTurnDiffCache.size > 24) {
        const oldest = parsedTurnDiffCache.keys().next().value
        if (oldest) parsedTurnDiffCache.delete(oldest)
      }
    }
    for (const file of parsed) {
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
    // An empty in-progress agent is only a transport placeholder. Keep the
    // thinking shimmer visible until actual assistant text arrives.
    if (isRunning(block.item.status) && block.item.text?.trim()) return block.item.id
    break
  }
  return ''
})

/**
 * Put the typewriter caret at the end of the last *text* node, not as a sibling
 * under the whole `.prose` (which lands on a new line after ul/ol/pre/div).
 */
function injectStreamingCaret(html: string): string {
  const caret =
    '<span class="streaming-caret" aria-hidden="true"></span>'
  const source = html || ''
  if (!source.trim()) return caret

  // Prefer DOM walk so lists/tables/code put the caret after the last glyph.
  if (typeof DOMParser !== 'undefined') {
    try {
      const doc = new DOMParser().parseFromString(
        `<div id="__stream_root">${source}</div>`,
        'text/html',
      )
      const root = doc.getElementById('__stream_root')
      if (root) {
        const chrome = 'button, .markdown-code-copy, .markdown-code-expand, .markdown-code-lang'
        let target: Element = root
        while (true) {
          const kids = [...target.children].filter((el) => !el.matches(chrome))
          if (!kids.length) break
          target = kids[kids.length - 1]!
        }
        const voidLike = /^(HR|BR|IMG|INPUT|META|LINK)$/i
        if (target !== root && voidLike.test(target.tagName)) {
          target.insertAdjacentHTML('afterend', caret)
        } else if (target.tagName === 'PRE') {
          const code = target.querySelector('code')
          ;(code || target).insertAdjacentHTML('beforeend', caret)
        } else {
          target.insertAdjacentHTML('beforeend', caret)
        }
        return root.innerHTML
      }
    } catch {
      // fall through
    }
  }

  // Regex fallback when DOMParser is unavailable.
  const close = /<\/(p|li|h[1-6]|td|th|blockquote|code|strong|em|span|a)>(\s*)$/i
  if (close.test(source)) {
    return source.replace(close, `${caret}</$1>$2`)
  }
  return `${source}${caret}`
}

/**
 * Cursor-style planning shimmer:
 * - Show for the whole turn while streaming (tools included), not only while a
 *   reasoning item is "running" (that status flips off too early).
 * - Hide when final assistant text is streaming, and when the turn ends.
 */
const showPlanningShimmer = computed(() => {
  if (!props.streaming) return false
  // Final reply is on screen — planning row should not compete with it.
  if (liveTextId.value) return false
  return true
})

/** Latest reasoning item (even if already completed) — used only for the label. */
const latestReasoningItem = computed(() => {
  for (let index = stream.value.length - 1; index >= 0; index -= 1) {
    const block = stream.value[index]
    if (block?.kind === 'reasoning') return block.item
  }
  return null
})

const planningShimmerLabel = computed(() => reasoningLiveLabel(latestReasoningItem.value))

function stripReasoningMarkdown(text: string): string {
  return text
    .replace(/\*\*/g, '')
    .replace(/__/g, '')
    .replace(/^#+\s*/gm, '')
    .replace(/`+/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

/** Meaningful reasoning body — ignore whitespace-only / separator-only payloads. */
function reasoningBodyText(item: TimelineItem): string {
  const summary = (item.reasoningSummary || '').trim()
  const content = (item.reasoningContent || '').trim()
  const text = (item.text || '').trim()
  if (summary && content && summary !== content && !summary.includes(content) && !content.includes(summary)) {
    return `${summary}\n\n${content}`
  }
  return summary || content || text
}

/** Cursor-style single-line planning label (latest thought, not full body). */
function reasoningLiveLabel(item: TimelineItem | null): string {
  if (!item) return t('timeline.reasoningLive')
  const raw = reasoningBodyText(item)
  if (!raw) return t('timeline.reasoningLive')
  const lines = raw
    .split(/\n+/)
    .map((line) => stripReasoningMarkdown(line.trim()))
    .filter(Boolean)
  // Prefer the newest line so the shimmer row tracks the stream.
  const latest = lines.at(-1) || ''
  if (!latest) return t('timeline.reasoningLive')
  return latest.length > 80 ? `${latest.slice(0, 80)}…` : latest
}

function plainStreamText(item: TimelineItem): string {
  if (item.type === 'reasoning') {
    return reasoningBodyText(item)
  }
  // Streaming: do not trim — trailing newlines are required for GFM lists/paragraphs.
  return stripProposedPlanTags(item.text || '', { trim: false })
}

function stripProposedPlanTags(text: string, options?: { trim?: boolean }): string {
  const shouldTrim = options?.trim !== false
  let next = text
    .replace(/<proposed_plan>\s*/gi, '')
    .replace(/\s*<\/proposed_plan>/gi, '')
    .replace(/\n{3,}/g, '\n\n')
  if (shouldTrim) next = next.trim()
  return next
}

function isOpen(id: string): boolean {
  return Boolean(openRows.value[id])
}

function toggle(id: string): void {
  openRows.value = { ...openRows.value, [id]: !openRows.value[id] }
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

  // Match any anchor — DOMPurify may leave bare <a> without href.
  const anchor = event.target instanceof Element
    ? event.target.closest('a')
    : null
  if (anchor instanceof HTMLAnchorElement) {
    event.preventDefault()
    event.stopPropagation()
    void openMarkdownAnchor(anchor)
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

function looksLikeLocalPath(href: string): boolean {
  if (/^file:/i.test(href)) return true
  if (/^sandbox:/i.test(href)) return true
  if (/^[a-zA-Z]:[\\/]/.test(href)) return true
  if (href.startsWith('\\\\') || href.startsWith('//')) return true
  if (href.startsWith('/') || href.startsWith('./') || href.startsWith('../') || href.startsWith('.\\') || href.startsWith('..\\')) return true
  // Relative path without a URL scheme: docs/report.docx
  if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(href)) return true
  return false
}

function normalizeOpenPath(raw: string): string {
  let path = raw.trim()
  // Codex / ChatGPT style virtual paths often map to workspace files by basename.
  if (/^sandbox:/i.test(path)) {
    path = path.replace(/^sandbox:/i, '').replace(/^\/mnt\/data\//i, '')
  }
  return path.trim()
}

function resolveAnchorOpenPath(anchor: HTMLAnchorElement): string {
  const dataPath = (anchor.getAttribute('data-open-path') || '').trim()
  if (dataPath) return normalizeOpenPath(dataPath)

  const href = (anchor.getAttribute('href') || '').trim()
  if (href && href !== '#' && !href.startsWith('javascript:')) {
    return normalizeOpenPath(href)
  }

  // Bare <a>下载 Word 文档</a>: dig nearby text / this turn's file changes.
  const prose = anchor.closest('.prose, .claude-prose, .reasoning-prose')
  const scopeText = [
    prose?.textContent || '',
    props.items.map((item) => item.text || '').join('\n'),
  ].join('\n')
  const candidates = extractCandidateFilePaths(scopeText)
  const linkText = (anchor.textContent || '').toLowerCase()
  const preferExt = /\bword\b|docx?/.test(linkText)
    ? ['.docx', '.doc']
    : /\bexcel\b|xlsx?|表格/.test(linkText)
      ? ['.xlsx', '.xls', '.csv']
      : /\bpdf\b/.test(linkText)
        ? ['.pdf']
        : /\bppt|演示/.test(linkText)
          ? ['.pptx', '.ppt']
          : []

  const fromTurn = resolvedFileChanges.value.map((item) => item.path)
  const pool = [...fromTurn, ...candidates]
  if (preferExt.length) {
    const matched = pool.find((path) => preferExt.some((ext) => path.toLowerCase().endsWith(ext)))
    if (matched) return matched
  }
  return pool[0] || ''
}

async function openMarkdownAnchor(anchor: HTMLAnchorElement): Promise<void> {
  const target = resolveAnchorOpenPath(anchor)
  if (!target) {
    notify('error', t('notifications.linkOpenFailed'), t('notifications.linkMissingPath'))
    return
  }
  await openMarkdownHref(target)
}

async function openMarkdownHref(href: string): Promise<void> {
  try {
    const value = normalizeOpenPath(href)
    if (/^https?:\/\//i.test(value)) {
      await backend.OpenExternal(value)
      return
    }
    if (/^mailto:/i.test(value)) {
      await backend.OpenExternal(value)
      return
    }
    if (looksLikeLocalPath(value)) {
      await backend.OpenLocalPath(value)
      return
    }
    notify('error', t('notifications.linkOpenFailed'), t('notifications.linkUnsupported'))
  } catch (error) {
    notify(
      'error',
      t('notifications.linkOpenFailed'),
      error instanceof Error ? error.message : String(error),
    )
  }
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
  if (item.type === 'mcpToolCall') {
    // Prefer explicit "Server / tool" titles; never blank out as bare "MCP".
    if (raw && !/^mcp$/i.test(raw)) {
      return { integration: raw, action: item.detail || t('timeline.mcpTool') }
    }
    return { integration: 'MCP', action: item.detail || t('timeline.mcpTool') }
  }
  if (item.type === 'dynamicToolCall') {
    // Grok built-ins (read_file, grep, …): integration = tool name, action = path/detail.
    const tool = stripGenericToolTitle(raw)
    const action = item.detail || item.command || item.text || tool
    return {
      integration: humanizeToolLabel(tool || 'Tool'),
      action: formatToolAction(action, tool),
    }
  }
  return { integration: humanizeToolLabel(raw || item.type), action: humanizeToolLabel(raw || item.type) }
}

function toolIntegrationKey(item: TimelineItem): string {
  // Keep each Grok built-in tool in its own group (read_file ≠ grep ≠ todo_write).
  if (item.type === 'dynamicToolCall') {
    const name = stripGenericToolTitle(item.title || '').toLowerCase() || item.id
    return `dyn:${name}`
  }
  return splitToolTitle(item).integration.toLowerCase()
}

function toolIntegrationName(item: TimelineItem): string {
  if (item.type === 'dynamicToolCall') {
    const name = stripGenericToolTitle(item.title || '')
    return humanizeToolLabel(name || 'Tool')
  }
  return humanizeToolLabel(splitToolTitle(item).integration)
}

function toolActionLabel(item: TimelineItem): string {
  if (item.type === 'dynamicToolCall') {
    // Prefer "Read File · AppSidebar.vue" style row labels.
    if (item.text?.trim() && !/^tool$/i.test(item.text.trim())) return item.text.trim()
    const tool = humanizeToolLabel(stripGenericToolTitle(item.title || ''))
    const target = formatToolAction(item.detail || item.command || '', tool)
    if (tool && target && !/^tool$/i.test(tool)) return `${tool} · ${target}`
    if (tool && !/^tool$/i.test(tool)) return tool
    return target || t('timeline.mcpTool')
  }
  return formatToolAction(splitToolTitle(item).action)
}

/** Drop useless fallback titles so empty resolution doesn't paint every row as "Tool". */
function stripGenericToolTitle(value: string): string {
  const raw = value.trim()
  if (!raw) return ''
  if (/^(tool|dynamictoolcall|mcptoolcall|mcp)$/i.test(raw)) return ''
  return raw
}

function formatToolAction(value: string, toolName = ''): string {
  const raw = value.trim()
  if (!raw) return ''
  // Don't re-humanize file paths into Title Case; keep basename readable.
  if (/[\\/]/.test(raw) || /\.[a-z0-9]+$/i.test(raw)) {
    const base = raw.split(/[\\/]/).filter(Boolean).at(-1) || raw
    return base
  }
  if (toolName && raw.toLowerCase() === toolName.toLowerCase()) {
    return humanizeToolLabel(raw)
  }
  return humanizeToolLabel(raw)
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

function toolGroupHeading(block: { integrationName: string, items: TimelineItem[] }): string {
  const name = (block.integrationName || '').trim()
  const allDynamic = block.items.every((item) => item.type === 'dynamicToolCall')
  // Grok built-ins are tools, not "integrations".
  if (allDynamic) {
    if (!name || /^tool$/i.test(name)) return t('timeline.usedToolsGeneric')
    return t('timeline.usedTool', { name })
  }
  if (!name || /^tool$/i.test(name)) return t('timeline.usedToolsGeneric')
  return t('timeline.usedIntegration', { name })
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
    class="group w-full"
    :class="[
      animateEnter ? 'timeline-message-enter' : '',
      streaming
        ? ''
        : kind === 'user'
          ? '[content-visibility:auto] [contain-intrinsic-size:88px]'
          : '[content-visibility:auto] [contain-intrinsic-size:200px]',
    ]"
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
        <TooltipProvider v-if="allowTurnActions && isFailed">
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

    <!-- Agent stream — CSS enter only (no TransitionGroup FLIP while streaming). -->
    <div v-else class="space-y-2">
      <div class="timeline-step-list space-y-1.5">
        <div
          v-for="block in stream"
          v-show="block.kind !== 'reasoning'"
          :key="streamBlockKey(block)"
          class="timeline-step-item"
          :class="animateEnter ? 'timeline-step-item--enter' : ''"
        >
          <!-- Reasoning is rendered only as the live shimmer row below (hidden when done). -->

          <!-- Plan checklist (update_plan) -->
          <div v-if="block.kind === 'plan'" class="space-y-1.5 py-0.5">
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
                v-html="markdownHTML(block.item.text, block.item)"
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
              <ChevronRight
                v-if="block.item.output"
                :size="11"
                class="timeline-chevron shrink-0 opacity-40"
                :class="stepOpen(block.item.id, false) ? 'is-open' : ''"
              />
            </button>
            <div
              class="timeline-collapse"
              :class="stepOpen(block.item.id, false) && block.item.output ? 'is-open' : ''"
            >
              <div class="timeline-collapse-inner">
                <div v-if="block.item.output" class="relative mt-1 ml-1">
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
              <ChevronRight
                :size="11"
                class="timeline-chevron shrink-0 opacity-40"
                :class="stepOpen(block.item.id, true) ? 'is-open' : ''"
              />
            </button>
            <div class="timeline-collapse" :class="stepOpen(block.item.id, true) ? 'is-open' : ''">
              <div class="timeline-collapse-inner">
                <div class="space-y-0.5 pl-2 pt-0.5">
                  <button
                    v-if="block.nestedCommand"
                    type="button"
                    class="flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 text-left text-[12px] text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground"
                    :class="isError(block.nestedCommand.status, block.nestedCommand.failed) ? 'text-destructive' : ''"
                    @click="toggleStep(block.nestedCommand.id, false)"
                  >
                    <Terminal :size="12" class="shrink-0 opacity-50" />
                    <span class="min-w-0 truncate font-mono text-[11.5px]">{{ commandRanLabel(block.nestedCommand.command) }}</span>
                    <ChevronRight
                      v-if="block.nestedCommand.output"
                      :size="11"
                      class="timeline-chevron ml-auto shrink-0 opacity-40"
                      :class="stepOpen(block.nestedCommand.id, false) ? 'is-open' : ''"
                    />
                  </button>
                  <div
                    class="timeline-collapse"
                    :class="block.nestedCommand && stepOpen(block.nestedCommand.id, false) && block.nestedCommand.output ? 'is-open' : ''"
                  >
                    <div class="timeline-collapse-inner">
                      <div v-if="block.nestedCommand?.output" class="relative ml-1">
                        <pre
                          class="tool-payload max-h-36 overflow-auto rounded-xl bg-muted/45 px-3 py-2 font-mono text-[11px] leading-5 text-foreground"
                          v-html="renderToolPayloadHTML(block.nestedCommand.output)"
                        />
                      </div>
                    </div>
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
              <span class="min-w-0 truncate">{{ toolGroupHeading(block) }}</span>
              <span
                v-if="block.items.length > 1"
                class="shrink-0 tabular-nums text-[10px] opacity-50"
              >{{ block.items.length }}</span>
              <ChevronRight
                :size="11"
                class="timeline-chevron shrink-0 opacity-40"
                :class="toolGroupOpen(block.id, block.items) ? 'is-open' : ''"
              />
            </button>

            <div
              class="timeline-collapse"
              :class="toolGroupOpen(block.id, block.items) ? 'is-open' : ''"
            >
              <div class="timeline-collapse-inner">
                <div class="mt-0.5 space-y-0.5 pl-1">
                  <div
                    v-for="item in block.items"
                    :key="item.id"
                    class="min-w-0"
                    :class="animateEnter ? 'timeline-tool-row--enter' : ''"
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
                      <ChevronRight
                        v-if="item.detail || item.output"
                        :size="11"
                        class="timeline-chevron shrink-0 opacity-40"
                        :class="isOpen(item.id) ? 'is-open' : ''"
                      />
                    </button>

                    <div class="timeline-collapse" :class="isOpen(item.id) ? 'is-open' : ''">
                      <div class="timeline-collapse-inner">
                        <div class="mt-1 ml-1 space-y-1.5">
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
                v-html="injectStreamingCaret(markdownHTML(plainStreamText(block.item), block.item))"
              />
            </template>
            <div
              v-else
              class="prose max-w-none stream-settle prose-headings:mb-2.5 prose-headings:mt-4 prose-headings:text-[1.05em] prose-headings:font-semibold prose-headings:tracking-tight prose-p:my-2.5 prose-p:leading-7 prose-li:my-1 prose-ul:my-2.5 prose-ol:my-2.5 prose-pre:my-3 prose-pre:rounded-xl prose-code:rounded-md prose-code:px-1.5 prose-code:py-0.5 prose-code:text-[0.86em] prose-code:before:content-none prose-code:after:content-none prose-a:font-medium prose-strong:font-semibold"
              @click="onMarkdownClick"
              v-html="markdownHTML(stripProposedPlanTags(block.item.text), block.item)"
            />
          </div>
        </div>

        <!-- Cursor-style sweep: base text always visible; sheen is a masked overlay only. -->
        <div
          v-if="showPlanningShimmer"
          key="reasoning-live"
          class="reasoning-live-row timeline-step-item--enter flex min-w-0 items-center py-1"
          :aria-label="planningShimmerLabel"
        >
          <span class="reasoning-shimmer min-w-0 max-w-full">
            <span class="reasoning-shimmer__base truncate text-[13px]">{{ planningShimmerLabel }}</span>
            <span class="reasoning-shimmer__sheen truncate text-[13px]" aria-hidden="true">{{ planningShimmerLabel }}</span>
          </span>
        </div>
      </div>

      <!-- Consolidated file change list — only after the turn finishes (not live mid-run). -->
      <div
        v-if="resolvedFileChanges.length && !streaming"
        class="mt-1 space-y-0.5 border-t border-border/50 pt-2"
        :class="animateEnter ? 'timeline-step-item--enter' : ''"
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
          <ChevronRight
            :size="11"
            class="timeline-chevron opacity-40"
            :class="turnFilesOpen ? 'is-open' : ''"
          />
        </button>
        <div class="timeline-collapse" :class="turnFilesOpen ? 'is-open' : ''">
          <div class="timeline-collapse-inner">
            <div class="space-y-0.5 pl-2 pt-0.5">
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
        </div>
      </div>

      <div class="flex min-h-5 items-center gap-2 pt-0.5">
        <!-- Token/duration footer belongs on agent turns only (not user bubbles). -->
        <div
          v-if="kind === 'agent' && (metrics?.tokenUsage || metrics?.durationMs || (!streaming && turnFileTotals.count))"
          class="flex items-center gap-1.5 text-[11px] tabular-nums text-muted-foreground/65"
        >
          <span v-if="metrics?.durationMs">{{ t('timeline.processed', { value: formatDuration(metrics.durationMs) }) }}</span>
          <span v-if="metrics?.durationMs && (metrics?.tokenUsage || (!streaming && turnFileTotals.count))">·</span>
          <span v-if="metrics?.tokenUsage" class="inline-flex max-w-full flex-wrap items-center gap-x-1.5 gap-y-0.5">
            <span>
              {{ (
                metrics.tokenUsage.totalTokens
                || (
                  metrics.tokenUsage.inputTokens
                  + metrics.tokenUsage.cachedInputTokens
                  + metrics.tokenUsage.outputTokens
                  + metrics.tokenUsage.reasoningOutputTokens
                )
              ).toLocaleString() }} tokens
            </span>
            <span
              v-if="metrics.tokenUsage.inputTokens || metrics.tokenUsage.outputTokens || metrics.tokenUsage.cachedInputTokens || metrics.tokenUsage.reasoningOutputTokens"
              class="text-muted-foreground/55"
            >
              (
              <template v-if="metrics.tokenUsage.inputTokens">↑{{ metrics.tokenUsage.inputTokens.toLocaleString() }}</template>
              <template v-if="metrics.tokenUsage.cachedInputTokens">
                <span v-if="metrics.tokenUsage.inputTokens"> · </span>cache {{ metrics.tokenUsage.cachedInputTokens.toLocaleString() }}
              </template>
              <template v-if="metrics.tokenUsage.outputTokens">
                <span v-if="metrics.tokenUsage.inputTokens || metrics.tokenUsage.cachedInputTokens"> · </span>↓{{ metrics.tokenUsage.outputTokens.toLocaleString() }}
              </template>
              <template v-if="metrics.tokenUsage.reasoningOutputTokens">
                <span v-if="metrics.tokenUsage.inputTokens || metrics.tokenUsage.cachedInputTokens || metrics.tokenUsage.outputTokens"> · </span>think {{ metrics.tokenUsage.reasoningOutputTokens.toLocaleString() }}
              </template>
              )
            </span>
          </span>
          <span v-if="metrics?.tokenUsage && !streaming && turnFileTotals.count">·</span>
          <span v-if="!streaming && turnFileTotals.count" class="inline-flex items-center gap-1">
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
          <DropdownMenu v-if="allowTurnActions && turnId && !streaming">
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
